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

// GetCounts returns a key's current total_evaluated and consecutive_passes,
// both zero for a key with no reputation row yet (never submitted).
func GetCounts(ctx context.Context, pool *pgxpool.Pool, keyID int64) (totalEvaluated, consecutivePasses int, err error) {
	err = pool.QueryRow(ctx,
		`SELECT total_evaluated, consecutive_passes FROM volunteer_reputation WHERE key_id = $1`,
		keyID,
	).Scan(&totalEvaluated, &consecutivePasses)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("reputation: get counts for key %d: %w", keyID, err)
	}
	return totalEvaluated, consecutivePasses, nil
}
