package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/xiilab/isaac-launchable/gateway-go/internal/config"
	"github.com/xiilab/isaac-launchable/gateway-go/internal/proxy"
	"github.com/xiilab/isaac-launchable/gateway-go/internal/session"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	factory := session.Factory(session.Config{
		TurnURI:        cfg.TurnURI,
		TurnUsername:   cfg.TurnUsername,
		TurnCredential: cfg.TurnCredential,
	})

	mux := http.NewServeMux()
	mux.Handle("/", proxy.NewHandler(cfg.KitSignalURL, factory))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("[gateway-go] listening on %s → upstream %s", cfg.ListenAddr, cfg.KitSignalURL)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		log.Fatalf("listen: %v", err)
	case <-ctx.Done():
		log.Println("[gateway-go] shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
}
