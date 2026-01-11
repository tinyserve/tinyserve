package api

import (
	"context"
	"net/http"
	"strings"
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
