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
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	AccountTag  string          `json:"account_tag"`
	CreatedAt   string          `json:"created_at"`
	DeletedAt   string          `json:"deleted_at,omitempty"`
	Status      string          `json:"status"`
	Credentials json.RawMessage `json:"credentials_file,omitempty"`
}

// TunnelCredentials contains the credentials for a tunnel.
type TunnelCredentials struct {
	AccountTag   string `json:"AccountTag"`
	TunnelID     string `json:"TunnelID"`
	TunnelName   string `json:"TunnelName"`
	TunnelSecret string `json:"TunnelSecret"`
}

// Zone represents a Cloudflare DNS zone.
type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// DNSRecord represents a DNS record in a zone.
type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

// PurgeCacheRequest represents a cache purge request.
type PurgeCacheRequest struct {
	PurgeEverything bool     `json:"purge_everything,omitempty"`
	Files           []string `json:"files,omitempty"`
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

// GetZoneID returns the zone ID for the given zone name.
func (c *Client) GetZoneID(ctx context.Context, zoneName string) (string, error) {
	return c.FindZoneForHostname(ctx, zoneName)
}

// FindZoneForHostname finds the zone that contains the given hostname.
// It walks up the domain hierarchy to find a matching zone.
// E.g., for "staging.tinyserve.org" it tries: staging.tinyserve.org, tinyserve.org, org
func (c *Client) FindZoneForHostname(ctx context.Context, hostname string) (string, error) {
	parts := splitDomain(hostname)
	for i := 0; i < len(parts)-1; i++ {
		candidate := joinDomain(parts[i:])
		var resp apiResponse[[]Zone]
		path := fmt.Sprintf("/zones?name=%s&status=active", candidate)
		if err := c.doRequest(ctx, "GET", path, nil, &resp); err != nil {
			return "", err
		}
		if resp.Success && len(resp.Result) > 0 {
			return resp.Result[0].ID, nil
		}
	}
	return "", fmt.Errorf("no zone found for hostname: %s", hostname)
}

func splitDomain(domain string) []string {
	var parts []string
	current := ""
	for i := len(domain) - 1; i >= 0; i-- {
		if domain[i] == '.' {
			if current != "" {
				parts = append([]string{current}, parts...)
			}
			current = ""
		} else {
			current = string(domain[i]) + current
		}
	}
	if current != "" {
		parts = append([]string{current}, parts...)
	}
	return parts
}

func joinDomain(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "."
		}
		result += p
	}
	return result
}

// ListDNSRecords returns DNS records matching the given filters.
func (c *Client) ListDNSRecords(ctx context.Context, zoneID, recordType, name string) ([]DNSRecord, error) {
	var resp apiResponse[[]DNSRecord]
	path := fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", zoneID, recordType, name)
	if err := c.doRequest(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("cloudflare: %v", resp.Errors)
	}
	return resp.Result, nil
}

// EnsureCNAME ensures a CNAME record exists with the correct target.
// Creates the record if it doesn't exist, updates it if the content differs.
// Deletes any conflicting A/AAAA records first.
func (c *Client) EnsureCNAME(ctx context.Context, zoneID, name, target string, proxied bool) error {
	// First, delete any conflicting A or AAAA records
	for _, recordType := range []string{"A", "AAAA"} {
		conflicting, err := c.ListDNSRecords(ctx, zoneID, recordType, name)
		if err != nil {
			return fmt.Errorf("list %s records: %w", recordType, err)
		}
		fmt.Printf("DEBUG EnsureCNAME: found %d %s records for %q\n", len(conflicting), recordType, name)
		for _, rec := range conflicting {
			fmt.Printf("DEBUG EnsureCNAME: deleting %s record %s (%s)\n", recordType, rec.ID, rec.Content)
			if err := c.DeleteDNSRecord(ctx, zoneID, rec.ID); err != nil {
				return fmt.Errorf("delete conflicting %s record: %w", recordType, err)
			}
		}
	}

	records, err := c.ListDNSRecords(ctx, zoneID, "CNAME", name)
	if err != nil {
		return err
	}

	record := DNSRecord{
		Type:    "CNAME",
		Name:    name,
		Content: target,
		TTL:     1,
		Proxied: proxied,
	}

	if len(records) == 0 {
		var resp apiResponse[DNSRecord]
		path := fmt.Sprintf("/zones/%s/dns_records", zoneID)
		if err := c.doRequest(ctx, "POST", path, record, &resp); err != nil {
			return err
		}
		if !resp.Success {
			return fmt.Errorf("cloudflare: %v", resp.Errors)
		}
		return nil
	}

	existing := records[0]
	if existing.Content == target && existing.Proxied == proxied {
		return nil
	}

	var resp apiResponse[DNSRecord]
	path := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, existing.ID)
	if err := c.doRequest(ctx, "PUT", path, record, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("cloudflare: %v", resp.Errors)
	}
	return nil
}

// DeleteDNSRecord deletes a DNS record by ID.
func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	var resp apiResponse[struct{}]
	path := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID)
	if err := c.doRequest(ctx, "DELETE", path, nil, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("cloudflare: %v", resp.Errors)
	}
	return nil
}

// PurgeCache purges cache for a zone based on the request.
func (c *Client) PurgeCache(ctx context.Context, zoneID string, req PurgeCacheRequest) error {
	var resp apiResponse[map[string]any]
	path := fmt.Sprintf("/zones/%s/purge_cache", zoneID)
	if err := c.doRequest(ctx, "POST", path, req, &resp); err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("cloudflare: %v", resp.Errors)
	}
	return nil
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
