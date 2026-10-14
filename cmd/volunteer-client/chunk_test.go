package main

import (
	"context"
	"errors"
	"testing"
)

// TestChunkedEmbedBatch_WithinChunkSize_CallsInnerOnce guards the common
// case: a lease batch no bigger than the chunk size needs no splitting at
// all — one call, not an unnecessary extra round trip to the local embedder.
func TestChunkedEmbedBatch_WithinChunkSize_CallsInnerOnce(t *testing.T) {
	var calls [][]string
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		calls = append(calls, texts)
		return make([][]float64, len(texts)), nil
	}

	wrapped := chunkedEmbedBatch(inner, 10)
	texts := []string{"a", "b", "c"}
	_, err := wrapped(context.Background(), texts)
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 call to inner, got %d: %+v", len(calls), calls)
	}
}

// TestChunkedEmbedBatch_ExceedsChunkSize_SplitsAndConcatenatesInOrder is the
// whole point of this wrapper: protect a volunteer's own local embedder
// (which may have no internal batching limit of its own — this project hit
// exactly that running a naive local server with a batch of 500, which
// thrashed memory and never returned) from an arbitrarily large lease
// batch, independent of how many jobs were leased over the network.
func TestChunkedEmbedBatch_ExceedsChunkSize_SplitsAndConcatenatesInOrder(t *testing.T) {
	var calls [][]string
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		calls = append(calls, texts)
		vecs := make([][]float64, len(texts))
		for i, t := range texts {
			vecs[i] = []float64{float64(len(t))} // identifies which text this came from
		}
		return vecs, nil
	}

	wrapped := chunkedEmbedBatch(inner, 2)
	texts := []string{"a", "bb", "ccc", "dddd", "e"} // 5 texts, chunk size 2 -> 3 calls
	vectors, err := wrapped(context.Background(), texts)
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}

	if len(calls) != 3 {
		t.Fatalf("expected 3 chunked calls for 5 texts at chunk size 2, got %d: %+v", len(calls), calls)
	}
	wantCalls := [][]string{{"a", "bb"}, {"ccc", "dddd"}, {"e"}}
	for i, want := range wantCalls {
		if len(calls[i]) != len(want) {
			t.Errorf("call %d = %v, want %v", i, calls[i], want)
			continue
		}
		for j := range want {
			if calls[i][j] != want[j] {
				t.Errorf("call %d = %v, want %v", i, calls[i], want)
			}
		}
	}

	if len(vectors) != len(texts) {
		t.Fatalf("len(vectors) = %d, want %d", len(vectors), len(texts))
	}
	for i, text := range texts {
		if vectors[i][0] != float64(len(text)) {
			t.Errorf("vectors[%d] does not match texts[%d] (%q) — results were concatenated out of order", i, i, text)
		}
	}
}

func TestChunkedEmbedBatch_InnerFailsOnAChunk_PropagatesTheError(t *testing.T) {
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		if texts[0] == "c" {
			return nil, errors.New("local embedder unreachable")
		}
		return make([][]float64, len(texts)), nil
	}

	wrapped := chunkedEmbedBatch(inner, 1)
	_, err := wrapped(context.Background(), []string{"a", "b", "c", "d"})
	if err == nil {
		t.Fatal("expected the error from the failing chunk to propagate, got nil")
	}
}

func TestChunkedEmbedBatch_ZeroChunkSize_MeansNoChunking(t *testing.T) {
	var calls int
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		calls++
		return make([][]float64, len(texts)), nil
	}

	wrapped := chunkedEmbedBatch(inner, 0)
	texts := make([]string, 500)
	for i := range texts {
		texts[i] = "x"
	}
	if _, err := wrapped(context.Background(), texts); err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected chunkSize=0 to mean no chunking (1 call), got %d calls", calls)
	}
}
