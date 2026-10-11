// Package submit accepts volunteer-computed embedding vectors and stages
// them in volunteer_submissions for later verification. It never writes to
// freehire's live job_semantic_chunks table — that happens only after
// verification (see the promotion package, task group 8).
package submit

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const vectorDimension = 768

type Status int

const (
	StatusAccepted Status = iota
	StatusNotLeased
	StatusInvalidVector
)

type Item struct {
	JobID  int64
	Vector []float64
}

type Result struct {
	JobID  int64
	Status Status
}

// Submit processes each item independently: a rejection in one item does
// not affect the others, and never touches reputation (reputation only
// changes as a result of spot-check verification, not at submission time).
func Submit(ctx context.Context, pool *pgxpool.Pool, keyID int64, items []Item) ([]Result, error) {
	results := make([]Result, len(items))
	for i, item := range items {
		status, err := submitOne(ctx, pool, keyID, item)
		if err != nil {
			return nil, fmt.Errorf("submit: job %d: %w", item.JobID, err)
		}
		results[i] = Result{JobID: item.JobID, Status: status}
	}
	return results, nil
}

func submitOne(ctx context.Context, pool *pgxpool.Pool, keyID int64, item Item) (Status, error) {
	if !validVector(item.Vector) {
		return StatusInvalidVector, nil
	}

	var leased bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM volunteer_leases
			WHERE job_id = $1 AND leased_by_key_id = $2 AND expires_at > now()
		)
	`, item.JobID, keyID).Scan(&leased)
	if err != nil {
		return 0, fmt.Errorf("checking lease ownership: %w", err)
	}
	if !leased {
		return StatusNotLeased, nil
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_submissions (job_id, submitted_by_key_id, embedding, status)
		VALUES ($1, $2, $3, 'pending')
	`, item.JobID, keyID, formatVector(item.Vector)); err != nil {
		return 0, fmt.Errorf("storing submission: %w", err)
	}

	return StatusAccepted, nil
}

func validVector(v []float64) bool {
	if len(v) != vectorDimension {
		return false
	}
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}

// formatVector renders a []float64 as pgvector's text input format, e.g.
// "[0.1,0.2,0.3]".
func formatVector(v []float64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(x, 'g', -1, 64))
	}
	b.WriteByte(']')
	return b.String()
}
