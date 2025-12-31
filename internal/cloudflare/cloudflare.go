package cloudflare

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const apiBase = "https://api.cloudflare.com/client/v4"

// Client is a Cloudflare API client for tunnel management.
type Client struct {
	token      string
	httpClient *http.Client
}

// NewClient creates a new Cloudflare API client with the given API token.
func NewClient(token string) *Client {
	return &Client{
		token: token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Account represents a Cloudflare account.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Tunnel represents a Cloudflare Tunnel.
type Tunnel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccountTag  string `json:"account_tag"`
	CreatedAt   string `json:"created_at"`
	DeletedAt   string `json:"deleted_at,omitempty"`
	Status      string `json:"status"`
	Credentials string `json:"credentials_file,omitempty"`
}

// TunnelCredentials contains the credentials for a tunnel.
type TunnelCredentials struct {
	AccountTag   string `json:"AccountTag"`
	TunnelID     string `json:"TunnelID"`
	TunnelName   string `json:"TunnelName"`
	TunnelSecret string `json:"TunnelSecret"`
}

type apiResponse[T any] struct {
	Success  bool       `json:"success"`
	Errors   []apiError `json:"errors"`
	Messages []string   `json:"messages"`
	Result   T          `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ListAccounts returns all accounts the API token has access to.
func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	var resp apiResponse[[]Account]
	if err := c.doRequest(ctx, "GET", "/accounts", nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("cloudflare: %v", resp.Errors)
	}
	return resp.Result, nil
}

// GetAccountID returns the account ID. If there's exactly one account, returns it.
// Otherwise returns an error asking for explicit account ID.
func (c *Client) GetAccountID(ctx context.Context) (string, error) {
	accounts, err := c.ListAccounts(ctx)
	if err != nil {
		return "", err
	}
	if len(accounts) == 0 {
		return "", fmt.Errorf("no accounts found for this API token")
	}
	if len(accounts) > 1 {
		return "", fmt.Errorf("multiple accounts found, please specify --account-id")
	}
	return accounts[0].ID, nil
}

// ListTunnels returns all tunnels for the given account.
func (c *Client) ListTunnels(ctx context.Context, accountID string) ([]Tunnel, error) {
	var resp apiResponse[[]Tunnel]
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel", accountID)
	if err := c.doRequest(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("cloudflare: %v", resp.Errors)
	}
	return resp.Result, nil
}

// FindTunnel looks for an existing tunnel by name. Returns nil if not found.
func (c *Client) FindTunnel(ctx context.Context, accountID, name string) (*Tunnel, error) {
	tunnels, err := c.ListTunnels(ctx, accountID)
	if err != nil {
		return nil, err
	}
	for _, t := range tunnels {
		if t.Name == name && t.DeletedAt == "" {
			return &t, nil
		}
	}
	return nil, nil
}

// CreateTunnel creates a new Cloudflare Tunnel and returns its credentials.
func (c *Client) CreateTunnel(ctx context.Context, accountID, name string) (*Tunnel, *TunnelCredentials, error) {
	// Generate a random secret for the tunnel
	secret, err := generateTunnelSecret()
	if err != nil {
		return nil, nil, fmt.Errorf("generate tunnel secret: %w", err)
	}

	body := map[string]any{
		"name":          name,
		"tunnel_secret": secret,
	}

	var resp apiResponse[Tunnel]
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel", accountID)
	if err := c.doRequest(ctx, "POST", path, body, &resp); err != nil {
		return nil, nil, err
	}
	if !resp.Success {
		return nil, nil, fmt.Errorf("cloudflare: %v", resp.Errors)
	}

	tunnel := resp.Result
	creds := &TunnelCredentials{
		AccountTag:   accountID,
		TunnelID:     tunnel.ID,
		TunnelName:   tunnel.Name,
		TunnelSecret: secret,
	}

	return &tunnel, creds, nil
}

// GetTunnelToken returns a token that can be used to run cloudflared.
func (c *Client) GetTunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	var resp apiResponse[string]
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/token", accountID, tunnelID)
	if err := c.doRequest(ctx, "GET", path, nil, &resp); err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("cloudflare: %v", resp.Errors)
	}
	return resp.Result, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}

// generateTunnelSecret generates a random base64-encoded secret for tunnel creation.
func generateTunnelSecret() (string, error) {
	// Use crypto/rand to generate 32 random bytes
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	// Encode as base64
	return base64.StdEncoding.EncodeToString(b), nil
}
