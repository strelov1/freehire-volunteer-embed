package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/auth"
	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestRequireEmbedScope_ValidToken_CallsNextWithKeyID(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := t.Context()

	var userID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('mw@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	token := "fhk_middlewarevalid"
	var keyID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, token_hash, token_prefix, scope)
		VALUES ($1, 'k', $2, 'fhk_midd', 'embed') RETURNING id
	`, userID, auth.HashToken(token)).Scan(&keyID); err != nil {
		t.Fatalf("seed api key: %v", err)
	}

	var sawKeyID int64
	var nextCalled bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		sawKeyID, _ = KeyIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	RequireEmbedScope(pool)(next).ServeHTTP(rec, req)

	if !nextCalled {
		t.Fatal("expected next handler to be called for a valid token")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if sawKeyID != keyID {
		t.Errorf("KeyIDFromContext = %d, want %d", sawKeyID, keyID)
	}
}

func TestRequireEmbedScope_InvalidToken_Returns401AndSkipsNext(t *testing.T) {
	pool := testdb.Setup(t)

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil)
	req.Header.Set("Authorization", "Bearer fhk_nope")
	rec := httptest.NewRecorder()

	RequireEmbedScope(pool)(next).ServeHTTP(rec, req)

	if nextCalled {
		t.Fatal("expected next handler NOT to be called for an invalid token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireEmbedScope_MissingHeader_Returns401(t *testing.T) {
	pool := testdb.Setup(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil)
	rec := httptest.NewRecorder()

	RequireEmbedScope(pool)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run without an Authorization header")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
