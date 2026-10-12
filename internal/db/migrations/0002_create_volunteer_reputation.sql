CREATE TABLE IF NOT EXISTS volunteer_reputation (
    key_id bigint PRIMARY KEY,
    consecutive_passes integer NOT NULL DEFAULT 0,
    consecutive_fails integer NOT NULL DEFAULT 0,
    -- Incremented once per submission routed through the spot-check
    -- decision (reputation.ShouldCheck), whether or not that submission was
    -- actually re-embedded and compared — NOT only on actual checks. See
    -- the contract documented on ShouldCheck's totalEvaluated parameter.
    total_evaluated integer NOT NULL DEFAULT 0,
    banned_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
