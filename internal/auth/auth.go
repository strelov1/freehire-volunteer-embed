// Package auth validates a volunteer's bearer token against freehire's
// api_keys table and this service's own reputation bans.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/hiredata"
	"github.com/strelov1/freehire-volunteer-embed/internal/reputation"
)

// ErrUnauthorized is returned for any authentication failure. Callers map it
// to HTTP 401; it deliberately does not distinguish "not found" from "wrong
// scope" from "expired" from "banned" to a caller who doesn't hold the key.
var ErrUnauthorized = errors.New("auth: unauthorized")

const requiredScope = "embed"

// HashToken returns the hex-encoded SHA-256 of a bearer token, matching
// hire's own HashAPIKey scheme exactly so this service can look up
// freehire-issued keys by their stored hash.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Authenticate validates an "Authorization: Bearer fhk_..." header value and
// returns the resolved api_keys.id on success.
func Authenticate(ctx context.Context, pool *pgxpool.Pool, authorizationHeader string) (int64, error) {
	const scheme = "Bearer "
	if len(authorizationHeader) <= len(scheme) || !strings.EqualFold(authorizationHeader[:len(scheme)], scheme) {
		return 0, ErrUnauthorized
	}
	token := authorizationHeader[len(scheme):]

	key, found, err := hiredata.LookupAPIKeyByHash(ctx, pool, HashToken(token))
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, ErrUnauthorized
	}
	if key.Scope != requiredScope {
		return 0, ErrUnauthorized
	}
	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		return 0, ErrUnauthorized
	}

	banned, err := reputation.IsBanned(ctx, pool, key.ID)
	if err != nil {
		return 0, err
	}
	if banned {
		return 0, ErrUnauthorized
	}

	return key.ID, nil
}
