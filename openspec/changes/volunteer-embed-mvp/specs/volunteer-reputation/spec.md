## ADDED Requirements

### Requirement: Tracking pass and fail streaks per key
The system SHALL maintain, per API key, a count of consecutive verified passes and a count of consecutive verification failures, resetting the opposite counter whenever one is incremented.

#### Scenario: A pass resets the fail streak
- **WHEN** a submission from a key is marked verified
- **THEN** that key's `consecutive_passes` increments and its `consecutive_fails` resets to zero

#### Scenario: A failure resets the pass streak
- **WHEN** a submission from a key is marked rejected after spot-check
- **THEN** that key's `consecutive_fails` increments and its `consecutive_passes` resets to zero

### Requirement: Banning after repeated failures
The system SHALL revoke the `embed` scope of any key that reaches 3 consecutive verification failures, preventing it from leasing further work.

#### Scenario: Third consecutive failure
- **WHEN** a key's `consecutive_fails` reaches 3
- **THEN** the system marks that key banned for embedding and subsequent lease requests from it are rejected with 401

### Requirement: Malformed submissions do not affect streaks
The system SHALL NOT modify a key's `consecutive_passes` or `consecutive_fails` as a result of a submission rejected for being malformed (wrong shape/size) rather than failing a spot-check.

#### Scenario: Wrong-shaped vector submitted
- **WHEN** a submission is rejected at the validation stage (not the spot-check stage)
- **THEN** neither `consecutive_passes` nor `consecutive_fails` changes for that key
