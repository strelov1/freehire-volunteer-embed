## Context

freehire runs a production job board with a semantic-search backlog: the `semantic_outbox` table in its Postgres database queues jobs awaiting an embedding vector, drained today by a single internal worker (`cmd/embed` in the `hire` repo) calling a trusted TEI (HuggingFace Text-Embeddings-Inference) server, using e5-multilingual-base (768-dim vectors, pgvector). At current throughput (~1,500 jobs/hour) the ~98k backlog takes days to clear.

This service lets anyone on the internet donate embedding compute against that same backlog, without asking freehire's maintainers to trust strangers' hardware or results. It is a deliberate "AI-compute-network, minus the blockchain" design: the parts of networks like Gonka that matter for this use case — distributing compute, detecting dishonest results — are kept; the parts that exist to support trustless strangers transacting value (consensus, token economics, cryptographic proof-of-work) are dropped, because freehire's maintainer is a single trusted verifier, not a decentralized market.

**Stakeholders:** freehire (benefits from faster backlog drain, bears the risk of bad data reaching its live search index), volunteers (donate spare compute, need a low-friction way to prove they're not malicious), this service (sits in the middle, is the thing that can get this wrong).

**Key existing constraint carried over from freehire:** writes into `job_semantic_chunks` are destructive — a bad vector silently overwrites a job's embedding and the outbox will not re-queue it on its own. Nothing unverified may reach that table.

## Goals / Non-Goals

**Goals:**
- Let an anonymous volunteer, authenticated only with a freehire API key, help drain the real `semantic_outbox` backlog.
- Catch dishonest or broken submissions before they reach `job_semantic_chunks`, using spot-check re-verification against freehire's own trusted TEI, not cryptography.
- Spend as little of the trusted TEI's compute on verification as the volunteer's track record allows, without ever reaching zero verification.
- Keep the blast radius of a compromised or overloaded volunteer-facing service away from freehire's main site and database integrity.

**Non-Goals (explicitly out of scope for this change):**
- Reward economics beyond "it helps the project" (no token, no payment, no leaderboard UI).
- Sybil resistance beyond "needs a real freehire account with a confirmed email" — a determined attacker with many accounts is not defended against in v1.
- Auto-scaling, multi-region deployment, or high-availability hosting.
- Any change to freehire's search-ranking or embedding model itself.

## Decisions

**1. Standalone repo/service with a direct, narrowly-scoped connection to freehire's production Postgres — not new endpoints added to `hire`'s own API.**
Alternative considered: add `lease`/`submit` HTTP handlers directly to `hire`'s existing API process. Rejected because it would put an open-to-the-internet, adversarial-input surface inside the same process and deploy pipeline as freehire's main site — a bug or overload here would risk the site itself. A separate service with its own Postgres role (read on `semantic_outbox`/job text/`api_keys`; write only on its two owned tables and a narrow promote path into `job_semantic_chunks`) keeps that surface isolated while still sharing the one source of truth, with no sync/replication lag to reason about.

**2. Reuse freehire's existing `api_keys` bearer tokens for volunteer auth; validate them by querying the table directly with the same hash scheme freehire uses (SHA-256 of the `fhk_...` token), rather than calling back into `hire`'s API per request.**
Alternative considered: have `hire` expose an internal "validate this token" endpoint. Rejected for v1 — it's an extra network hop and an extra thing to keep running for a check that's a single indexed lookup against data this service already reads. The only change needed in `hire` is a migration allowing a new `embed` value in `api_keys.scope`'s CHECK constraint, and allowing a user to mint a key with that scope — tracked as an external task, not built here.

**3. Staging-then-promote, never direct-write.**
Every submitted vector lands in `volunteer_submissions` (status `pending`/`verified`/`rejected`) first. Only a `verified` row gets promoted, in one transaction, into `job_semantic_chunks` with its `semantic_outbox` row cleared — mirroring exactly what `cmd/embed` already does on success, so a promoted row is indistinguishable from one the trusted worker produced itself.

**4. Spot-check by cosine similarity against the trusted TEI, threshold 0.99, not bit-exact comparison.**
The same model run on different hardware/batch sizes produces tiny floating-point differences, so exact equality would false-positive on honest submissions. 0.99 cosine similarity is comfortably above the floating-point noise floor observed in testing and well below the similarity gap that distinguishes "the right job description" from "a different one."

**5. Adaptive check rate driven by a simple streak counter, not a continuous trust score.**
A volunteer's `volunteer_reputation` row tracks `consecutive_passes` and `consecutive_fails`. Check probability is a step function of `consecutive_passes` (100% for the first 20 submissions, then stepping down — e.g. 1-in-10, then 1-in-30 — with a floor that is never 0%). Any single failure resets `consecutive_passes` to 0 (back to 100% checking) and increments `consecutive_fails`; reaching 3 revokes the key's `embed` scope. This is simple to reason about and audit, at the cost of being less sample-efficient than a real trust/reputation model — acceptable for v1.
Malformed submissions (wrong vector length, non-numeric values) are rejected with HTTP 400 and do **not** touch either streak — they're a client bug, not a signal about honesty.

**6. Lease semantics are copied from `cmd/embed`'s existing pattern, not reinvented**: claim a batch with a lease expiry; an unsubmitted claim past expiry returns to the pool automatically. This service and the internal `cmd/embed` worker both draw from the same `semantic_outbox`, so a volunteer's abandoned claim is just as recoverable as an internal worker's crash.

## Risks / Trade-offs

- **[Risk] A volunteer builds a long clean streak, then starts submitting bad vectors.** Because checks get rarer over time, some bad vectors could land before the next spot-check catches the pattern and bans the key. → *Mitigation:* accepted trade-off for v1; the check-rate floor (never 0%) bounds how long this can go undetected, and a later version could re-verify a random sample of already-promoted rows retroactively if this proves to be a real problem in practice.
- **[Risk] This service's Postgres role is a new credential with write access to a production table.** A bug in the promote path could still corrupt `job_semantic_chunks` even without malicious volunteers. → *Mitigation:* the promote path is the smallest, most heavily tested piece of this service; it performs exactly one shape of write (one vector, one chunk, one transaction) and nothing else touches that table.
- **[Risk] Open-to-the-internet HTTP surface invites abuse unrelated to embedding correctness** (request flooding, oversized payloads, slow-loris connections). → *Mitigation:* basic request size limits and per-key rate limiting on `lease`/`submit`; full DDoS protection is out of scope for v1 and deferred to whatever sits in front of this service (reverse proxy / hosting provider).
- **[Trade-off] No reward beyond goodwill.** This caps how many volunteers will realistically show up, but avoids building payment/token infrastructure that this project does not need to prove the idea.

## Migration Plan

1. **External, in `hire`**: add `embed` to the `api_keys.scope` CHECK constraint via migration; allow minting an `embed`-scoped key (web UI and/or `freehire-cli`). This can ship independently and has no effect until this service exists.
2. **In this repo**: stand up the two new tables (`volunteer_submissions`, `volunteer_reputation`) via this service's own migrations, under its own scoped Postgres role (not a superuser or the role `hire` itself uses).
3. Deploy the service with `lease`/`submit` live but reject-all (or feature-flagged off) until manually verified against a handful of real `semantic_outbox` rows in a dry run.
4. Flip it live; monitor promote-path volume and spot-check pass rate.
5. **Rollback**: disable the service (stop accepting `lease`), nothing to unwind in `hire` itself since promoted rows are indistinguishable from normal `cmd/embed` output — the internal worker keeps draining the backlog exactly as before.

## Open Questions

- **Hosting**: where does this service run (its own small VM, a container platform, etc.) and how is TLS terminated? Not yet decided — needed before `lease`/`submit` can be reachable from the internet.
- **Postgres credential provisioning**: who creates the scoped role on freehire's production database, and how are its credentials delivered to this service's deployment environment?
- **Batch size per lease**: how many jobs per `lease` call is a reasonable default (affects both throughput and how much rework a lease timeout costs)?
