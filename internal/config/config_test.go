package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeClusters writes a clusters YAML file to a temp dir and returns its path.
func writeClusters(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clusters.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const twoClusters = `clusters:
  - name: prod
    url: http://kato.prod.svc:8080
    label: Production
  - name: staging
    url: http://kato.staging.svc:8080
`

func TestLoadDefaults(t *testing.T) {
	t.Setenv("LARK_APP_ID", "cli_x")
	t.Setenv("LARK_APP_SECRET", "secret_x")
	t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, twoClusters))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(cfg.Clusters) != 2 {
		t.Fatalf("clusters = %+v", cfg.Clusters)
	}
	if cfg.Clusters[0].Name != "prod" || cfg.Clusters[0].URL != "http://kato.prod.svc:8080" || cfg.Clusters[0].Label != "Production" {
		t.Errorf("cluster[0] = %+v", cfg.Clusters[0])
	}
	if cfg.Clusters[1].Name != "staging" || cfg.Clusters[1].Label != "" {
		t.Errorf("cluster[1] = %+v", cfg.Clusters[1])
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
		t.Errorf("larkBaseURL = %q", cfg.LarkBaseURL)
	}
}

func TestLoadMaxConcurrentRuns(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")
	t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, twoClusters))

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

func TestLoadMissingRequired(t *testing.T) {
	t.Setenv("LARK_APP_ID", "")
	t.Setenv("LARK_APP_SECRET", "")
	t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, twoClusters))
	if _, err := Load(); err == nil {
		t.Fatal("expected error when LARK_APP_ID/SECRET unset")
	}
}

func TestLoadBadTimeout(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")
	t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, twoClusters))
	t.Setenv("KATO_RUN_TIMEOUT", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("expected error on bad duration")
	}
}

func TestLoadClustersValidation(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")

	cases := []struct {
		name string
		body string
	}{
		{"empty list", "clusters: []\n"},
		{"missing url", "clusters:\n  - name: prod\n"},
		{"empty name", "clusters:\n  - name: \"\"\n    url: http://x\n"},
		{"duplicate name", "clusters:\n  - name: prod\n    url: http://a\n  - name: prod\n    url: http://b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KATO_CLUSTERS_FILE", writeClusters(t, tc.body))
			if _, err := Load(); err == nil {
				t.Errorf("%s: expected a validation error", tc.name)
			}
		})
	}
}

func TestLoadClustersFileMissing(t *testing.T) {
	t.Setenv("LARK_APP_ID", "id")
	t.Setenv("LARK_APP_SECRET", "sec")
	t.Setenv("KATO_CLUSTERS_FILE", filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if _, err := Load(); err == nil {
		t.Fatal("expected an error when the clusters file is missing")
	}
}
