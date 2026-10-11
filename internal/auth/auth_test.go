package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestHashToken_MatchesKnownSHA256Hex(t *testing.T) {
	// printf 'fhk_test' | shasum -a 256
	const want = "4f537508dcd3b68dbc096c2db88b1ad4c85fa57915385ff8c7c777cf4321c7e6"
	got := HashToken("fhk_test")
	if got != want {
		t.Errorf("HashToken(%q) = %q, want %q (must match hire's hex(sha256(token)) scheme)", "fhk_test", got, want)
	}
}

func TestAuthenticate_ValidEmbedScopedKey_ReturnsKeyID(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('v@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := "fhk_validtoken"
	var keyID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, token_hash, token_prefix, scope)
		VALUES ($1, 'k', $2, 'fhk_valid', 'embed') RETURNING id
	`, userID, HashToken(token)).Scan(&keyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	gotID, err := Authenticate(ctx, pool, "Bearer "+token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if gotID != keyID {
		t.Errorf("Authenticate returned key %d, want %d", gotID, keyID)
	}
}

func TestAuthenticate_UnknownToken_ReturnsUnauthorized(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	_, err := Authenticate(ctx, pool, "Bearer fhk_doesnotexist")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticate_WrongScope_ReturnsUnauthorized(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('full@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := "fhk_fullscope"
	if _, err := pool.Exec(ctx, `
		INSERT INTO api_keys (user_id, name, token_hash, token_prefix, scope)
		VALUES ($1, 'k', $2, 'fhk_full', 'full')
	`, userID, HashToken(token)); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	_, err := Authenticate(ctx, pool, "Bearer "+token)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticate_ExpiredKey_ReturnsUnauthorized(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('exp@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := "fhk_expiredtoken"
	past := time.Now().Add(-1 * time.Hour)
	if _, err := pool.Exec(ctx, `
		INSERT INTO api_keys (user_id, name, token_hash, token_prefix, scope, expires_at)
		VALUES ($1, 'k', $2, 'fhk_expi', 'embed', $3)
	`, userID, HashToken(token), past); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	_, err := Authenticate(ctx, pool, "Bearer "+token)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticate_BannedKey_ReturnsUnauthorized(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('ban@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := "fhk_bannedtoken"
	var keyID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, token_hash, token_prefix, scope)
		VALUES ($1, 'k', $2, 'fhk_bann', 'embed') RETURNING id
	`, userID, HashToken(token)).Scan(&keyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, banned_at) VALUES ($1, now())
	`, keyID); err != nil {
		t.Fatalf("seed ban: %v", err)
	}

	_, err := Authenticate(ctx, pool, "Bearer "+token)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticate_MissingBearerPrefix_ReturnsUnauthorized(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	_, err := Authenticate(ctx, pool, "fhk_notbearerformat")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestAuthenticate_LowercaseBearerScheme_StillAuthenticates(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('case@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := "fhk_lowercasebearer"
	var keyID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, token_hash, token_prefix, scope)
		VALUES ($1, 'k', $2, 'fhk_lowe', 'embed') RETURNING id
	`, userID, HashToken(token)).Scan(&keyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	gotID, err := Authenticate(ctx, pool, "bearer "+token)
	if err != nil {
		t.Fatalf("Authenticate with lowercase scheme: %v", err)
	}
	if gotID != keyID {
		t.Errorf("Authenticate returned key %d, want %d", gotID, keyID)
	}
}
