// SPDX-License-Identifier: AGPL-3.0-only

// Command nusa runs the API server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nusa-app/nusa/internal/api"
	"github.com/nusa-app/nusa/internal/brand"
	"github.com/nusa-app/nusa/internal/config"
	"github.com/nusa-app/nusa/internal/store"
)

func main() {
	if err := run(); err != nil {
		// Configuration errors are multi-line and meant for a human reading a
		// terminal or a crash log, so they go to stderr in plain text rather
		// than through the structured logger.
		fmt.Fprintf(os.Stderr, "%s: %v\n", brand.Slug, err)
		os.Exit(1)
	}
}

const usage = `usage: nusa <command>

commands:
  serve                 run the HTTP server (default)
  migrate up            apply every pending migration
  migrate down [n|all]  roll back n migrations, default 1
  migrate version       report the applied schema version

Configuration comes from the environment; see .env.example.`

func run() error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	logger := newLogger(cfg)

	command, args := "serve", os.Args[1:]
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}

	switch command {
	case "serve":
		return serve(cfg, logger)
	case "migrate":
		return runMigrate(cfg, logger, args)
	case "help", "-h", "--help":
		fmt.Println(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usage)
	}
}

// runMigrate applies or rolls back schema migrations. Migrations are a
// separate command rather than something serve does on startup, so that
// rolling out a new binary and changing the schema stay distinct, deliberate
// steps.
func runMigrate(cfg *config.Config, logger *slog.Logger, args []string) error {
	action := "up"
	if len(args) > 0 {
		action, args = args[0], args[1:]
	}

	switch action {
	case "up":
		switch err := store.MigrateUp(cfg.DatabaseURL); {
		case errors.Is(err, store.ErrNoChange):
			logger.Info("migrations already up to date")
		case err != nil:
			return err
		default:
			logger.Info("migrations applied")
		}

	case "down":
		steps := 1
		if len(args) > 0 {
			if args[0] == "all" {
				steps = 0
			} else {
				n, err := strconv.Atoi(args[0])
				if err != nil || n < 1 {
					return fmt.Errorf("migrate down: %q is not a positive number of steps (or \"all\")", args[0])
				}
				steps = n
			}
		}
		switch err := store.MigrateDown(cfg.DatabaseURL, steps); {
		case errors.Is(err, store.ErrNoChange):
			logger.Info("nothing to roll back")
		case err != nil:
			return err
		default:
			logger.Info("migrations rolled back", slog.Int("steps", steps))
		}

	case "version":
		version, dirty, err := store.MigrateVersion(cfg.DatabaseURL)
		if err != nil {
			return err
		}
		logger.Info("schema version", slog.Uint64("version", uint64(version)), slog.Bool("dirty", dirty))
		if dirty {
			return errors.New("database is dirty: a migration stopped part-way and needs manual repair")
		}

	default:
		return fmt.Errorf("unknown migrate action %q (want: up, down, version)", action)
	}

	return nil
}

func serve(cfg *config.Config, logger *slog.Logger) error {
	logger.Info("starting",
		slog.String("service", brand.Name),
		slog.String("env", string(cfg.Env)),
		slog.String("addr", cfg.HTTPAddr),
		slog.String("database", cfg.RedactedDatabaseURL()),
	)

	// Stop accepting work as soon as the platform asks us to. Compose, systemd
	// and Kubernetes all send SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	defer cancelConnect()

	db, err := store.Open(connectCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: api.NewRouter(api.Deps{
			Logger:        logger,
			Database:      db,
			HealthTimeout: cfg.HealthTimeout,
			WebDir:        cfg.WebDir,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve http: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down", slog.Duration("grace_period", cfg.ShutdownTimeout))
	}

	// A fresh context: the one above is already cancelled by the signal, and
	// in-flight requests still deserve their grace period.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down http server: %w", err)
	}

	logger.Info("stopped")
	return nil
}

// newLogger favours readability in development and machine parsing everywhere
// else.
func newLogger(cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.IsDevelopment() {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
