package teiproxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// NewHandler serves two paths that both forward their raw request body to
// upstreamURL (the real trusted TEI's /embed) and return its response
// verbatim — /high/embed for volunteer spot-check verification, /low/embed
// for hire's own backfill worker. The only difference between them is
// which Dispatcher lane they go through. apiKey, when non-empty, is sent
// as a Bearer token to upstreamURL: this proxy holds the one shared
// secret so neither caller needs to know it anymore.
func NewHandler(d *Dispatcher, upstreamURL, apiKey string, httpClient *http.Client) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/high/embed", forwardHandler(d, LaneHigh, upstreamURL, apiKey, httpClient))
	mux.Handle("/low/embed", forwardHandler(d, LaneLow, upstreamURL, apiKey, httpClient))
	return mux
}

type forwardResult struct {
	status int
	body   []byte
}

func forwardHandler(d *Dispatcher, lane Lane, upstreamURL, apiKey string, httpClient *http.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "reading request body", http.StatusBadRequest)
			return
		}

		result, err := Do(r.Context(), d, lane, func() (forwardResult, error) {
			return forwardToUpstream(r.Context(), httpClient, upstreamURL, apiKey, reqBody)
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		w.WriteHeader(result.status)
		w.Write(result.body)
	})
}

func forwardToUpstream(ctx context.Context, httpClient *http.Client, upstreamURL, apiKey string, body []byte) (forwardResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return forwardResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return forwardResult{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return forwardResult{}, err
	}
	return forwardResult{status: resp.StatusCode, body: respBody}, nil
}
