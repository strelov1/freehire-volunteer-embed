// Package httpapi wires the lease/submit HTTP surface, including the
// bearer-token auth middleware every route runs behind.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/auth"
)

type contextKey int

const keyIDContextKey contextKey = 0

// KeyIDFromContext returns the authenticated caller's api_keys.id, set by
// RequireEmbedScope. ok is false if called outside that middleware.
func KeyIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(keyIDContextKey).(int64)
	return id, ok
}

// RequireEmbedScope authenticates the Authorization header against
// freehire's api_keys (scope=embed, not expired, not banned) before calling
// next, rejecting with 401 otherwise.
func RequireEmbedScope(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			keyID, err := auth.Authenticate(r.Context(), pool, r.Header.Get("Authorization"))
			if errors.Is(err, auth.ErrUnauthorized) {
				writeUnauthorized(w)
				return
			}
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			ctx := context.WithValue(r.Context(), keyIDContextKey, keyID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}
