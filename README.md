# freehire-volunteer-embed

Lets anonymous volunteers donate spare compute to drain freehire's job
embedding backlog. A volunteer runs a small client on their own machine:
it leases a batch of real jobs from freehire's production queue, computes
embedding vectors locally, and submits them back. Submissions are
spot-checked against freehire's own trusted embedding server before ever
reaching the live search index — nothing a volunteer sends goes live
unverified.

See `openspec/changes/volunteer-embed-mvp/design.md` for the full design
(lease/submit protocol, spot-check verification, staging-then-promote).

## What's in this repo

- `cmd/volunteer-embed` — the server. Exposes `POST /v1/embed-tasks/lease`
  and `POST /v1/embed-tasks/submit`, talks to freehire's production
  Postgres through a narrowly-scoped role, verifies submissions against a
  trusted TEI-compatible endpoint, and promotes verified vectors into
  freehire's live search table.
- `cmd/volunteer-client` — what a volunteer runs on their own machine: a
  small loop that leases jobs, embeds them locally via any TEI-compatible
  `/embed` endpoint, and submits the results.
- `cmd/tei-priority-proxy` — an optional priority scheduler for a
  contended trusted TEI (not currently in use — see its own package doc).

## Running the volunteer client

See [`HOW_TO_RUN_THE_CLIENT.md`](HOW_TO_RUN_THE_CLIENT.md)
for a full walkthrough: building the client, running a local embedding
server, and confirming it's actually processing real jobs.

You'll need a freehire API key with the `embed` scope.

## Running the server locally (development)

```bash
go build ./...
TEST_DATABASE_URL=postgres://postgres:test@localhost:15432/testdb?sslmode=disable go test -p 1 ./...
```

The server itself needs `DATABASE_URL` (a Postgres connection string) and
`TRUSTED_TEI_URL` (a TEI-compatible `/embed` endpoint) to start — see
`internal/config/config.go` for every setting and its default.
