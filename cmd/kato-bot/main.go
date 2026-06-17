// Command kato-bot runs the Lark chat adapter for kato.
package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/zufardhiyaulhaq/kato-bot/internal/config"
	"github.com/zufardhiyaulhaq/kato-bot/internal/core"
	"github.com/zufardhiyaulhaq/kato-bot/internal/kato"
	"github.com/zufardhiyaulhaq/kato-bot/internal/platform/lark"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	katoClient := kato.New(cfg.KatoBaseURL, cfg.KatoRunTimeout)
	renderer := lark.NewSender(cfg.LarkAppID, cfg.LarkAppSecret, cfg.LarkBaseURL)
	c := &core.Core{Kato: katoClient, R: renderer}

	adapter := &lark.Adapter{
		AppID:         cfg.LarkAppID,
		AppSecret:     cfg.LarkAppSecret,
		Core:          c,
		R:             renderer,
		RunTimeout:    cfg.KatoRunTimeout,
		LogLevel:      cfg.LogLevel,
		MaxConcurrent: cfg.MaxConcurrentRuns,
		BaseURL:       cfg.LarkBaseURL,
	}

	// Health server for k8s probes (no inbound app traffic; this is liveness only).
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
		log.Printf("health server on %s", cfg.HealthAddr)
		// A bind failure (e.g. HEALTH_ADDR clashes with a local port-forward) is fatal:
		// otherwise the bot runs but k8s probes fail, killing the pod with no clear cause.
		if err := http.ListenAndServe(cfg.HealthAddr, mux); err != nil {
			log.Fatalf("health server on %s: %v", cfg.HealthAddr, err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("kato-bot connecting to Lark; kato at %s (run timeout %s, domain %s)",
		cfg.KatoBaseURL, cfg.KatoRunTimeout, cfg.LarkBaseURL)
	if err := adapter.Start(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("lark adapter: %v", err)
	}
	log.Print("kato-bot shut down")
}
