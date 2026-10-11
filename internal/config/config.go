// Package config loads this service's settings from environment variables.
// Required variables fail loudly when unset — this service holds credentials
// to freehire's production database, and a silent empty default here is not
// acceptable.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL        string
	TrustedTEIURL      string
	ListenAddr         string
	LeaseDuration      time.Duration
	FullCheckThreshold int
	CheckDivisorStage1 int
	CheckDivisorStage2 int
}

func Load() (Config, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return Config{}, fmt.Errorf("config: DATABASE_URL is required")
	}
	trustedTEIURL := os.Getenv("TRUSTED_TEI_URL")
	if trustedTEIURL == "" {
		return Config{}, fmt.Errorf("config: TRUSTED_TEI_URL is required")
	}

	leaseDuration := 5 * time.Minute
	if v := os.Getenv("LEASE_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: LEASE_DURATION invalid: %w", err)
		}
		leaseDuration = d
	}

	listenAddr := ":8080"
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		listenAddr = v
	}

	fullCheckThreshold, err := intEnvOrDefault("FULL_CHECK_THRESHOLD", 20)
	if err != nil {
		return Config{}, err
	}
	checkDivisorStage1, err := intEnvOrDefault("CHECK_DIVISOR_STAGE1", 10)
	if err != nil {
		return Config{}, err
	}
	checkDivisorStage2, err := intEnvOrDefault("CHECK_DIVISOR_STAGE2", 30)
	if err != nil {
		return Config{}, err
	}

	return Config{
		DatabaseURL:        databaseURL,
		TrustedTEIURL:      trustedTEIURL,
		ListenAddr:         listenAddr,
		LeaseDuration:      leaseDuration,
		FullCheckThreshold: fullCheckThreshold,
		CheckDivisorStage1: checkDivisorStage1,
		CheckDivisorStage2: checkDivisorStage2,
	}, nil
}

func intEnvOrDefault(name string, fallback int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s invalid: %w", name, err)
	}
	return n, nil
}
