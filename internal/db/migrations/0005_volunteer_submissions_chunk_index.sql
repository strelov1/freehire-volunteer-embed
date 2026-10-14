-- A job's description can split into more than one chunk (see
-- internal/jobtext's chunkText, ported from hire's own cmd/embed) — each
-- chunk is submitted and verified as its own row here, so this table needs
-- to say WHICH chunk a row is, not just which job it's for. Matches
-- job_semantic_chunks.chunk_index's type (smallint) and default (every
-- row before this migration was implicitly chunk 0, the only chunk the
-- old single-vector-per-job protocol ever produced).
--
-- NOTE: this table is owned by `postgres`, not the service's own
-- `volunteer_embed` role (see freehire-volunteer-embed-grants.sql's note
-- on migration 0004) — ALTER TABLE needs the owner, so this file must be
-- applied BY HAND as `postgres` before deploying a binary whose embedded
-- migrations list includes it, same as 0004. Deploying first would crash
-- the service on every startup (ApplyMigrations has no privilege to run
-- this itself and treats that as fatal).
ALTER TABLE volunteer_submissions
    ADD COLUMN chunk_index smallint NOT NULL DEFAULT 0;
