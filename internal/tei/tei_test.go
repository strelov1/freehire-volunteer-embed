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

	vec, err := Embed(context.Background(), server.URL, "", "hello job")
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

	_, err := Embed(context.Background(), server.URL, "", "text")
	if err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestEmbed_MalformedJSON_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	_, err := Embed(context.Background(), server.URL, "", "text")
	if err == nil {
		t.Fatal("expected an error for a malformed JSON response")
	}
}

func TestEmbed_EmptyVectorArray_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{})
	}))
	defer server.Close()

	_, err := Embed(context.Background(), server.URL, "", "text")
	if err == nil {
		t.Fatal("expected an error for an empty vector array")
	}
}

func TestEmbed_WithAPIKey_SendsBearerHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([][]float64{{0.1}})
	}))
	defer server.Close()

	if _, err := Embed(context.Background(), server.URL, "secret-token", "text"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer secret-token")
	}
}

func TestEmbed_WithoutAPIKey_SendsNoAuthorizationHeader(t *testing.T) {
	sawHeader := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHeader = r.Header.Get("Authorization") != ""
		_ = json.NewEncoder(w).Encode([][]float64{{0.1}})
	}))
	defer server.Close()

	if _, err := Embed(context.Background(), server.URL, "", "text"); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if sawHeader {
		t.Error("expected no Authorization header when apiKey is empty (freehire's real TEI needs no auth)")
	}
}

// TestEmbedBatch_MultipleTexts_ReturnsVectorsInOrder is the whole point of
// EmbedBatch: one request carrying several texts, so a TEI-compatible
// server can batch the forward pass instead of the caller paying per-item
// request/connection overhead for each one separately (measured ~20x
// faster for 5 texts against a local Mac TEI server during this project).
func TestEmbedBatch_MultipleTexts_ReturnsVectorsInOrder(t *testing.T) {
	var gotInputs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Inputs []string `json:"inputs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotInputs = body.Inputs
		_ = json.NewEncoder(w).Encode([][]float64{{0.1, 0.2}, {0.3, 0.4}, {0.5, 0.6}})
	}))
	defer server.Close()

	vecs, err := EmbedBatch(context.Background(), server.URL, "", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(gotInputs) != 3 || gotInputs[0] != "a" || gotInputs[1] != "b" || gotInputs[2] != "c" {
		t.Errorf("request inputs = %+v, want [a b c] in order", gotInputs)
	}
	want := [][]float64{{0.1, 0.2}, {0.3, 0.4}, {0.5, 0.6}}
	if len(vecs) != len(want) {
		t.Fatalf("len(vecs) = %d, want %d", len(vecs), len(want))
	}
	for i := range want {
		if vecs[i][0] != want[i][0] || vecs[i][1] != want[i][1] {
			t.Errorf("vecs[%d] = %v, want %v", i, vecs[i], want[i])
		}
	}
}

// TestEmbedBatch_ResponseCountMismatch_ReturnsError guards against silently
// misattributing vectors to the wrong text if a TEI-compatible server ever
// returns a different number of vectors than texts sent — a caller zipping
// these back together by position must not do so blind.
func TestEmbedBatch_ResponseCountMismatch_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{{0.1, 0.2}}) // only 1, caller sent 2
	}))
	defer server.Close()

	_, err := EmbedBatch(context.Background(), server.URL, "", []string{"a", "b"})
	if err == nil {
		t.Fatal("expected an error when the response vector count doesn't match the request text count")
	}
}

func TestEmbedBatch_EmptyInput_ReturnsEmptyNotError(t *testing.T) {
	vecs, err := EmbedBatch(context.Background(), "http://127.0.0.1:1", "", nil)
	if err != nil {
		t.Fatalf("EmbedBatch with no texts should not even need a server: %v", err)
	}
	if len(vecs) != 0 {
		t.Errorf("len(vecs) = %d, want 0", len(vecs))
	}
}

// TestEmbedBatch_TimeoutScalesWithBatchSize guards a real regression: a
// single fixed timeout covering an entire batch (instead of per text) is
// fine for a one-item call but becomes a permanent failure mode for a
// volunteer's CPU-only machine running a large batch — every call would
// time out forever, silently contributing nothing. The timeout budget
// must grow with how much work was actually requested.
func TestEmbedBatch_TimeoutScalesWithBatchSize(t *testing.T) {
	old := requestTimeout
	requestTimeout = 100 * time.Millisecond // per-text budget
	defer func() { requestTimeout = old }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond) // longer than 1 text's budget, shorter than 5 texts' budget
		_ = json.NewEncoder(w).Encode([][]float64{{0.1}, {0.2}, {0.3}, {0.4}, {0.5}})
	}))
	defer server.Close()

	_, err := EmbedBatch(context.Background(), server.URL, "", []string{"a", "b", "c", "d", "e"})
	if err != nil {
		t.Fatalf("EmbedBatch with 5 texts should get roughly 5x the per-text timeout budget, got: %v", err)
	}
}

func TestEmbed_UnreachableServer_ReturnsError(t *testing.T) {
	_, err := Embed(context.Background(), "http://127.0.0.1:1", "", "text")
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
	_, err := Embed(context.Background(), server.URL, "", "text")
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
