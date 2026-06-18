// Package config loads kato-bot configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ClusterConfig is one configured kato backend (name → URL, with an optional label).
type ClusterConfig struct {
	Name  string
	URL   string
	Label string
}

// Config is the resolved runtime configuration.
type Config struct {
	LarkAppID         string
	LarkAppSecret     string
	Clusters          []ClusterConfig
	KatoRunTimeout    time.Duration
	HealthAddr        string
	LogLevel          string
	MaxConcurrentRuns int
	LarkBaseURL       string
}

// Load reads config from env, applying defaults. LARK_APP_ID and LARK_APP_SECRET are
// required; the clusters file (KATO_CLUSTERS_FILE, default /etc/kato-bot/clusters.yaml)
// must exist, parse, and list at least one valid cluster. KATO_RUN_TIMEOUT must parse as a
// Go duration and MAX_CONCURRENT_RUNS as a positive int when set.
func Load() (Config, error) {
	cfg := Config{
		LarkAppID:         os.Getenv("LARK_APP_ID"),
		LarkAppSecret:     os.Getenv("LARK_APP_SECRET"),
		HealthAddr:        envOr("HEALTH_ADDR", ":8080"),
		LogLevel:          envOr("LOG_LEVEL", "info"),
		KatoRunTimeout:    360 * time.Second,
		MaxConcurrentRuns: 4,
		// Open-platform base URL. Lark international: https://open.larksuite.com;
		// Feishu (China): https://open.feishu.cn.
		LarkBaseURL: envOr("LARK_BASE_URL", "https://open.larksuite.com"),
	}
	if strings.TrimSpace(cfg.LarkAppID) == "" || strings.TrimSpace(cfg.LarkAppSecret) == "" {
		return Config{}, fmt.Errorf("LARK_APP_ID and LARK_APP_SECRET are required")
	}

	clusters, err := loadClusters(envOr("KATO_CLUSTERS_FILE", "/etc/kato-bot/clusters.yaml"))
	if err != nil {
		return Config{}, err
	}
	cfg.Clusters = clusters

	if v := os.Getenv("KATO_RUN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("KATO_RUN_TIMEOUT: %w", err)
		}
		cfg.KatoRunTimeout = d
	}
	if v := os.Getenv("MAX_CONCURRENT_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return Config{}, fmt.Errorf("MAX_CONCURRENT_RUNS: must be a positive integer, got %q", v)
		}
		cfg.MaxConcurrentRuns = n
	}
	return cfg, nil
}

// clustersFile mirrors the YAML shape of the clusters config file.
type clustersFile struct {
	Clusters []struct {
		Name  string `yaml:"name"`
		URL   string `yaml:"url"`
		Label string `yaml:"label"`
	} `yaml:"clusters"`
}

// loadClusters reads and validates the clusters YAML file: it must contain at least one
// cluster, each with a unique non-empty name and a non-empty url.
func loadClusters(path string) ([]ClusterConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read clusters file %s: %w", path, err)
	}
	var f clustersFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse clusters file %s: %w", path, err)
	}
	if len(f.Clusters) == 0 {
		return nil, fmt.Errorf("clusters file %s: at least one cluster is required", path)
	}
	seen := make(map[string]bool, len(f.Clusters))
	out := make([]ClusterConfig, 0, len(f.Clusters))
	for i, c := range f.Clusters {
		name := strings.TrimSpace(c.Name)
		url := strings.TrimSpace(c.URL)
		if name == "" {
			return nil, fmt.Errorf("clusters file %s: cluster #%d has an empty name", path, i+1)
		}
		if url == "" {
			return nil, fmt.Errorf("clusters file %s: cluster %q has an empty url", path, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("clusters file %s: duplicate cluster name %q", path, name)
		}
		seen[name] = true
		out = append(out, ClusterConfig{Name: name, URL: url, Label: strings.TrimSpace(c.Label)})
	}
	return out, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
