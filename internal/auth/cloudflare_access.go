package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	cfAccessJWTHeader  = "Cf-Access-Jwt-Assertion"
	cfAccessCertPath   = "/cdn-cgi/access/certs"
	keysCacheDuration  = 1 * time.Hour
)

type CloudflareAccessConfig struct {
	TeamDomain string
	PolicyAUD  string
}

type CloudflareAccessAuthenticator struct {
	config     CloudflareAccessConfig
	httpClient *http.Client

	mu         sync.RWMutex
	keys       map[string]*rsa.PublicKey
	keysExpiry time.Time
}

func NewCloudflareAccessAuthenticator(cfg CloudflareAccessConfig) *CloudflareAccessAuthenticator {
	return &CloudflareAccessAuthenticator{
		config:     cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		keys:       make(map[string]*rsa.PublicKey),
	}
}

func (c *CloudflareAccessAuthenticator) Type() BrowserAuthType {
	return BrowserAuthCloudflareAccess
}

func (c *CloudflareAccessAuthenticator) Authenticate(r *http.Request) (*BrowserUser, error) {
	token := r.Header.Get(cfAccessJWTHeader)
	if token == "" {
		return nil, ErrNotAuthenticated
	}

	claims, err := c.validateToken(token)
	if err != nil {
		return nil, fmt.Errorf("validate token: %w", err)
	}

	return &BrowserUser{
		Email:    claims.Email,
		ID:       claims.Subject,
		Provider: "cloudflare_access",
	}, nil
}

type cfAccessClaims struct {
	Audience  interface{} `json:"aud"`
	Email     string      `json:"email"`
	Subject   string      `json:"sub"`
	IssuedAt  int64       `json:"iat"`
	ExpiresAt int64       `json:"exp"`
	Issuer    string      `json:"iss"`
	Type      string      `json:"type"`
}

func (c *CloudflareAccessAuthenticator) validateToken(tokenStr string) (*cfAccessClaims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	headerJSON, err := base64URLDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}

	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}

	if header.Alg != "RS256" {
		return nil, fmt.Errorf("unsupported algorithm: %s", header.Alg)
	}

	key, err := c.getPublicKey(header.Kid)
	if err != nil {
		return nil, fmt.Errorf("get public key: %w", err)
	}

	signature, err := base64URLDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	signedContent := parts[0] + "." + parts[1]
	hash := sha256.Sum256([]byte(signedContent))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature); err != nil {
		return nil, fmt.Errorf("invalid signature: %w", err)
	}

	claimsJSON, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	var claims cfAccessClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}

	now := time.Now().Unix()
	if claims.ExpiresAt < now {
		return nil, fmt.Errorf("token expired")
	}
	if claims.IssuedAt > now+60 {
		return nil, fmt.Errorf("token issued in the future")
	}

	if c.config.PolicyAUD != "" {
		if !c.audienceMatches(claims.Audience) {
			return nil, fmt.Errorf("audience mismatch")
		}
	}

	return &claims, nil
}

func (c *CloudflareAccessAuthenticator) audienceMatches(aud interface{}) bool {
	switch v := aud.(type) {
	case string:
		return v == c.config.PolicyAUD
	case []interface{}:
		for _, a := range v {
			if s, ok := a.(string); ok && s == c.config.PolicyAUD {
				return true
			}
		}
	}
	return false
}

func (c *CloudflareAccessAuthenticator) getPublicKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	if time.Now().Before(c.keysExpiry) {
		if key, ok := c.keys[kid]; ok {
			c.mu.RUnlock()
			return key, nil
		}
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Now().Before(c.keysExpiry) {
		if key, ok := c.keys[kid]; ok {
			return key, nil
		}
	}

	if err := c.fetchKeys(); err != nil {
		return nil, err
	}

	key, ok := c.keys[kid]
	if !ok {
		return nil, fmt.Errorf("key %q not found", kid)
	}
	return key, nil
}

func (c *CloudflareAccessAuthenticator) fetchKeys() error {
	certsURL := fmt.Sprintf("https://%s%s", c.config.TeamDomain, cfAccessCertPath)

	resp, err := c.httpClient.Get(certsURL)
	if err != nil {
		return fmt.Errorf("fetch certs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch certs: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read certs: %w", err)
	}

	var certsResp struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
		PublicCert  struct{} `json:"public_cert,omitempty"`
		PublicCerts []struct {
			Kid  string `json:"kid"`
			Cert string `json:"cert"`
		} `json:"public_certs,omitempty"`
	}
	if err := json.Unmarshal(body, &certsResp); err != nil {
		return fmt.Errorf("parse certs: %w", err)
	}

	c.keys = make(map[string]*rsa.PublicKey)
	for _, jwk := range certsResp.Keys {
		if jwk.Kty != "RSA" {
			continue
		}
		key, err := jwkToRSAPublicKey(jwk.N, jwk.E)
		if err != nil {
			continue
		}
		c.keys[jwk.Kid] = key
	}

	c.keysExpiry = time.Now().Add(keysCacheDuration)
	return nil
}

func jwkToRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64URLDecode(nStr)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)

	eBytes, err := base64URLDecode(eStr)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}

	return &rsa.PublicKey{N: n, E: e}, nil
}

func base64URLDecode(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")

	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}

	return base64.StdEncoding.DecodeString(s)
}
