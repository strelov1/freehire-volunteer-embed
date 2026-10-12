package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestHealthHandler_DatabaseReachable_Returns200(t *testing.T) {
	pool := testdb.Setup(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	HealthHandler(pool).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestHealthHandler_DatabaseUnreachable_Returns503(t *testing.T) {
	pool := testdb.Setup(t)
	pool.Close() // simulate the database being unreachable

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	HealthHandler(pool).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
