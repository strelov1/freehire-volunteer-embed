## ADDED Requirements

### Requirement: Deciding whether to verify a submission
The system SHALL decide, per submission, whether to spot-check it against the trusted TEI server, based on the submitting key's current `volunteer_reputation` streaks, and this decision SHALL never result in a 0% check rate for any key.

#### Scenario: New volunteer is checked on every submission
- **WHEN** a key has fewer than 20 total checked submissions
- **THEN** every submission from that key is spot-checked

#### Scenario: Long clean streak reduces check frequency
- **WHEN** a key has a long `consecutive_passes` streak
- **THEN** the system checks that key's submissions less often than a new key's, but never at a 0% rate

### Requirement: Re-embedding and comparing
The system SHALL, for a submission selected for spot-check, re-embed the job's text using the trusted TEI server and compare the result to the submitted vector by cosine similarity.

#### Scenario: Submission matches the trusted re-computation
- **WHEN** the cosine similarity between a submitted vector and the trusted TEI's re-computed vector for the same text is at or above 0.99
- **THEN** the submission is marked verified

#### Scenario: Submission diverges from the trusted re-computation
- **WHEN** the cosine similarity between a submitted vector and the trusted TEI's re-computed vector is below 0.99
- **THEN** the submission is marked rejected and the job is released back to the lease pool

### Requirement: Trusted TEI unavailability does not penalize the volunteer
The system SHALL defer a spot-check rather than treat it as a failure when the trusted TEI server cannot be reached.

#### Scenario: Trusted server is down during a scheduled check
- **WHEN** the system selects a submission for spot-check but the trusted TEI server is unreachable
- **THEN** the submission remains pending (neither verified nor rejected) and no reputation counters change
