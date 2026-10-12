package httpapi

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HealthHandler serves GET /health for the hosting platform's liveness
// checks. It does not require authentication.
func HealthHandler(pool *pgxpool.Pool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
