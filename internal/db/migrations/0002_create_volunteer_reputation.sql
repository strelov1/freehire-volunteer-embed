CREATE TABLE IF NOT EXISTS volunteer_reputation (
    key_id bigint PRIMARY KEY,
    consecutive_passes integer NOT NULL DEFAULT 0,
    consecutive_fails integer NOT NULL DEFAULT 0,
    total_checked integer NOT NULL DEFAULT 0,
    banned_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
