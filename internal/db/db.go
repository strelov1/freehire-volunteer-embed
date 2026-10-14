// Package db opens the connection pool to freehire's shared production
// Postgres database, using a role scoped to exactly what this service needs.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open connects with a pool capped at maxConns. Submit's own bounded
// concurrency (internal/submit.submitConcurrency) fans out several
// goroutines per request, each making multiple sequential queries — an
// unsized pool could be exhausted by a single request's own fan-out before
// even factoring in another volunteer's concurrent request.
func Open(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("db: databaseURL is empty")
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	poolConfig.MaxConns = maxConns

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	return pool, nil
}
