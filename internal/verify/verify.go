// Package verify decides whether a volunteer's submitted embedding matches
// what freehire's own trusted TEI server computes for the same text, per
// design.md Decisions 4 and 5.
package verify

import (
	"context"
	"math"

	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/reputation"
	"github.com/strelov1/freehire-volunteer-embed/internal/tei"
)

// matchThreshold is the minimum cosine similarity between a submitted
// vector and the trusted re-computation to count as a match. Comfortably
// above cross-hardware floating-point noise, well below the gap between
// unrelated texts (design.md Decision 4).
const matchThreshold = 0.99

type Outcome int

const (
	// OutcomeVerifiedWithoutCheck: trusted on track record, not re-embedded.
	OutcomeVerifiedWithoutCheck Outcome = iota
	// OutcomeVerifiedPassedCheck: re-embedded and matched.
	OutcomeVerifiedPassedCheck
	// OutcomeRejectedFailedCheck: re-embedded and did not match.
	OutcomeRejectedFailedCheck
	// OutcomeDeferredTEIUnavailable: selected for checking, but the trusted
	// TEI could not be reached. Callers must leave the submission pending
	// and must not change any reputation counters.
	OutcomeDeferredTEIUnavailable
)

// Evaluate decides whether to spot-check a submission (via
// reputation.ShouldCheck) and, if so, re-embeds jobText through the trusted
// TEI and compares it to submittedVector by cosine similarity.
func Evaluate(ctx context.Context, teiURL string, cfg config.Config, totalChecked, consecutivePasses int, jobText string, submittedVector []float64) Outcome {
	if !reputation.ShouldCheck(totalChecked, consecutivePasses, cfg) {
		return OutcomeVerifiedWithoutCheck
	}

	trustedVector, err := tei.Embed(ctx, teiURL, jobText)
	if err != nil {
		return OutcomeDeferredTEIUnavailable
	}

	if Cosine(submittedVector, trustedVector) >= matchThreshold {
		return OutcomeVerifiedPassedCheck
	}
	return OutcomeRejectedFailedCheck
}

// Cosine returns the cosine similarity of two equal-length vectors.
func Cosine(a, b []float64) float64 {
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
