// Command volunteer-client is what a volunteer runs on their own machine:
// it leases jobs from freehire-volunteer-embed's server, embeds each one on
// local hardware via a TEI-compatible endpoint, and submits the results.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/strelov1/freehire-volunteer-embed/internal/client"
	"github.com/strelov1/freehire-volunteer-embed/internal/clientconfig"
	"github.com/strelov1/freehire-volunteer-embed/internal/tei"
)

func main() {
	cfg, err := clientconfig.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpClient := &http.Client{}

	lease := func(ctx context.Context) ([]client.Job, error) {
		return client.Lease(ctx, httpClient, cfg.ServerURL, cfg.APIKey, cfg.BatchSize)
	}
	embed := func(ctx context.Context, text string) ([]float64, error) {
		return tei.Embed(ctx, cfg.LocalTEIURL, cfg.LocalTEIAPIKey, text)
	}
	submit := func(ctx context.Context, submissions []client.Submission) ([]client.Result, error) {
		return client.Submit(ctx, httpClient, cfg.ServerURL, cfg.APIKey, submissions)
	}

	log.Printf("volunteer-client starting: server=%s local-tei=%s batch-size=%d", cfg.ServerURL, cfg.LocalTEIURL, cfg.BatchSize)

	for {
		result, err := runOnce(ctx, lease, embed, submit)
		if err != nil {
			log.Printf("run: %v", err)
		} else if result.Leased == 0 {
			log.Printf("no jobs available, waiting %s", cfg.PollInterval)
		} else {
			log.Printf("leased=%d submitted=%d", result.Leased, result.Submitted)
		}

		select {
		case <-ctx.Done():
			log.Print("shutting down")
			return
		case <-time.After(cfg.PollInterval):
		}
	}
}
