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

func TestLoad_Defaults_LocalEmbedChunkSizeIsZero(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LocalEmbedChunkSize != 0 {
		t.Errorf("expected default LocalEmbedChunkSize 0 (no chunking), got %d", cfg.LocalEmbedChunkSize)
	}
}

func TestLoad_LocalEmbedChunkSize_ParsesSetValue(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LOCAL_EMBED_CHUNK_SIZE", "20")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LocalEmbedChunkSize != 20 {
		t.Errorf("expected LocalEmbedChunkSize 20, got %d", cfg.LocalEmbedChunkSize)
	}
}

func TestLoad_LocalEmbedChunkSize_NotANumber_ReturnsError(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LOCAL_EMBED_CHUNK_SIZE", "lots")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error for a non-numeric LOCAL_EMBED_CHUNK_SIZE, got nil")
	}
}

func TestLoad_Defaults_LocalEmbedChunkSizeMax(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LocalEmbedChunkSizeMax != 16 {
		t.Errorf("expected default LocalEmbedChunkSizeMax 16, got %d", cfg.LocalEmbedChunkSizeMax)
	}
}

func TestLoad_LocalEmbedChunkSizeMax_ParsesSetValue(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LOCAL_EMBED_CHUNK_SIZE_MAX", "16")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LocalEmbedChunkSizeMax != 16 {
		t.Errorf("expected LocalEmbedChunkSizeMax 16, got %d", cfg.LocalEmbedChunkSizeMax)
	}
}

func TestLoad_LocalEmbedChunkSizeMax_NotPositive_ReturnsError(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LOCAL_EMBED_CHUNK_SIZE_MAX", "0")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error for LOCAL_EMBED_CHUNK_SIZE_MAX=0, got nil")
	}
}

func TestLoad_LocalEmbedChunkSize_Negative_ReturnsError(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LOCAL_EMBED_CHUNK_SIZE", "-1")

	_, err := Load()

	if err == nil {
		t.Fatal("expected an error for a negative LOCAL_EMBED_CHUNK_SIZE, got nil")
	}
}

func TestLoad_LoadDefaultsToMedium(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LoadPercent != 50 {
		t.Errorf("LoadPercent = %d, want 50 (medium) when LOAD is unset", cfg.LoadPercent)
	}
}

func TestLoad_LoadAcceptsModesAndPercentages(t *testing.T) {
	for in, want := range map[string]int{"easy": 25, "medium": 50, "hard": 100, "HARD": 100, "1": 1, "70": 70, "100": 100, "80%": 80} {
		setRequiredEnv(t)
		t.Setenv("LOAD", in)

		cfg, err := Load()
		if err != nil {
			t.Errorf("LOAD=%q: Load: %v", in, err)
			continue
		}
		if cfg.LoadPercent != want {
			t.Errorf("LOAD=%q: LoadPercent = %d, want %d", in, cfg.LoadPercent, want)
		}
	}
}

func TestLoad_LoadRejectsAnythingElse(t *testing.T) {
	for _, in := range []string{"0", "101", "-5", "max", "fast"} {
		setRequiredEnv(t)
		t.Setenv("LOAD", in)

		if _, err := Load(); err == nil {
			t.Errorf("LOAD=%q: expected an error, got nil", in)
		}
	}
}
