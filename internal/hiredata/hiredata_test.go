package hiredata

import (
	"context"
	"reflect"
	"strings"
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

func TestJobChunks_ReturnsTitleCompanyPrefixedStrippedDescription(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO jobs (public_slug, title, company, description)
		VALUES ('acme-backend-eng', 'Backend Engineer', 'Acme', '<p>Build great APIs.</p>') RETURNING id
	`).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	chunks, err := JobChunks(ctx, pool, jobID)
	if err != nil {
		t.Fatalf("JobChunks: %v", err)
	}
	want := []string{"passage: Backend Engineer at Acme. Build great APIs."}
	if !reflect.DeepEqual(chunks, want) {
		t.Errorf("JobChunks = %q, want %q", chunks, want)
	}
}

func TestJobChunks_UnknownJob_ReturnsError(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	_, err := JobChunks(ctx, pool, 999999)
	if err == nil {
		t.Fatal("expected an error for an unknown job id")
	}
}

// TestJobChunks_LongDescription_ReturnsMultipleChunks proves JobChunks
// actually chunks a long description instead of returning the whole thing
// as one passage — the fix for a real gap: a single-passage-per-job
// service permanently lost every chunk past the first for long
// descriptions, since nothing else ever revisits an already-stamped job.
func TestJobChunks_LongDescription_ReturnsMultipleChunks(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var paras []string
	for i := 0; i < 40; i++ {
		paras = append(paras, strings.Repeat("word ", 20)+"end of paragraph.")
	}
	description := strings.Join(paras, "\n")

	var jobID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO jobs (public_slug, title, company, description)
		VALUES ('acme-long-job', 'Engineer', 'Acme', $1) RETURNING id
	`, description).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	chunks, err := JobChunks(ctx, pool, jobID)
	if err != nil {
		t.Fatalf("JobChunks: %v", err)
	}
	if len(chunks) <= 1 {
		t.Fatalf("len(chunks) = %d, want > 1 for a description well past the chunk budget", len(chunks))
	}
}
