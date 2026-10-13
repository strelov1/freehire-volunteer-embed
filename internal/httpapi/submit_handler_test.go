package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

type submitResponse struct {
	Results []struct {
		JobID  int64  `json:"job_id"`
		Status string `json:"status"`
	} `json:"results"`
}

func fixtureVector768() []float64 {
	v := make([]float64, 768)
	for i := range v {
		v[i] = 0.02
	}
	return v
}

func TestSubmitHandler_LeasedJobValidVector_Accepted(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(5)

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('submit-handler-job', 'd') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3, $4)`,
		outboxID, jobID, keyID, time.Now().Add(5*time.Minute),
	); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"submissions": []map[string]any{
			{"outbox_id": outboxID, "job_id": jobID, "vector": fixtureVector768()},
		},
	})
	req := withKeyID(httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/submit", bytes.NewReader(payload)), keyID)
	rec := httptest.NewRecorder()

	SubmitHandler(pool, testConfig()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp submitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != "accepted" {
		t.Errorf("Results = %+v, want one accepted result", resp.Results)
	}
}

func TestSubmitHandler_UnleasedJob_ReturnsNotLeasedStatus(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('submit-handler-unleased', 'd') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"submissions": []map[string]any{
			{"job_id": jobID, "vector": fixtureVector768()},
		},
	})
	req := withKeyID(httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/submit", bytes.NewReader(payload)), 5)
	rec := httptest.NewRecorder()

	SubmitHandler(pool, testConfig()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp submitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Status != "not_leased" {
		t.Errorf("Results = %+v, want one not_leased result", resp.Results)
	}
}

func TestSubmitHandler_MalformedJSON_Returns400(t *testing.T) {
	pool := testdb.Setup(t)

	req := withKeyID(httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/submit", bytes.NewReader([]byte("not json"))), 5)
	rec := httptest.NewRecorder()

	SubmitHandler(pool, testConfig()).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSubmitHandler_MissingKeyIDInContext_Returns500(t *testing.T) {
	pool := testdb.Setup(t)

	payload, _ := json.Marshal(map[string]any{"submissions": []map[string]any{}})
	req := httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/submit", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	SubmitHandler(pool, testConfig()).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (handler used without auth middleware)", rec.Code)
	}
}
