package api

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"tinyserve/internal/auth"
	"tinyserve/internal/state"
)

type AuthMiddleware struct {
	store state.Store
}

func NewAuthMiddleware(store state.Store) *AuthMiddleware {
	return &AuthMiddleware{store: store}
}

type BrowserAuthMiddleware struct {
	store state.Store

	mu            sync.RWMutex
	authenticator auth.BrowserAuthenticator
	lastConfig    state.BrowserAuthSettings
}

func NewBrowserAuthMiddleware(store state.Store) *BrowserAuthMiddleware {
	return &BrowserAuthMiddleware{store: store}
}

func (m *BrowserAuthMiddleware) getAuthenticator(cfg state.BrowserAuthSettings) auth.BrowserAuthenticator {
	m.mu.RLock()
	if m.authenticator != nil && m.lastConfig == cfg {
		a := m.authenticator
		m.mu.RUnlock()
		return a
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.authenticator != nil && m.lastConfig == cfg {
		return m.authenticator
	}

	switch cfg.Type {
	case string(auth.BrowserAuthCloudflareAccess):
		m.authenticator = auth.NewCloudflareAccessAuthenticator(auth.CloudflareAccessConfig{
			TeamDomain: cfg.TeamDomain,
			PolicyAUD:  cfg.PolicyAUD,
		})
	default:
		if cfg.Type != "" && cfg.Type != string(auth.BrowserAuthNone) {
			log.Printf("WARNING: unknown browser auth type %q, falling back to no authentication", cfg.Type)
		}
		m.authenticator = &auth.NoopAuthenticator{}
	}
	m.lastConfig = cfg
	return m.authenticator
}

func (m *BrowserAuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		st, err := m.store.Load(ctx)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		cfg := st.Settings.Remote.BrowserAuth
		if cfg.Type == "" || cfg.Type == string(auth.BrowserAuthNone) {
			next.ServeHTTP(w, r)
			return
		}

		authenticator := m.getAuthenticator(cfg)
		user, err := authenticator.Authenticate(r)
		if err != nil {
			if err == auth.ErrNotAuthenticated {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			http.Error(w, "authentication failed", http.StatusForbidden)
			return
		}

		if user != nil {
			ctx = auth.ContextWithBrowserUser(ctx, user)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *AuthMiddleware) RequireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		st, err := m.store.Load(ctx)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if len(st.Tokens) == 0 {
			next(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "authorization required", http.StatusUnauthorized)
			return
		}

		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "invalid authorization header", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if !auth.IsValidTokenFormat(token) {
			http.Error(w, "invalid token format", http.StatusUnauthorized)
			return
		}

		var matchedToken *state.APIToken
		for i := range st.Tokens {
			if auth.VerifyToken(token, st.Tokens[i].Hash) {
				matchedToken = &st.Tokens[i]
				break
			}
		}

		if matchedToken == nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		now := time.Now().UTC()
		matchedToken.LastUsed = &now
		m.store.Save(ctx, st)

		ctx = context.WithValue(ctx, tokenContextKey, matchedToken)
		next(w, r.WithContext(ctx))
	}
}

type contextKey string

const tokenContextKey contextKey = "api_token"

func TokenFromContext(ctx context.Context) *state.APIToken {
	if v := ctx.Value(tokenContextKey); v != nil {
		return v.(*state.APIToken)
	}
	return nil
}
