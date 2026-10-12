package hiredata

import (
	"context"
	"testing"
	"time"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestLookupAPIKeyByHash_Found(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('volunteer@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var keyID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, token_hash, token_prefix, scope)
		VALUES ($1, 'test key', 'hash-abc', 'fhk_abc', 'embed') RETURNING id
	`, userID).Scan(&keyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	key, found, err := LookupAPIKeyByHash(ctx, pool, "hash-abc")
	if err != nil {
		t.Fatalf("LookupAPIKeyByHash: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if key.ID != keyID {
		t.Errorf("ID = %d, want %d", key.ID, keyID)
	}
	if key.Scope != "embed" {
		t.Errorf("Scope = %q, want embed", key.Scope)
	}
	if key.ExpiresAt != nil {
		t.Errorf("ExpiresAt = %v, want nil", key.ExpiresAt)
	}
}

func TestLookupAPIKeyByHash_NotFound(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	_, found, err := LookupAPIKeyByHash(ctx, pool, "no-such-hash")
	if err != nil {
		t.Fatalf("LookupAPIKeyByHash: %v", err)
	}
	if found {
		t.Fatal("expected key not to be found")
	}
}

func TestLookupAPIKeyByHash_ExpiresAtPopulated(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('expiring@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	expiry := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	if _, err := pool.Exec(ctx, `
		INSERT INTO api_keys (user_id, name, token_hash, token_prefix, scope, expires_at)
		VALUES ($1, 'expiring key', 'hash-exp', 'fhk_exp', 'embed', $2)
	`, userID, expiry); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	key, found, err := LookupAPIKeyByHash(ctx, pool, "hash-exp")
	if err != nil {
		t.Fatalf("LookupAPIKeyByHash: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if key.ExpiresAt == nil || !key.ExpiresAt.Equal(expiry) {
		t.Errorf("ExpiresAt = %v, want %v", key.ExpiresAt, expiry)
	}
}

func TestJobText_ReturnsTitleCompanyPrefixedStrippedDescription(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO jobs (public_slug, title, company, description)
		VALUES ('acme-backend-eng', 'Backend Engineer', 'Acme', '<p>Build great APIs.</p>') RETURNING id
	`).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	text, err := JobText(ctx, pool, jobID)
	if err != nil {
		t.Fatalf("JobText: %v", err)
	}
	want := "passage: Backend Engineer at Acme. Build great APIs."
	if text != want {
		t.Errorf("JobText = %q, want %q", text, want)
	}
}

func TestJobText_UnknownJob_ReturnsError(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	_, err := JobText(ctx, pool, 999999)
	if err == nil {
		t.Fatal("expected an error for an unknown job id")
	}
}
