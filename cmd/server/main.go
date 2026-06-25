package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aravind/vault-spindle/internal/api"
	"github.com/aravind/vault-spindle/internal/migrate"
	"github.com/aravind/vault-spindle/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dsn := envOr("DATABASE_URL", "postgres://vault:vault@localhost:5432/vault?sslmode=disable")
	addr := envOr("LISTEN_ADDR", ":8080")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		logger.Error("connect database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := runMigrations(ctx, pool); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}

	st := store.New(pool)
	srv := api.NewServer(st, logger)

	go startIdempotencyPurge(st, logger)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
	}
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate.Apply(ctx, pool, "migrations")
}

func startIdempotencyPurge(st *store.Store, logger *slog.Logger) {
	retention := 7 * 24 * time.Hour
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		n, err := st.PurgeOldIdempotency(context.Background(), retention)
		if err != nil {
			logger.Error("idempotency purge failed", "err", err)
			continue
		}
		if n > 0 {
			logger.Info("purged idempotency records", "count", n)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
