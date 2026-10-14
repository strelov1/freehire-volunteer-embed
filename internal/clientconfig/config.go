// Package clientconfig loads the volunteer client's settings from
// environment variables. Required variables fail loudly when unset.
package clientconfig

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ServerURL              string
	APIKey                 string
	LocalTEIURL            string
	LocalTEIAPIKey         string
	BatchSize              int
	LocalEmbedChunkSize    int
	LocalEmbedChunkSizeMax int
	PollInterval           time.Duration
}

func Load() (Config, error) {
	serverURL := os.Getenv("SERVER_URL")
	if serverURL == "" {
		return Config{}, fmt.Errorf("clientconfig: SERVER_URL is required")
	}
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return Config{}, fmt.Errorf("clientconfig: API_KEY is required")
	}
	localTEIURL := os.Getenv("LOCAL_TEI_URL")
	if localTEIURL == "" {
		return Config{}, fmt.Errorf("clientconfig: LOCAL_TEI_URL is required")
	}

	batchSize := 10
	if v := os.Getenv("BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("clientconfig: BATCH_SIZE invalid: %w", err)
		}
		if n <= 0 {
			return Config{}, fmt.Errorf("clientconfig: BATCH_SIZE must be positive, got %d", n)
		}
		batchSize = n
	}

	pollInterval := 30 * time.Second
	if v := os.Getenv("POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("clientconfig: POLL_INTERVAL invalid: %w", err)
		}
		pollInterval = d
	}

	// Default 0 means the client auto-tunes this itself: start small, grow
	// while the local embedder stays fast, back off at the first sign of
	// trouble (see cmd/volunteer-client/adaptive.go). Setting this pins a
	// fixed size instead and disables auto-tuning — the manual override for
	// a volunteer who wants to cap it themselves.
	localEmbedChunkSize := 0
	if v := os.Getenv("LOCAL_EMBED_CHUNK_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("clientconfig: LOCAL_EMBED_CHUNK_SIZE invalid: %w", err)
		}
		if n < 0 {
			return Config{}, fmt.Errorf("clientconfig: LOCAL_EMBED_CHUNK_SIZE must not be negative, got %d", n)
		}
		localEmbedChunkSize = n
	}

	// A volunteer's real safe ceiling depends on their own hardware and is
	// unknowable in advance; 100 is a conservative starting point, not a
	// measured safe value for every machine — a machine that still can't
	// hold this much in memory at once should lower it explicitly.
	localEmbedChunkSizeMax, err := intEnvOrDefault("LOCAL_EMBED_CHUNK_SIZE_MAX", 100)
	if err != nil {
		return Config{}, err
	}
	if localEmbedChunkSizeMax <= 0 {
		return Config{}, fmt.Errorf("clientconfig: LOCAL_EMBED_CHUNK_SIZE_MAX must be positive, got %d", localEmbedChunkSizeMax)
	}

	return Config{
		ServerURL:              serverURL,
		APIKey:                 apiKey,
		LocalTEIURL:            localTEIURL,
		LocalTEIAPIKey:         os.Getenv("LOCAL_TEI_API_KEY"),
		BatchSize:              batchSize,
		LocalEmbedChunkSize:    localEmbedChunkSize,
		LocalEmbedChunkSizeMax: localEmbedChunkSizeMax,
		PollInterval:           pollInterval,
	}, nil
}

func intEnvOrDefault(name string, fallback int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("clientconfig: %s invalid: %w", name, err)
	}
	return n, nil
}
