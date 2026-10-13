package httpapi

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/lease"
)

type leaseRequest struct {
	BatchSize int `json:"batch_size"`
}

type leaseJob struct {
	// OutboxID identifies the specific semantic_outbox row this claim is
	// for — a job can have more than one outstanding row at once (e.g. mid
	// migration between two embedding model versions), so a volunteer must
	// round-trip this value in its matching submission; ID (job_id) alone
	// cannot disambiguate which row a submission resolves.
	OutboxID int64  `json:"outbox_id"`
	ID       int64  `json:"id"`
	Text     string `json:"text"`
}

type leaseResponseBody struct {
	Jobs []leaseJob `json:"jobs"`
}

// LeaseHandler serves POST /v1/embed-tasks/lease. It must run behind
// RequireEmbedScope, which supplies the caller's key id via context.
func LeaseHandler(pool *pgxpool.Pool, cfg config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyID, ok := KeyIDFromContext(r.Context())
		if !ok {
			http.Error(w, "internal error: missing authenticated key", http.StatusInternalServerError)
			return
		}

		var req leaseRequest
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req) // empty/absent body is valid: use defaults
		}
		batchSize := req.BatchSize
		if batchSize <= 0 || batchSize > cfg.MaxLeaseBatchSize {
			batchSize = cfg.MaxLeaseBatchSize
		}

		claimed, err := lease.Claim(r.Context(), pool, keyID, batchSize, cfg.LeaseDuration)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		jobs := make([]leaseJob, len(claimed))
		for i, c := range claimed {
			jobs[i] = leaseJob{OutboxID: c.OutboxID, ID: c.JobID, Text: c.Text}
		}
		log.Printf("AUDIT lease key=%d count=%d", keyID, len(jobs))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(leaseResponseBody{Jobs: jobs})
	})
}
