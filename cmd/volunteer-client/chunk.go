package main

import "context"

// chunkedEmbedBatch wraps an embedBatchFunc so it never hands more than
// chunkSize texts to the underlying embedder in a single call. This exists
// because the network-facing lease size (how many jobs a client pulls per
// round trip, for efficiency) and the local compute size a volunteer's own
// embedder can safely handle in one call are two different numbers — a
// naive local embedder with no batching limit of its own can thrash a
// machine's memory if handed an entire large lease at once. chunkSize <= 0
// means no chunking.
func chunkedEmbedBatch(inner embedBatchFunc, chunkSize int) embedBatchFunc {
	return func(ctx context.Context, texts []string) ([][]float64, error) {
		if chunkSize <= 0 || len(texts) <= chunkSize {
			return inner(ctx, texts)
		}

		vectors := make([][]float64, 0, len(texts))
		for start := 0; start < len(texts); start += chunkSize {
			end := min(start+chunkSize, len(texts))
			chunkVectors, err := inner(ctx, texts[start:end])
			if err != nil {
				return nil, err
			}
			vectors = append(vectors, chunkVectors...)
		}
		return vectors, nil
	}
}
