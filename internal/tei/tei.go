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
	"time"
)

// requestTimeout bounds every call to Embed, independent of whatever
// deadline (if any) the caller's context carries. Spot-check verification
// runs synchronously inside a volunteer's submit request (see
// internal/submit), so a slow — not down, just slow — trusted TEI must not
// be allowed to hang that request indefinitely; it should instead fail fast
// into OutcomeDeferredTEIUnavailable like an unreachable one does. A
// variable, not a const, so tests can shrink it instead of sleeping for the
// production default.
var requestTimeout = 10 * time.Second

// Embed posts a single text to a TEI-compatible /embed endpoint and returns
// its embedding vector. apiKey is sent as a Bearer token when non-empty —
// freehire's own trusted TEI needs none (a loopback service with no auth),
// but some TEI-compatible deployments (e.g. HF Inference Endpoints, or a
// volunteer's own authenticated server) require one.
func Embed(ctx context.Context, teiURL, apiKey, text string) ([]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	payload, err := json.Marshal(map[string][]string{"inputs": {text}})
	if err != nil {
		return nil, fmt.Errorf("tei: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, teiURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("tei: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

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
