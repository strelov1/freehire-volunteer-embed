## ADDED Requirements

### Requirement: Authenticated task leasing
The system SHALL allow a caller holding a valid freehire API key with scope `embed` to lease a batch of pending jobs from `semantic_outbox` for embedding.

#### Scenario: Valid embed-scoped key leases a batch
- **WHEN** a caller calls `POST /v1/embed-tasks/lease` with a valid `Authorization: Bearer fhk_...` token whose `api_keys.scope` is `embed`
- **THEN** the system returns a batch of pending jobs (id and text) from `semantic_outbox`, each marked as leased to that key with a lease expiry

#### Scenario: Missing or wrong-scope key is rejected
- **WHEN** a caller calls `POST /v1/embed-tasks/lease` with a missing, invalid, expired, revoked, or non-`embed`-scoped token
- **THEN** the system returns 401 and leases nothing

### Requirement: Lease expiry returns unclaimed work to the pool
The system SHALL treat a leased job as available again once its lease expires without a submission, without requiring any action from the original leaseholder.

#### Scenario: Volunteer disappears mid-lease
- **WHEN** a job's lease expires and no submission for it has been received
- **THEN** the job becomes eligible to be leased again, by the same or a different key

### Requirement: Submitting computed vectors
The system SHALL allow a caller to submit embedding vectors for jobs it currently holds an active lease on.

#### Scenario: Submission for an actively leased job
- **WHEN** a caller calls `POST /v1/embed-tasks/submit` with a vector for a job it currently holds a non-expired lease on
- **THEN** the system accepts the submission for verification and does not yet modify `job_semantic_chunks`

#### Scenario: Submission for a job the caller does not hold
- **WHEN** a caller submits a vector for a job it never leased, or whose lease has expired, or that another key currently holds
- **THEN** the system rejects the submission with an error and takes no action against the caller's reputation

### Requirement: Malformed submissions are rejected without reputation impact
The system SHALL validate submitted vectors (correct count, correct dimensionality, numeric values) and reject invalid ones without affecting the submitting key's reputation counters.

#### Scenario: Wrong vector dimensionality
- **WHEN** a caller submits a vector that is not 768 numeric values
- **THEN** the system returns 400 and does not touch `volunteer_reputation` for that key
