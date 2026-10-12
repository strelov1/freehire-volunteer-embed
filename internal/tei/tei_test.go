package tei

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
