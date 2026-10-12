package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/ratelimit"
)

func TestRateLimitByKey_WithinBudget_CallsNext(t *testing.T) {
	limiter := ratelimit.New(1, 3)
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := withKeyID(httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil), 1)
	rec := httptest.NewRecorder()

	RateLimitByKey(limiter)(next).ServeHTTP(rec, req)

	if !nextCalled {
		t.Fatal("expected next handler to be called within budget")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRateLimitByKey_BudgetExhausted_Returns429AndSkipsNext(t *testing.T) {
	limiter := ratelimit.New(1, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := RateLimitByKey(limiter)(next)

	req := withKeyID(httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil), 1)
	mw.ServeHTTP(httptest.NewRecorder(), req) // consume the burst of 1

	nextCalled := false
	req2 := withKeyID(httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil), 1)
	rec2 := httptest.NewRecorder()
	RateLimitByKey(limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})).ServeHTTP(rec2, req2)

	if nextCalled {
		t.Error("expected next handler NOT to be called once the budget is exhausted")
	}
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec2.Code)
	}
}

func TestRateLimitByKey_MissingKeyIDInContext_Returns500(t *testing.T) {
	limiter := ratelimit.New(1, 3)
	req := httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil)
	rec := httptest.NewRecorder()

	RateLimitByKey(limiter)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run without a key id in context")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
