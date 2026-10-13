package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/client"
)

func TestLease_SendsAuthAndBatchSize_ReturnsJobs(t *testing.T) {
	var gotAuth, gotMethod, gotPath string
	var gotBody map[string]int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jobs": []map[string]any{
				{"id": 42, "text": "passage: Senior Engineer at Acme."},
			},
		})
	}))
	defer server.Close()

	jobs, err := client.Lease(context.Background(), http.DefaultClient, server.URL, "fhk_test", 10)
	if err != nil {
		t.Fatalf("Lease: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/v1/embed-tasks/lease" {
		t.Errorf("expected path /v1/embed-tasks/lease, got %s", gotPath)
	}
	if gotAuth != "Bearer fhk_test" {
		t.Errorf("expected Authorization 'Bearer fhk_test', got %q", gotAuth)
	}
	if gotBody["batch_size"] != 10 {
		t.Errorf("expected batch_size 10 in request body, got %v", gotBody)
	}

	if len(jobs) != 1 || jobs[0].ID != 42 || jobs[0].Text != "passage: Senior Engineer at Acme." {
		t.Errorf("unexpected jobs: %+v", jobs)
	}
}

func TestLease_ServerError_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := client.Lease(context.Background(), http.DefaultClient, server.URL, "fhk_bad", 10)
	if err == nil {
		t.Fatal("expected an error for a 401 response, got nil")
	}
}

func TestSubmit_SendsSubmissions_ReturnsResults(t *testing.T) {
	var gotBody map[string][]map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"job_id": 42, "status": "accepted"},
			},
		})
	}))
	defer server.Close()

	results, err := client.Submit(context.Background(), http.DefaultClient, server.URL, "fhk_test", []client.Submission{
		{JobID: 42, Vector: []float64{0.1, 0.2}},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	submissions := gotBody["submissions"]
	if len(submissions) != 1 || int64(submissions[0]["job_id"].(float64)) != 42 {
		t.Errorf("unexpected request body: %+v", gotBody)
	}

	if len(results) != 1 || results[0].JobID != 42 || results[0].Status != "accepted" {
		t.Errorf("unexpected results: %+v", results)
	}
}
