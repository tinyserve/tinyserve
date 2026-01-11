package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoopAuthenticator(t *testing.T) {
	a := &NoopAuthenticator{}

	if a.Type() != BrowserAuthNone {
		t.Errorf("Type() = %v, want %v", a.Type(), BrowserAuthNone)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	user, err := a.Authenticate(req)
	if err != nil {
		t.Errorf("Authenticate() error = %v, want nil", err)
	}
	if user != nil {
		t.Errorf("Authenticate() user = %v, want nil", user)
	}
}

func TestCloudflareAccessAuthenticator_NoHeader(t *testing.T) {
	a := NewCloudflareAccessAuthenticator(CloudflareAccessConfig{
		TeamDomain: "example.cloudflareaccess.com",
		PolicyAUD:  "test-aud",
	})

	if a.Type() != BrowserAuthCloudflareAccess {
		t.Errorf("Type() = %v, want %v", a.Type(), BrowserAuthCloudflareAccess)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	user, err := a.Authenticate(req)
	if err != ErrNotAuthenticated {
		t.Errorf("Authenticate() error = %v, want ErrNotAuthenticated", err)
	}
	if user != nil {
		t.Errorf("Authenticate() user = %v, want nil", user)
	}
}

func TestCloudflareAccessAuthenticator_InvalidJWT(t *testing.T) {
	a := NewCloudflareAccessAuthenticator(CloudflareAccessConfig{
		TeamDomain: "example.cloudflareaccess.com",
		PolicyAUD:  "test-aud",
	})

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"garbage", "not-a-jwt"},
		{"two-parts", "header.payload"},
		{"four-parts", "a.b.c.d"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.token != "" {
				req.Header.Set("Cf-Access-Jwt-Assertion", tc.token)
			}

			user, err := a.Authenticate(req)
			if tc.token == "" {
				if err != ErrNotAuthenticated {
					t.Errorf("Authenticate() error = %v, want ErrNotAuthenticated", err)
				}
			} else {
				if err == nil {
					t.Error("Authenticate() expected error, got nil")
				}
			}
			if user != nil {
				t.Errorf("Authenticate() user = %v, want nil", user)
			}
		})
	}
}

func TestBrowserUserContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := req.Context()

	user := BrowserUserFromContext(ctx)
	if user != nil {
		t.Errorf("BrowserUserFromContext() = %v, want nil", user)
	}

	testUser := &BrowserUser{
		Email:    "test@example.com",
		ID:       "user-123",
		Provider: "cloudflare_access",
	}

	ctx = ContextWithBrowserUser(ctx, testUser)
	user = BrowserUserFromContext(ctx)
	if user == nil {
		t.Fatal("BrowserUserFromContext() = nil, want user")
	}
	if user.Email != testUser.Email {
		t.Errorf("Email = %v, want %v", user.Email, testUser.Email)
	}
	if user.ID != testUser.ID {
		t.Errorf("ID = %v, want %v", user.ID, testUser.ID)
	}
	if user.Provider != testUser.Provider {
		t.Errorf("Provider = %v, want %v", user.Provider, testUser.Provider)
	}
}

func TestBase64URLDecode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"dGVzdA", "test", false},
		{"dGVzdA==", "test", false},
		{"aGVsbG8gd29ybGQ", "hello world", false},
		{"aGVsbG8td29ybGQ", "hello-world", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, err := base64URLDecode(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if string(result) != tc.expected {
				t.Errorf("got %q, want %q", string(result), tc.expected)
			}
		})
	}
}
