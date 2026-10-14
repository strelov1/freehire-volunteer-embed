package db

import (
	"context"
	"testing"
)

func TestOpen_EmptyDatabaseURL_ReturnsError(t *testing.T) {
	_, err := Open(context.Background(), "", 10)

	if err == nil {
		t.Fatal("expected an error for an empty database URL, got nil")
	}
}

func TestOpen_MalformedDatabaseURL_ReturnsError(t *testing.T) {
	_, err := Open(context.Background(), "not a valid postgres dsn", 10)

	if err == nil {
		t.Fatal("expected an error for a malformed database URL, got nil")
	}
}

// TestOpen_SetsMaxConns guards the fix for a real incident: Submit() fans
// out submitConcurrency goroutines per request, each making several
// sequential queries, against a pool that previously had no explicit size
// at all (pgx's own small default) -- a single request's own fan-out could
// already exhaust it before factoring in any other volunteer's concurrent
// request. pgxpool.NewWithConfig does not connect eagerly, so this needs no
// real database.
func TestOpen_SetsMaxConns(t *testing.T) {
	pool, err := Open(context.Background(), "postgres://user:pass@127.0.0.1:1/db", 42)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	if got := pool.Config().MaxConns; got != 42 {
		t.Errorf("MaxConns = %d, want 42", got)
	}
}
