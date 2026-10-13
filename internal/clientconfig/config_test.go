package clientconfig

import "testing"

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SERVER_URL", "https://freehire.me")
	t.Setenv("API_KEY", "fhk_test")
	t.Setenv("LOCAL_TEI_URL", "http://127.0.0.1:8080/embed")
}

func TestLoad_MissingServerURL_ReturnsError(t *testing.T) {
	t.Setenv("API_KEY", "fhk_test")
	t.Setenv("LOCAL_TEI_URL", "http://127.0.0.1:8080/embed")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error when SERVER_URL is unset, got nil")
	}
}

func TestLoad_MissingAPIKey_ReturnsError(t *testing.T) {
	t.Setenv("SERVER_URL", "https://freehire.me")
	t.Setenv("LOCAL_TEI_URL", "http://127.0.0.1:8080/embed")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error when API_KEY is unset, got nil")
	}
}

func TestLoad_MissingLocalTEIURL_ReturnsError(t *testing.T) {
	t.Setenv("SERVER_URL", "https://freehire.me")
	t.Setenv("API_KEY", "fhk_test")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error when LOCAL_TEI_URL is unset, got nil")
	}
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.BatchSize != 10 {
		t.Errorf("expected default BatchSize 10, got %d", cfg.BatchSize)
	}
	if cfg.PollInterval.String() != "30s" {
		t.Errorf("expected default PollInterval 30s, got %s", cfg.PollInterval)
	}
}

func TestLoad_ZeroBatchSize_ReturnsError(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("BATCH_SIZE", "0")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error for BATCH_SIZE=0, got nil")
	}
}
