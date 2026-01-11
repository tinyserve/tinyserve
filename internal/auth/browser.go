package auth

import (
	"context"
	"errors"
	"net/http"
)

type BrowserAuthType string

const (
	BrowserAuthNone             BrowserAuthType = "none"
	BrowserAuthCloudflareAccess BrowserAuthType = "cloudflare_access"
)

type BrowserUser struct {
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider,omitempty"`
}

type BrowserAuthenticator interface {
	Type() BrowserAuthType
	Authenticate(r *http.Request) (*BrowserUser, error)
}

var ErrNotAuthenticated = errors.New("not authenticated")
var ErrAuthMisconfigured = errors.New("authentication misconfigured")

type browserUserKey struct{}

func BrowserUserFromContext(ctx context.Context) *BrowserUser {
	if v := ctx.Value(browserUserKey{}); v != nil {
		return v.(*BrowserUser)
	}
	return nil
}

func ContextWithBrowserUser(ctx context.Context, user *BrowserUser) context.Context {
	return context.WithValue(ctx, browserUserKey{}, user)
}

type NoopAuthenticator struct{}

func (n *NoopAuthenticator) Type() BrowserAuthType {
	return BrowserAuthNone
}

func (n *NoopAuthenticator) Authenticate(r *http.Request) (*BrowserUser, error) {
	return nil, nil
}
