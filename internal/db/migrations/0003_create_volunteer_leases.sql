-- volunteer_leases tracks which API key currently holds a claim on a
-- semantic_outbox job. semantic_outbox itself (owned by hire) records only
-- that a row is claimed (claimed_at), not by whom; this service has many
-- concurrent claimants and needs the latter.
CREATE TABLE IF NOT EXISTS volunteer_leases (
    job_id bigint PRIMARY KEY,
    leased_by_key_id bigint NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS volunteer_leases_expires_at_idx ON volunteer_leases (expires_at);
