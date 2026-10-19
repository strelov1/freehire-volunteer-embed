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

// TakeTurn claims a key's next check-decision turn: it advances
// total_evaluated by one and returns the value it held BEFORE (zero for a
// key that has never submitted) along with consecutive_passes, in one
// atomic statement. The turn is what ShouldCheck decides on, and claiming
// it is the only thing that advances total_evaluated.
//
// Reading the counters and advancing them later, once the outcome was
// known, made two failures (2026-10-18): concurrent groups in one batch all
// read the same value and so all reached the same decision — once in every
// divisor batches the whole batch was re-embedded through the trusted TEI —
// and a check the TEI could not finish advanced nothing, so the next
// submission landed on the same "check" turn again.
func TakeTurn(ctx context.Context, pool *pgxpool.Pool, keyID int64) (turn, consecutivePasses int, err error) {
	err = pool.QueryRow(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated)
		VALUES ($1, 1)
		ON CONFLICT (key_id) DO UPDATE SET
			total_evaluated = volunteer_reputation.total_evaluated + 1,
			updated_at = now()
		RETURNING total_evaluated - 1, consecutive_passes
	`, keyID).Scan(&turn, &consecutivePasses)
	if err != nil {
		return 0, 0, fmt.Errorf("reputation: take turn for key %d: %w", keyID, err)
	}
	return turn, consecutivePasses, nil
}
