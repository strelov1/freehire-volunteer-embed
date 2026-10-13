// Command tei-priority-proxy sits in front of freehire's single trusted
// TEI server and gives volunteer spot-check verification priority over
// hire's own backfill worker, which both compete for the same upstream.
// See internal/teiproxy for the actual scheduling guarantee.
//
// This process sits in hire's own production embedding path once its
// EMBED_URL is pointed at this proxy's /low/embed — a bug or crash here
// can stall hire's real backfill worker, not just the volunteer side. Kept
// deliberately small (stdlib only, two files of real logic) for that
// reason.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/strelov1/freehire-volunteer-embed/internal/teiproxy"
)

func main() {
	upstreamURL := os.Getenv("UPSTREAM_TEI_URL")
	if upstreamURL == "" {
		log.Fatal("UPSTREAM_TEI_URL is required")
	}
	// /high/embed and /low/embed carry no auth — safe only because this
	// defaults to loopback. An operator setting LISTEN_ADDR explicitly is
	// trusted to keep that property.
	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8091"
	}
	maxConcurrent := envIntOrDefault("MAX_CONCURRENT", 4)
	const maxRequestBodyBytes = 1 << 20 // 1 MiB: one batch of text to embed, generously bounded

	d := teiproxy.NewDispatcher(256)
	d.RunWorkers(context.Background(), maxConcurrent)

	// 60s to match hire's own embedAttemptTimeout for a single TEI call
	// (internal/search/search/embed.go) — this proxy must never be the
	// tighter timeout on a path hire's own worker already tolerates.
	httpClient := &http.Client{Timeout: 60 * time.Second}
	handler := teiproxy.NewHandler(d, upstreamURL, httpClient)

	mux := http.NewServeMux()
	mux.Handle("/", limitBodySize(maxRequestBodyBytes, handler))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	log.Printf("tei-priority-proxy listening on %s, upstream=%s, workers=%d", listenAddr, upstreamURL, maxConcurrent)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

func limitBodySize(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

func envIntOrDefault(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("%s invalid: %v", name, err)
	}
	return n
}
