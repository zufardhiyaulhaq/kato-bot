// Package config loads kato-bot configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the resolved runtime configuration.
type Config struct {
	LarkAppID         string
	LarkAppSecret     string
	KatoBaseURL       string
	KatoRunTimeout    time.Duration
	HealthAddr        string
	LogLevel          string
	MaxConcurrentRuns int
	LarkBaseURL       string
}

// Load reads config from env, applying defaults. LARK_APP_ID and LARK_APP_SECRET are
// required; KATO_RUN_TIMEOUT must parse as a Go duration and MAX_CONCURRENT_RUNS as a
// positive int when set.
func Load() (Config, error) {
	cfg := Config{
		LarkAppID:         os.Getenv("LARK_APP_ID"),
		LarkAppSecret:     os.Getenv("LARK_APP_SECRET"),
		KatoBaseURL:       envOr("KATO_BASE_URL", "http://kato.kato.svc:8080"),
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
