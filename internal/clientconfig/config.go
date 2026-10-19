// Package clientconfig loads the volunteer client's settings from
// environment variables. Required variables fail loudly when unset.
package clientconfig

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
	// LoadPercent is how much of the time the local embedder may be busy,
	// 1..100: LOAD=easy (25), medium (50, the default), hard (100), or a
	// number. It is a share of the volunteer's machine, so the default
	// leaves half of it to whatever else they are doing.
	LoadPercent int
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

	// Measured 2026-10-18 on an Apple Silicon Mac (multilingual-e5-base on
	// MPS, 512-token texts): throughput is flat from 1 to 64 texts per call,
	// ~18-22 texts/s, peaking at 8 — the GPU is saturated by one text. A
	// bigger call buys nothing and costs memory that grows with it, and on
	// real descriptions of mixed length it also pays for padding every text
	// to the longest: the tuner, left to climb to 90, ran ~290ms a text
	// against ~40ms at 1-4 while the machine dropped to 18% free memory. 16
	// keeps headroom over the peak for faster hardware; raise it for a
	// discrete GPU, where bigger calls do pay.
	localEmbedChunkSizeMax, err := intEnvOrDefault("LOCAL_EMBED_CHUNK_SIZE_MAX", 16)
	if err != nil {
		return Config{}, err
	}
	if localEmbedChunkSizeMax <= 0 {
		return Config{}, fmt.Errorf("clientconfig: LOCAL_EMBED_CHUNK_SIZE_MAX must be positive, got %d", localEmbedChunkSizeMax)
	}

	loadPercent, err := parseLoad(os.Getenv("LOAD"))
	if err != nil {
		return Config{}, err
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
		LoadPercent:            loadPercent,
	}, nil
}

var loadModes = map[string]int{"easy": 25, "medium": 50, "hard": 100}

func parseLoad(v string) (int, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return loadModes["medium"], nil
	}
	if n, ok := loadModes[v]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(strings.TrimSuffix(v, "%"))
	if err != nil || n < 1 || n > 100 {
		return 0, fmt.Errorf("clientconfig: LOAD must be easy, medium, hard or a percentage 1..100, got %q", v)
	}
	return n, nil
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
