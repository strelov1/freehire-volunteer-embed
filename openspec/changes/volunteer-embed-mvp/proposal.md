## Why

freehire's semantic search backlog (`semantic_outbox`) currently sits at ~98k unembedded jobs, processed by a single worker on the production host at roughly 1,500/hour. Letting outside volunteers run their own embedding hardware against the same queue — inspired by decentralized AI-compute networks like Gonka, but without blockchain/token machinery — can clear the backlog faster using donated compute, at near-zero engineering cost on freehire's side.

## What Changes

- New standalone Go service (`freehire-volunteer-embed`) that lets anonymous internet volunteers lease batches of real jobs from freehire's production `semantic_outbox` queue, compute embeddings on their own hardware, and submit the resulting vectors.
- Volunteers authenticate with freehire's existing `api_keys` bearer tokens (`fhk_...`), reusing the identity system already exposed via the freehire website and `freehire-cli auth login`. No new signup flow.
- Submitted vectors never go directly into freehire's live search table (`job_semantic_chunks`). They land in a staging table first and are promoted only after passing verification.
- Trust is established without cryptography: the service spot-checks a sample of each volunteer's submissions by re-embedding the same text on freehire's existing trusted TEI server and comparing vectors by cosine similarity. New volunteers are checked on every submission; volunteers with a long clean streak are checked less often (adaptive rate, with a floor — never 0%). Three consecutive failed checks revokes the volunteer's access; a malformed/wrong-shaped submission does not count as a failure (it's a client bug, not dishonesty).
- **Dependency on the freehire/hire repo (tracked here, implemented there, out of scope for this repo):** a small migration adding `embed` as an allowed value to the `api_keys.scope` CHECK constraint, plus allowing that scope to be selected when minting a key. No new HTTP endpoints are needed in hire — this service reads/writes freehire's production Postgres directly, using a narrowly-scoped database role.

## Capabilities

### New Capabilities
- `task-lease-protocol`: HTTP API (`lease`/`submit`) that lets an authenticated volunteer claim a batch of pending jobs from `semantic_outbox` and return computed embedding vectors for them, reusing the existing lease-timeout convention so abandoned claims return to the pool.
- `spot-check-verification`: re-embeds a sampled subset of volunteer submissions via the production TEI endpoint and compares them to the submitted vector by cosine similarity to decide pass/fail.
- `volunteer-reputation`: per-API-key trust bookkeeping — consecutive-pass streak (drives adaptive check frequency) and consecutive-fail streak (drives ban-after-3).
- `verified-promotion`: moves a vector from the staging table into freehire's real `job_semantic_chunks` table and clears its `semantic_outbox` entry only after verification passes; unverified or failed submissions never touch the live table.

### Modified Capabilities
(none — this is a new repository with no existing specs)

## Impact

- **New production database objects** (in freehire's existing Postgres, via a scoped role): tables `volunteer_submissions`, `volunteer_reputation`, `volunteer_leases`, owned by this service. `volunteer_leases` exists because `semantic_outbox` itself records only *that* a row is claimed, not *by whom* — this service needs the latter to have many concurrent claimants.
- **Read access** to freehire's `semantic_outbox`, the job text backing it, and `api_keys` (for bearer-token validation using the same hash scheme freehire already uses).
- **Write access** limited to: the three new tables, and a narrow promote-path `UPDATE`/`INSERT` into `job_semantic_chunks` plus clearing the corresponding `semantic_outbox` row.
- **Calls out to** freehire's existing trusted TEI embedding endpoint for spot-check re-computation.
- **External dependency**: a small, separately-tracked change in the `hire` repo (CHECK-constraint migration for `api_keys.scope`); not implemented in this repository.
- **New public-facing HTTP surface**: this service's `lease`/`submit` endpoints are reachable from the open internet; hosting/TLS is an open question resolved in `design.md`.
