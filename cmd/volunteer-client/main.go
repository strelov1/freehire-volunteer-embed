// Command volunteer-client is what a volunteer runs on their own machine:
// it leases jobs from freehire-volunteer-embed's server, embeds each one on
// local hardware via a TEI-compatible endpoint, and submits the results.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
	rawEmbedBatch := func(ctx context.Context, texts []string) ([][]float64, error) {
		return tei.EmbedBatch(ctx, cfg.LocalTEIURL, cfg.LocalTEIAPIKey, texts)
	}

	var embedBatch embedBatchFunc
	var chunkSizeDescription string
	if cfg.LocalEmbedChunkSize > 0 {
		// An explicit size pins it and disables auto-tuning — the escape
		// hatch for a volunteer who wants to cap this by hand.
		embedBatch = chunkedEmbedBatch(rawEmbedBatch, cfg.LocalEmbedChunkSize)
		chunkSizeDescription = fmt.Sprintf("%d (fixed)", cfg.LocalEmbedChunkSize)
	} else {
		sizer := newAdaptiveChunkSize(adaptiveSeedChunkSize, adaptiveMinChunkSize, cfg.LocalEmbedChunkSizeMax)
		embedBatch = adaptiveEmbedBatch(rawEmbedBatch, sizer, time.Now, availableMemoryFraction)
		chunkSizeDescription = fmt.Sprintf("auto (seed %d, max %d)", adaptiveSeedChunkSize, cfg.LocalEmbedChunkSizeMax)
	}

	embedBatch = newEmbedGate(cfg.LoadPercent, sleepCtx).wrap(embedBatch)

	submit := func(ctx context.Context, submissions []client.Submission) ([]client.Result, error) {
		return client.Submit(ctx, httpClient, cfg.ServerURL, cfg.APIKey, submissions)
	}

	log.Printf("volunteer-client starting: server=%s local-tei=%s batch-size=%d local-embed-chunk-size=%s load=%d%% workers=%d",
		cfg.ServerURL, cfg.LocalTEIURL, cfg.BatchSize, chunkSizeDescription, cfg.LoadPercent, workerCount)

	var wg sync.WaitGroup
	for w := 1; w <= workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runWorker(ctx, w, cfg.PollInterval, lease, embedBatch, submit)
		}()
	}
	wg.Wait()
	log.Print("shutting down")
}

// workerCount is how many lease-embed-submit loops run at once. They share
// the embedder through embedGate, one batch at a time, so more workers do
// not mean more load — they mean the next batch is already leased while
// the current one embeds, and the embedder never waits on the network.
// Six is measured, not derived (2026-10-18, LOAD=hard, an M-series Mac
// against a loaded server whose submit took 10-47s): four kept the
// embedder 72% busy at 12 texts/s, six kept it 92% busy at 20 texts/s —
// the machine's own ceiling for this model.
const workerCount = 6

// runWorker loops until ctx is cancelled, waiting pollInterval only when
// there was nothing to do or a call failed: sleeping after a batch that
// succeeded only idles the embedder while work is queued.
func runWorker(ctx context.Context, id int, pollInterval time.Duration, lease leaseFunc, embedBatch embedBatchFunc, submit submitFunc) {
	for ctx.Err() == nil {
		result, err := runOnce(ctx, lease, embedBatch, submit)
		switch {
		case err != nil:
			log.Printf("worker %d: run: %v", id, err)
		case result.Leased == 0:
			log.Printf("worker %d: no jobs available, waiting %s", id, pollInterval)
		default:
			log.Printf("worker %d: leased=%d submitted=%d", id, result.Leased, result.Submitted)
			if result.Submitted == result.Leased {
				continue
			}
		}
		if sleepCtx(ctx, pollInterval) != nil {
			return
		}
	}
}
