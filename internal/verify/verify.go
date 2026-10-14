// Package verify decides whether a volunteer's submitted embedding matches
// what freehire's own trusted TEI server computes for the same text, per
// design.md Decisions 4 and 5.
package verify

import "math"

// MatchThreshold is the minimum cosine similarity between a submitted
// vector and the trusted re-computation to count as a match. Comfortably
// above cross-hardware floating-point noise, well below the gap between
// unrelated texts (design.md Decision 4).
const MatchThreshold = 0.99

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
