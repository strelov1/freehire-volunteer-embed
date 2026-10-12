package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimitBodySize_BodyUnderLimit_ReadsFully(t *testing.T) {
	body := strings.Repeat("a", 100)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()

	var readErr error
	var gotLen int
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		readErr = err
		gotLen = len(data)
	})

	LimitBodySize(1000)(next).ServeHTTP(rec, req)

	if readErr != nil {
		t.Fatalf("unexpected read error: %v", readErr)
	}
	if gotLen != 100 {
		t.Errorf("read %d bytes, want 100", gotLen)
	}
}

func TestLimitBodySize_BodyOverLimit_ReadFails(t *testing.T) {
	body := strings.Repeat("a", 2000)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()

	var readErr error
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	})

	LimitBodySize(1000)(next).ServeHTTP(rec, req)

	if readErr == nil {
		t.Error("expected reading an over-limit body to error, got nil")
	}
}
