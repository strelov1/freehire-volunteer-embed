-- A job can have more than one pending semantic_outbox row at once (e.g. mid
-- migration between two embedding model versions, one row per target_model).
-- Keying volunteer_leases by job_id alone collapsed two distinct outbox
-- claims into one, which is what let promoting one submission silently
-- delete the other's still-pending row (see freehire-ops' incident
-- writeup). Keying by the specific outbox row id fixes that; job_id stays
-- as a plain indexed column for lookups that only care about the job.
--
-- TRUNCATE is safe here: this service has been live under a day with no
-- real volunteers yet, so every existing row is test data that would
-- otherwise just expire on its own.
TRUNCATE volunteer_leases;

ALTER TABLE volunteer_leases
    DROP CONSTRAINT volunteer_leases_pkey,
    ADD COLUMN outbox_id bigint NOT NULL,
    ADD CONSTRAINT volunteer_leases_pkey PRIMARY KEY (outbox_id);

CREATE INDEX IF NOT EXISTS volunteer_leases_job_id_idx ON volunteer_leases (job_id);
