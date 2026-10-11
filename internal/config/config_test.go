package config

import (
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/hire")
	t.Setenv("TRUSTED_TEI_URL", "http://127.0.0.1:8090/embed")
}

func TestLoad_MissingDatabaseURL_ReturnsError(t *testing.T) {
	t.Setenv("TRUSTED_TEI_URL", "http://127.0.0.1:8090/embed")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error when DATABASE_URL is unset, got nil")
	}
}

func TestLoad_MissingTrustedTEIURL_ReturnsError(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/hire")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error when TRUSTED_TEI_URL is unset, got nil")
	}
}

func TestLoad_RequiredVarsPresent_PopulatesFields(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseURL != "postgres://user:pass@localhost:5432/hire" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.TrustedTEIURL != "http://127.0.0.1:8090/embed" {
		t.Errorf("TrustedTEIURL = %q", cfg.TrustedTEIURL)
	}
}

func TestLoad_OptionalVarsDefaultWhenUnset(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr default = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.LeaseDuration != 5*time.Minute {
		t.Errorf("LeaseDuration default = %v, want 5m", cfg.LeaseDuration)
	}
	if cfg.FullCheckThreshold != 20 {
		t.Errorf("FullCheckThreshold default = %d, want 20", cfg.FullCheckThreshold)
	}
	if cfg.CheckDivisorStage1 != 10 {
		t.Errorf("CheckDivisorStage1 default = %d, want 10", cfg.CheckDivisorStage1)
	}
	if cfg.CheckDivisorStage2 != 30 {
		t.Errorf("CheckDivisorStage2 default = %d, want 30", cfg.CheckDivisorStage2)
	}
}

func TestLoad_OptionalVarsOverrideWhenSet(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LISTEN_ADDR", ":9090")
	t.Setenv("LEASE_DURATION", "2m")

	cfg, err := Load()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want :9090", cfg.ListenAddr)
	}
	if cfg.LeaseDuration != 2*time.Minute {
		t.Errorf("LeaseDuration = %v, want 2m", cfg.LeaseDuration)
	}
}
