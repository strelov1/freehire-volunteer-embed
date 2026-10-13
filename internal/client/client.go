// Package client calls freehire-volunteer-embed's lease/submit protocol
// (see cmd/volunteer-embed) from a volunteer's own machine.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// requestTimeout bounds every call to the server, independent of whatever
// deadline (if any) the caller's context carries. This client runs
// unattended on a volunteer's own machine with no caller imposing a
// deadline of its own, so a merely-slow (not down) server must not be
// allowed to hang the poll loop indefinitely. A variable, not a const, so
// tests can shrink it instead of sleeping for the production default.
var requestTimeout = 30 * time.Second

type Job struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

type Submission struct {
	JobID  int64     `json:"job_id"`
	Vector []float64 `json:"vector"`
}

type Result struct {
	JobID  int64  `json:"job_id"`
	Status string `json:"status"`
}

// Lease requests up to batchSize jobs from the server's lease endpoint.
func Lease(ctx context.Context, httpClient *http.Client, serverURL, apiKey string, batchSize int) ([]Job, error) {
	payload, err := json.Marshal(map[string]int{"batch_size": batchSize})
	if err != nil {
		return nil, fmt.Errorf("client: encode lease request: %w", err)
	}

	var body struct {
		Jobs []Job `json:"jobs"`
	}
	if err := doJSON(ctx, httpClient, serverURL+"/v1/embed-tasks/lease", apiKey, payload, &body); err != nil {
		return nil, fmt.Errorf("client: lease: %w", err)
	}
	return body.Jobs, nil
}

// Submit posts computed embeddings back to the server's submit endpoint.
func Submit(ctx context.Context, httpClient *http.Client, serverURL, apiKey string, submissions []Submission) ([]Result, error) {
	payload, err := json.Marshal(map[string][]Submission{"submissions": submissions})
	if err != nil {
		return nil, fmt.Errorf("client: encode submit request: %w", err)
	}

	var body struct {
		Results []Result `json:"results"`
	}
	if err := doJSON(ctx, httpClient, serverURL+"/v1/embed-tasks/submit", apiKey, payload, &body); err != nil {
		return nil, fmt.Errorf("client: submit: %w", err)
	}
	return body.Results, nil
}

func doJSON(ctx context.Context, httpClient *http.Client, url, apiKey string, payload []byte, out any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, respBody)
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
