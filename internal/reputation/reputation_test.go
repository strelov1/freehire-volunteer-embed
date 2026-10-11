package reputation

import (
	"context"
	"testing"
	"time"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestIsBanned_NoRow_ReturnsFalse(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	banned, err := IsBanned(ctx, pool, 12345)
	if err != nil {
		t.Fatalf("IsBanned: %v", err)
	}
	if banned {
		t.Error("expected a key with no reputation row to not be banned")
	}
}

func TestIsBanned_BannedAtSet_ReturnsTrue(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, banned_at) VALUES (1, $1)
	`, time.Now()); err != nil {
		t.Fatalf("seed banned reputation row: %v", err)
	}

	banned, err := IsBanned(ctx, pool, 1)
	if err != nil {
		t.Fatalf("IsBanned: %v", err)
	}
	if !banned {
		t.Error("expected key 1 to be banned")
	}
}

func TestIsBanned_BannedAtNull_ReturnsFalse(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id) VALUES (2)
	`); err != nil {
		t.Fatalf("seed reputation row: %v", err)
	}

	banned, err := IsBanned(ctx, pool, 2)
	if err != nil {
		t.Fatalf("IsBanned: %v", err)
	}
	if banned {
		t.Error("expected key 2 (no ban) to not be banned")
	}
}
