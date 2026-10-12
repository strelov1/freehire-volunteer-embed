package tei

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEmbed_SuccessfulResponse_ReturnsVector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Inputs []string `json:"inputs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(body.Inputs) != 1 || body.Inputs[0] != "hello job" {
			t.Fatalf("unexpected request inputs: %+v", body.Inputs)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([][]float64{{0.1, 0.2, 0.3}})
	}))
	defer server.Close()

	vec, err := Embed(context.Background(), server.URL, "hello job")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	want := []float64{0.1, 0.2, 0.3}
	if len(vec) != len(want) {
		t.Fatalf("len(vec) = %d, want %d", len(vec), len(want))
	}
	for i := range want {
		if vec[i] != want[i] {
			t.Errorf("vec[%d] = %v, want %v", i, vec[i], want[i])
		}
	}
}

func TestEmbed_NonOKStatus_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()

	_, err := Embed(context.Background(), server.URL, "text")
	if err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestEmbed_MalformedJSON_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	_, err := Embed(context.Background(), server.URL, "text")
	if err == nil {
		t.Fatal("expected an error for a malformed JSON response")
	}
}

func TestEmbed_EmptyVectorArray_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{})
	}))
	defer server.Close()

	_, err := Embed(context.Background(), server.URL, "text")
	if err == nil {
		t.Fatal("expected an error for an empty vector array")
	}
}

func TestEmbed_UnreachableServer_ReturnsError(t *testing.T) {
	_, err := Embed(context.Background(), "http://127.0.0.1:1", "text")
	if err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
}

// TestEmbed_SlowServer_FailsFastRatherThanHanging guards against a TEI that
// is merely slow (not down): Embed must give up on its own bounded timeout,
// not rely solely on whatever deadline the caller's context happens to
// carry — spot-check verification runs synchronously inside a volunteer's
// HTTP request (internal/submit), so an unbounded wait here would hang that
// request.
func TestEmbed_SlowServer_FailsFastRatherThanHanging(t *testing.T) {
	old := requestTimeout
	requestTimeout = 50 * time.Millisecond
	defer func() { requestTimeout = old }()

	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock // never responds before Embed's own short timeout fires
	}))

	start := time.Now()
	_, err := Embed(context.Background(), server.URL, "text")
	elapsed := time.Since(start)

	close(unblock) // let the still-blocked handler goroutine finish...
	server.Close() // ...before Close() waits on it, so this doesn't deadlock

	if err == nil {
		t.Fatal("expected an error from a server that never responds")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Embed took %v to fail, want it bounded by its own short requestTimeout, not the test's safety margin", elapsed)
	}
}
