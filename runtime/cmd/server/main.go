package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/httpapi"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	"github.com/quanyejun/quanyejun/runtime/internal/tasks"
)

func main() {
	ctx := context.Background()
	dataStore := buildStore(ctx)
	defer dataStore.Close()
	hub := eventhub.New()
	provider := buildProvider(ctx)
	runner := tasks.NewRunner(dataStore, hub)
	origins := strings.Split(env("ALLOWED_ORIGINS", "http://localhost:3000"), ",")
	api := httpapi.New(dataStore, hub, provider, runner, origins)
	server := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 75 * time.Second}
	go func() {
		info := provider.Info()
		slog.Info("QuanYejun runtime listening", "address", server.Addr, "llmProvider", info.ID, "llmModel", info.Model, "providerSource", info.Source)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("runtime stopped", "error", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
	}
}

func buildStore(ctx context.Context) store.Store {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		slog.Warn("DATABASE_URL is not set; using non-persistent preview storage")
		return store.NewMemoryStore()
	}
	postgres, err := store.NewPostgresStore(ctx, databaseURL)
	if err != nil {
		slog.Error("postgres initialization failed", "error", err)
		os.Exit(1)
	}
	slog.Info("persistent storage ready", "driver", "postgres")
	return postgres
}
func buildProvider(ctx context.Context) llm.Provider {
	provider, err := llm.DetectProvider(ctx, llm.DetectOptions{})
	if err != nil {
		slog.Error("LLM provider detection failed", "error", err)
		os.Exit(1)
	}
	return provider
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
