package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/submit"
)

type submitRequestItem struct {
	// OutboxID must be the same value the matching lease response gave for
	// this job — a job can have more than one outstanding semantic_outbox
	// row at once, and job_id alone cannot say which one this submission
	// resolves.
	OutboxID int64     `json:"outbox_id"`
	JobID    int64     `json:"job_id"`
	Vector   []float64 `json:"vector"`
}

type submitRequestBody struct {
	Submissions []submitRequestItem `json:"submissions"`
}

type submitResultItem struct {
	JobID  int64  `json:"job_id"`
	Status string `json:"status"`
}

type submitResponseBody struct {
	Results []submitResultItem `json:"results"`
}

func statusString(s submit.Status) string {
	switch s {
	case submit.StatusAccepted:
		return "accepted"
	case submit.StatusNotLeased:
		return "not_leased"
	case submit.StatusInvalidVector:
		return "invalid_vector"
	case submit.StatusError:
		return "error"
	default:
		return "unknown"
	}
}

// SubmitHandler serves POST /v1/embed-tasks/submit. It must run behind
// RequireEmbedScope, which supplies the caller's key id via context.
func SubmitHandler(pool *pgxpool.Pool, cfg config.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyID, ok := KeyIDFromContext(r.Context())
		if !ok {
			http.Error(w, "internal error: missing authenticated key", http.StatusInternalServerError)
			return
		}

		var body submitRequestBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		items := make([]submit.Item, len(body.Submissions))
		for i, s := range body.Submissions {
			items[i] = submit.Item{OutboxID: s.OutboxID, JobID: s.JobID, Vector: s.Vector}
		}

		results := submit.Submit(r.Context(), pool, keyID, items, cfg.TrustedTEIURL, cfg)

		resp := submitResponseBody{Results: make([]submitResultItem, len(results))}
		for i, r := range results {
			resp.Results[i] = submitResultItem{JobID: r.JobID, Status: statusString(r.Status)}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}
