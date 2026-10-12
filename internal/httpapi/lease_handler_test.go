package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func withKeyID(r *http.Request, keyID int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), keyIDContextKey, keyID))
}

// unreachableTEIURL lets handler tests that don't care about spot-check
// outcomes (lease tests never call it; submit tests let verification defer)
// run without a real TEI server.
const unreachableTEIURL = "http://127.0.0.1:1"

func testConfig() config.Config {
	return config.Config{
		LeaseDuration:      5 * time.Minute,
		MaxLeaseBatchSize:  50,
		TrustedTEIURL:      unreachableTEIURL,
		FullCheckThreshold: 20,
		CheckDivisorStage1: 10,
		CheckDivisorStage2: 30,
	}
}

type leaseResponse struct {
	Jobs []struct {
		ID   int64  `json:"id"`
		Text string `json:"text"`
	} `json:"jobs"`
}

func TestLeaseHandler_ReturnsClaimedJobs(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO jobs (public_slug, description) VALUES ('lease-handler-job', 'desc text') RETURNING id
	`).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5')`, jobID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	req := withKeyID(httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil), 7)
	rec := httptest.NewRecorder()

	LeaseHandler(pool, testConfig()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp leaseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Jobs) != 1 {
		t.Fatalf("len(Jobs) = %d, want 1", len(resp.Jobs))
	}
	if resp.Jobs[0].ID != jobID || resp.Jobs[0].Text != "desc text" {
		t.Errorf("Jobs[0] = %+v, want ID=%d Text=%q", resp.Jobs[0], jobID, "desc text")
	}
}

func TestLeaseHandler_EmptyQueue_Returns200WithEmptyJobs(t *testing.T) {
	pool := testdb.Setup(t)

	req := withKeyID(httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil), 7)
	rec := httptest.NewRecorder()

	LeaseHandler(pool, testConfig()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp leaseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Jobs) != 0 {
		t.Errorf("len(Jobs) = %d, want 0", len(resp.Jobs))
	}
}

func TestLeaseHandler_RequestedBatchSizeClampedToConfiguredMax(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		var jobID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO jobs (public_slug, description) VALUES ($1, 'd') RETURNING id
		`, "clamp-job-"+string(rune('a'+i))).Scan(&jobID); err != nil {
			t.Fatalf("seed job: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5')`, jobID); err != nil {
			t.Fatalf("seed outbox row: %v", err)
		}
	}

	cfg := testConfig()
	cfg.MaxLeaseBatchSize = 2

	body := bytes.NewBufferString(`{"batch_size": 100}`)
	req := withKeyID(httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", body), 7)
	rec := httptest.NewRecorder()

	LeaseHandler(pool, cfg).ServeHTTP(rec, req)

	var resp leaseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Jobs) != 2 {
		t.Errorf("len(Jobs) = %d, want 2 (clamped to configured max)", len(resp.Jobs))
	}
}

func TestLeaseHandler_MissingKeyIDInContext_Returns500(t *testing.T) {
	pool := testdb.Setup(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/embed-tasks/lease", nil)
	rec := httptest.NewRecorder()

	LeaseHandler(pool, testConfig()).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (handler used without auth middleware)", rec.Code)
	}
}
