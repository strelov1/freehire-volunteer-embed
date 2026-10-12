package reputation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// banThreshold is how many consecutive spot-check failures revoke a key's
// embed scope (design.md Decision 5).
const banThreshold = 3

// RecordSkipped records that a submission was trusted on track record
// without an actual TEI re-embedding (verify.OutcomeVerifiedWithoutCheck).
// It only advances total_evaluated — per reputation.ShouldCheck's contract,
// every submission routed through the check decision must advance this
// counter, whether or not it was actually checked — and leaves the
// pass/fail streaks untouched, since skipping a check teaches nothing new
// about the key's honesty.
func RecordSkipped(ctx context.Context, pool *pgxpool.Pool, keyID int64) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated)
		VALUES ($1, 1)
		ON CONFLICT (key_id) DO UPDATE SET
			total_evaluated = volunteer_reputation.total_evaluated + 1,
			updated_at = now()
	`, keyID)
	if err != nil {
		return fmt.Errorf("reputation: record skipped for key %d: %w", keyID, err)
	}
	return nil
}

// RecordPassed records a submission that was actually re-embedded and
// matched (verify.OutcomeVerifiedPassedCheck).
func RecordPassed(ctx context.Context, pool *pgxpool.Pool, keyID int64) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes, consecutive_fails)
		VALUES ($1, 1, 1, 0)
		ON CONFLICT (key_id) DO UPDATE SET
			total_evaluated = volunteer_reputation.total_evaluated + 1,
			consecutive_passes = volunteer_reputation.consecutive_passes + 1,
			consecutive_fails = 0,
			updated_at = now()
	`, keyID)
	if err != nil {
		return fmt.Errorf("reputation: record passed for key %d: %w", keyID, err)
	}
	return nil
}

// RecordFailed records a submission that was actually re-embedded and did
// not match (verify.OutcomeRejectedFailedCheck), and bans the key once it
// reaches banThreshold consecutive failures.
func RecordFailed(ctx context.Context, pool *pgxpool.Pool, keyID int64) error {
	var consecutiveFails int
	err := pool.QueryRow(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes, consecutive_fails)
		VALUES ($1, 1, 0, 1)
		ON CONFLICT (key_id) DO UPDATE SET
			total_evaluated = volunteer_reputation.total_evaluated + 1,
			consecutive_passes = 0,
			consecutive_fails = volunteer_reputation.consecutive_fails + 1,
			updated_at = now()
		RETURNING consecutive_fails
	`, keyID).Scan(&consecutiveFails)
	if err != nil {
		return fmt.Errorf("reputation: record failed for key %d: %w", keyID, err)
	}

	if consecutiveFails >= banThreshold {
		if _, err := pool.Exec(ctx, `
			UPDATE volunteer_reputation SET banned_at = now() WHERE key_id = $1 AND banned_at IS NULL
		`, keyID); err != nil {
			return fmt.Errorf("reputation: ban key %d: %w", keyID, err)
		}
	}
	return nil
}
