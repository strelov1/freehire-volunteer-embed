## ADDED Requirements

### Requirement: Only verified vectors reach the live search table
The system SHALL write a submitted vector into freehire's `job_semantic_chunks` table only after that submission has been marked verified, whether by passing spot-check or by not being selected for spot-check under the current reputation-driven check rate.

#### Scenario: Verified submission is promoted
- **WHEN** a submission is marked verified (checked-and-passed, or not selected for checking)
- **THEN** the system writes its vector into `job_semantic_chunks` and clears the corresponding `semantic_outbox` row in the same transaction

#### Scenario: Rejected or pending submission is never promoted
- **WHEN** a submission is rejected by spot-check, or is still pending a check
- **THEN** the system does not write anything into `job_semantic_chunks` for that job

### Requirement: Promotion is atomic and idempotent per job
The system SHALL ensure that a job is promoted at most once, even if multiple submissions for it exist (e.g., a re-leased job after a prior rejection).

#### Scenario: A previously rejected job is later completed by another volunteer
- **WHEN** a job was leased, submitted, and rejected once, then re-leased and submitted again by a different key, and that second submission is verified
- **THEN** only the second, verified vector is promoted into `job_semantic_chunks`
