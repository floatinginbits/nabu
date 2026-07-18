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

	"github.com/floatinginbits/nabu/internal/auth"
	"github.com/floatinginbits/nabu/internal/config"
	nabuhttp "github.com/floatinginbits/nabu/internal/http"
	"github.com/floatinginbits/nabu/internal/project"
	"github.com/floatinginbits/nabu/internal/store"
	"github.com/floatinginbits/nabu/internal/task"
	"github.com/floatinginbits/nabu/internal/user"
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

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), time.Minute)
	defer cancelStartup()
	if err := store.Migrate(startupCtx, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migrating database: %w", err)
	}
	log.Info("database migrations applied")

	pool, err := store.NewPool(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	users := user.NewService(user.NewPostgresRepository(pool))
	if cfg.InitialAdminEmail != "" {
		created, err := users.EnsureInitialAdmin(startupCtx, cfg.InitialAdminEmail, cfg.InitialAdminPassword)
		if err != nil {
			return fmt.Errorf("ensuring initial admin: %w", err)
		}
		if created {
			log.Info("initial admin created", slog.String("email", cfg.InitialAdminEmail))
		}
	}

	projectRepo := project.NewPostgresRepository(pool)
	// v1 is single-org: the org is a schema-enforced singleton, resolved once
	// here so every session is scoped to it (see internal/http.Deps.OrgID).
	orgID, err := projectRepo.SingletonOrgID(startupCtx)
	if err != nil {
		return fmt.Errorf("resolving organization: %w", err)
	}
	projects := project.NewService(projectRepo)

	tasks := task.NewService(task.NewPostgresRepository(pool), projects)
	authSvc := auth.NewService(users, auth.NewPostgresRefreshRepository(pool), []byte(cfg.AuthSecret), log)

	handler, err := nabuhttp.NewHandler(nabuhttp.Deps{
		Log:          log,
		Tasks:        tasks,
		Projects:     projects,
		Auth:         authSvc,
		Users:        users,
		CookieSecure: cfg.CookieSecure,
		OrgID:        orgID,
	})
	if err != nil {
		return fmt.Errorf("building http handler: %w", err)
	}

	srv := &stdhttp.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
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
