// nabu is the single entrypoint for all of Nabu's server processes; --mode
// selects what this instance runs (only the API server exists so far — worker
// mode arrives with background jobs, per ARCHITECTURE.md).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	stdhttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/floatinginbits/nabu/internal/config"
	nabuhttp "github.com/floatinginbits/nabu/internal/http"
)

func main() {
	if err := run(); err != nil {
		slog.Error("nabu exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "api", `run mode (only "api" is implemented)`)
	flag.Parse()
	if *mode != "api" {
		return fmt.Errorf("unknown mode %q: only \"api\" is implemented", *mode)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	srv := &stdhttp.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           nabuhttp.NewHandler(log),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Info("api server listening", slog.Int("port", cfg.Port))
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}
