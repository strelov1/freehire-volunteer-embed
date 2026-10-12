package httpapi

import (
	"net/http"

	"github.com/strelov1/freehire-volunteer-embed/internal/ratelimit"
)

// RateLimitByKey rejects requests once the authenticated caller's budget is
// exhausted. It must run behind RequireEmbedScope, which supplies the
// caller's key id via context.
func RateLimitByKey(limiter *ratelimit.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			keyID, ok := KeyIDFromContext(r.Context())
			if !ok {
				http.Error(w, "internal error: missing authenticated key", http.StatusInternalServerError)
				return
			}
			if !limiter.Allow(keyID) {
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
