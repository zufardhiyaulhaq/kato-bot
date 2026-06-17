package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("LARK_APP_ID", "cli_x")
	t.Setenv("LARK_APP_SECRET", "secret_x")
	// KATO_BASE_URL / KATO_RUN_TIMEOUT unset → defaults

	cfg, err := Load()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cfg.KatoBaseURL != "http://kato.kato.svc:8080" {
		t.Errorf("base = %q", cfg.KatoBaseURL)
	}
	if cfg.KatoRunTimeout != 360*time.Second {
		t.Errorf("timeout = %v", cfg.KatoRunTimeout)
	}
	if cfg.HealthAddr != ":8080" {
		t.Errorf("health = %q", cfg.HealthAddr)
	}
	if cfg.MaxConcurrentRuns != 4 {
		t.Errorf("maxConcurrentRuns = %d, want 4", cfg.MaxConcurrentRuns)
	}
	if cfg.LarkBaseURL != "https://open.larksuite.com" {
		t.Errorf("larkBaseURL = %q, want https://open.larksuite.com", cfg.LarkBaseURL)
	}
}

func TestLoadMaxConcurrentRuns(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")

	t.Setenv("MAX_CONCURRENT_RUNS", "8")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cfg.MaxConcurrentRuns != 8 {
		t.Errorf("maxConcurrentRuns = %d, want 8", cfg.MaxConcurrentRuns)
	}

	for _, bad := range []string{"0", "-1", "two"} {
		t.Setenv("MAX_CONCURRENT_RUNS", bad)
		if _, err := Load(); err == nil {
			t.Errorf("MAX_CONCURRENT_RUNS=%q should error", bad)
		}
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")
	t.Setenv("KATO_BASE_URL", "http://localhost:9999/")
	t.Setenv("KATO_RUN_TIMEOUT", "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cfg.KatoBaseURL != "http://localhost:9999/" || cfg.KatoRunTimeout != 30*time.Second {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	t.Setenv("LARK_APP_ID", "")
	t.Setenv("LARK_APP_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when LARK_APP_ID/SECRET unset")
	}
}

func TestLoadBadTimeout(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")
	t.Setenv("KATO_RUN_TIMEOUT", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("expected error on bad duration")
	}
}
