// Command volunteer-embed runs the lease/submit HTTP service that lets
// internet volunteers help drain freehire's semantic_outbox embedding backlog.
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/db"
	"github.com/strelov1/freehire-volunteer-embed/internal/httpapi"
	"github.com/strelov1/freehire-volunteer-embed/internal/ratelimit"
)

// maxRequestBodyBytes caps a lease/submit request body. A submit batch of
// 500 jobs' worth of 768-float vectors, JSON-encoded, runs to roughly 8 MiB
// (500 * 768 floats * ~21 bytes/float as text) — this leaves headroom above
// that for whatever batch size a volunteer's LEASE_BATCH_SIZE_MAX allows.
const maxRequestBodyBytes = 16 << 20 // 16 MiB

// requestsPerSecond/burst bound how often a single key may call lease or
// submit. Generous enough for a legitimate volunteer's steady loop, tight
// enough to stop one misbehaving key from hammering this service.
const requestsPerSecond = 2
const burst = 10

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	if err := db.ApplyMigrations(ctx, pool); err != nil {
		log.Fatalf("db: apply migrations: %v", err)
	}

	limiter := ratelimit.New(requestsPerSecond, burst)
	requireAuth := httpapi.RequireEmbedScope(pool)
	rateLimit := httpapi.RateLimitByKey(limiter)
	bodyLimit := httpapi.LimitBodySize(maxRequestBodyBytes)

	authenticated := func(h http.Handler) http.Handler {
		return bodyLimit(requireAuth(rateLimit(h)))
	}

	mux := http.NewServeMux()
	mux.Handle("GET /health", httpapi.HealthHandler(pool))
	mux.Handle("POST /v1/embed-tasks/lease", authenticated(httpapi.LeaseHandler(pool, cfg)))
	mux.Handle("POST /v1/embed-tasks/submit", authenticated(httpapi.SubmitHandler(pool, cfg)))

	log.Printf("volunteer-embed listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
