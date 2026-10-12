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

func TestLoad_ZeroLeaseBatchSizeMax_ReturnsError(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LEASE_BATCH_SIZE_MAX", "0")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error for LEASE_BATCH_SIZE_MAX=0, got nil (a zero max would make every lease request fail)")
	}
}

func TestLoad_NegativeLeaseBatchSizeMax_ReturnsError(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LEASE_BATCH_SIZE_MAX", "-5")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error for a negative LEASE_BATCH_SIZE_MAX, got nil")
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
	if cfg.TrustedTEIAPIKey != "" {
		t.Errorf("TrustedTEIAPIKey = %q, want empty when unset (freehire's own TEI needs no auth)", cfg.TrustedTEIAPIKey)
	}
}

func TestLoad_TrustedTEIAPIKeySet_IsPopulated(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TRUSTED_TEI_API_KEY", "some-secret")

	cfg, err := Load()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TrustedTEIAPIKey != "some-secret" {
		t.Errorf("TrustedTEIAPIKey = %q, want %q", cfg.TrustedTEIAPIKey, "some-secret")
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
	if cfg.MaxLeaseBatchSize != 50 {
		t.Errorf("MaxLeaseBatchSize default = %d, want 50", cfg.MaxLeaseBatchSize)
	}
}

func TestLoad_OptionalVarsOverrideWhenSet(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LISTEN_ADDR", ":9090")
	t.Setenv("LEASE_DURATION", "2m")
	t.Setenv("FULL_CHECK_THRESHOLD", "5")
	t.Setenv("CHECK_DIVISOR_STAGE1", "7")
	t.Setenv("CHECK_DIVISOR_STAGE2", "40")

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
	if cfg.FullCheckThreshold != 5 {
		t.Errorf("FullCheckThreshold = %d, want 5", cfg.FullCheckThreshold)
	}
	if cfg.CheckDivisorStage1 != 7 {
		t.Errorf("CheckDivisorStage1 = %d, want 7", cfg.CheckDivisorStage1)
	}
	if cfg.CheckDivisorStage2 != 40 {
		t.Errorf("CheckDivisorStage2 = %d, want 40", cfg.CheckDivisorStage2)
	}
}

func TestLoad_InvalidCheckDivisorStage1_ReturnsError(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CHECK_DIVISOR_STAGE1", "not-a-number")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error for a non-numeric CHECK_DIVISOR_STAGE1, got nil")
	}
}
