CREATE TABLE IF NOT EXISTS volunteer_submissions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id bigint NOT NULL,
    submitted_by_key_id bigint NOT NULL,
    embedding vector(768) NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'verified', 'rejected')),
    created_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz
);

CREATE INDEX IF NOT EXISTS volunteer_submissions_job_id_idx ON volunteer_submissions (job_id);
CREATE INDEX IF NOT EXISTS volunteer_submissions_status_idx ON volunteer_submissions (status);
