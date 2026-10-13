package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLease_SlowServer_FailsFastRatherThanHanging guards against a server
// that is merely slow (not down): Lease must give up on its own bounded
// timeout, not rely solely on the caller's context — a volunteer's own
// client has no caller imposing a deadline, so an unbounded wait here hangs
// the whole poll loop until an OS signal arrives.
func TestLease_SlowServer_FailsFastRatherThanHanging(t *testing.T) {
	old := requestTimeout
	requestTimeout = 50 * time.Millisecond
	defer func() { requestTimeout = old }()

	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock // never responds before Lease's own short timeout fires
	}))

	start := time.Now()
	_, err := Lease(context.Background(), http.DefaultClient, server.URL, "fhk_test", 10)
	elapsed := time.Since(start)

	close(unblock)
	server.Close()

	if err == nil {
		t.Fatal("expected an error from a server that never responds")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Lease took %v to fail, want it bounded by its own short requestTimeout, not the test's safety margin", elapsed)
	}
}
