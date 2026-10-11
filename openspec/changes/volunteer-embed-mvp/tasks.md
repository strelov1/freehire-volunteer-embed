## 1. Project scaffold

- [x] 1.1 Initialize Go module, directory layout (`cmd/`, `internal/`), linting/test tooling consistent with the `hire` and `freehire-cli` repos
- [x] 1.2 Config loading (DB connection string, trusted TEI URL, lease duration, check-rate parameters, listen address) from env vars
- [x] 1.3 Postgres connection pool setup, pointed at freehire's production database via a scoped role (connection string supplied via config, not hardcoded)

## 2. Data model

- [x] 2.1 Migration: create `volunteer_submissions` (job id, submitting key id, vector, status `pending`/`verified`/`rejected`, timestamps)
- [x] 2.2 Migration: create `volunteer_reputation` (api key id, consecutive_passes, consecutive_fails, banned_at nullable, timestamps)
- [x] 2.3 Migration: create `volunteer_leases` (job id primary key, leased_by_key_id, expires_at) — this service's own record of who currently holds a `semantic_outbox` claim, since that table has no claimant-identity column (discovered during implementation; see design.md Decision 6)
- [x] 2.4 Read-only query layer against existing `semantic_outbox`, its backing job text, and `api_keys` (no migrations — these tables are owned by `hire`)

## 3. Authentication

- [x] 3.1 Implement bearer-token validation: hash incoming `fhk_...` token, look up `api_keys` by hash, require non-expired/non-revoked and `scope = 'embed'`
- [x] 3.2 Implement banned-key check against `volunteer_reputation.banned_at` as part of auth, rejecting with 401
- [x] 3.3 Middleware wiring `Authorization: Bearer` parsing onto both endpoints

## 4. Lease endpoint

- [x] 4.1 `POST /v1/embed-tasks/lease`: atomically claim a batch of pending `semantic_outbox` rows not currently under an active lease, tag with caller's key id and lease expiry
- [x] 4.2 Lease expiry sweep: expired, unsubmitted leases become claimable again (reuse `semantic_outbox`'s existing lease-timeout convention)
- [x] 4.3 Batch size limit (configurable, small default) and empty-queue response handling

## 5. Submit endpoint (staging only)

- [ ] 5.1 `POST /v1/embed-tasks/submit`: validate caller holds an active lease on each submitted job id
- [ ] 5.2 Validate vector shape (768 floats, all finite numbers); reject malformed submissions with 400 without touching reputation
- [ ] 5.3 Persist valid submissions into `volunteer_submissions` with status `pending`

## 6. Spot-check verification

- [ ] 6.1 Check-rate decision function: given a key's `volunteer_reputation`, decide whether a given submission is spot-checked (100% under 20 total checks, stepped-down rate with a non-zero floor afterward)
- [ ] 6.2 Trusted-TEI client: re-embed a job's text via freehire's existing TEI endpoint
- [ ] 6.3 Cosine similarity comparison between submitted and trusted vectors, threshold 0.99, deciding verified vs rejected
- [ ] 6.4 Handle trusted-TEI unavailability by leaving the submission `pending` and changing no reputation counters
- [ ] 6.5 Submissions not selected for checking are marked `verified` directly (trust-on-track-record path)

## 7. Reputation bookkeeping

- [ ] 7.1 On verified pass: increment `consecutive_passes`, reset `consecutive_fails`
- [ ] 7.2 On spot-check failure: increment `consecutive_fails`, reset `consecutive_passes`, release the job's lease back to the pool
- [ ] 7.3 On `consecutive_fails` reaching 3: set `banned_at`, causing subsequent auth checks to reject the key

## 8. Verified promotion

- [ ] 8.1 Promotion transaction: write the verified vector into `job_semantic_chunks` and clear/complete the corresponding `semantic_outbox` row atomically
- [ ] 8.2 Idempotency guard: a job already promoted (e.g. via a stale re-submission) is not promoted twice
- [ ] 8.3 Integration check against a disposable copy of the real schema (not the production database) proving a promoted row is indistinguishable from `cmd/embed`'s own output

## 9. Operational basics

- [ ] 9.1 Request size limits and per-key rate limiting on both endpoints
- [ ] 9.2 Structured logging for lease/submit/verify/ban events (enough to audit a disputed ban)
- [ ] 9.3 Health check endpoint for the hosting platform

## 10. External dependency (tracked here, implemented in the `hire` repo)

- [ ] 10.1 (external) Migration in `hire`: add `embed` to the `api_keys.scope` CHECK constraint
- [ ] 10.2 (external) Allow minting an `embed`-scoped key via the existing key-creation path (web UI and/or `freehire-cli`)
- [ ] 10.3 Provision this service's scoped Postgres role against freehire's production database (least privilege per design.md's Decision 1)
