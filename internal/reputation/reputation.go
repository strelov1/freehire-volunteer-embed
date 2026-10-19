// Package reputation tracks each volunteer API key's trust record in this
// service's own volunteer_reputation table — pass/fail streaks and bans.
package reputation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IsBanned reports whether keyID has been banned for embedding abuse. A key
// with no reputation row yet (never submitted anything) is not banned.
func IsBanned(ctx context.Context, pool *pgxpool.Pool, keyID int64) (bool, error) {
	var bannedAt *time.Time
	err := pool.QueryRow(ctx,
		`SELECT banned_at FROM volunteer_reputation WHERE key_id = $1`,
		keyID,
	).Scan(&bannedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reputation: is banned for key %d: %w", keyID, err)
	}
	return bannedAt != nil, nil
}

// TakeTurns claims a key's next n check-decision turns: it advances
// total_evaluated by n and returns the first turn — the value it held
// BEFORE, zero for a key that has never submitted — along with
// consecutive_passes, in one atomic statement. The caller hands turns
// first..first+n-1 out one per submission, and ShouldCheck decides on
// each. Claiming turns is the only thing that advances total_evaluated.
//
// Reading the counters and advancing them later, once the outcome was
// known, made two failures (2026-10-18): concurrent groups in one batch all
// read the same value and so all reached the same decision — once in every
// divisor batches the whole batch was re-embedded through the trusted TEI —
// and a check the TEI could not finish advanced nothing, so the next
// submission landed on the same "check" turn again. One statement per
// batch rather than per job also keeps the key's row from becoming a lock
// every job of the batch queues behind.
func TakeTurns(ctx context.Context, pool *pgxpool.Pool, keyID int64, n int) (first, consecutivePasses int, err error) {
	err = pool.QueryRow(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated)
		VALUES ($1, $2)
		ON CONFLICT (key_id) DO UPDATE SET
			total_evaluated = volunteer_reputation.total_evaluated + $2,
			updated_at = now()
		RETURNING total_evaluated - $2, consecutive_passes
	`, keyID, n).Scan(&first, &consecutivePasses)
	if err != nil {
		return 0, 0, fmt.Errorf("reputation: take %d turns for key %d: %w", n, keyID, err)
	}
	return first, consecutivePasses, nil
}
