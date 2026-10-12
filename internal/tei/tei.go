// Package tei calls freehire's existing trusted TEI (Text-Embeddings-Inference)
// server to re-embed a job's text during spot-check verification.
package tei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Embed posts a single text to a TEI-compatible /embed endpoint and returns
// its embedding vector.
func Embed(ctx context.Context, teiURL, text string) ([]float64, error) {
	payload, err := json.Marshal(map[string][]string{"inputs": {text}})
	if err != nil {
		return nil, fmt.Errorf("tei: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, teiURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("tei: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tei: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tei: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tei: status %d: %s", resp.StatusCode, body)
	}

	var vectors [][]float64
	if err := json.Unmarshal(body, &vectors); err != nil {
		return nil, fmt.Errorf("tei: decode response: %w", err)
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("tei: response contained no vectors")
	}
	return vectors[0], nil
}
