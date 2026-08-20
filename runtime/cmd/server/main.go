package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/quanyejun/quanyejun/runtime/internal/agent"
	"github.com/quanyejun/quanyejun/runtime/internal/domain"
	"github.com/quanyejun/quanyejun/runtime/internal/environment"
	"github.com/quanyejun/quanyejun/runtime/internal/eventhub"
	"github.com/quanyejun/quanyejun/runtime/internal/httpapi"
	"github.com/quanyejun/quanyejun/runtime/internal/llm"
	runtimemcp "github.com/quanyejun/quanyejun/runtime/internal/mcp"
	runtimeskills "github.com/quanyejun/quanyejun/runtime/internal/skills"
	"github.com/quanyejun/quanyejun/runtime/internal/store"
	"github.com/quanyejun/quanyejun/runtime/internal/tasks"
	runtimetools "github.com/quanyejun/quanyejun/runtime/internal/tools"
)

func main() {
	ctx := context.Background()
	dataStore := buildStore(ctx)
	defer dataStore.Close()
	hub := eventhub.New()
	executionEnvironment := buildEnvironment(ctx)
	provider := buildProvider(ctx, executionEnvironment)
	taskRunner := tasks.NewRunner(dataStore, hub)
	origins := strings.Split(env("ALLOWED_ORIGINS", "http://localhost:3000"), ",")
	displayName := domain.NormalizeDisplayName(os.Getenv("IDENTITY_DISPLAY_NAME"))
	skillRegistry := runtimeskills.NewRegistry(buildSkillsRoot(ctx, executionEnvironment))
	toolRegistry := runtimetools.NewRegistry(dataStore, executionEnvironment, skillRegistry)
	mcpRegistry, mcpRefreshInterval := buildMCPRegistry(ctx, executionEnvironment)
	toolRegistry.AttachMCP(mcpRegistry)
	mcpContext, stopMCP := context.WithCancel(ctx)
	defer stopMCP()
	go watchMCPRegistry(mcpContext, mcpRegistry, mcpRefreshInterval)
	harnessConfig := agent.ConfigFromEnvironment(os.Getenv)
	agentRunner := agent.NewRunnerWithConfig(dataStore, hub, provider, toolRegistry, displayName, harnessConfig)
	if err := agentRunner.Recover(ctx); err != nil {
		slog.Error("agent run recovery failed", "error", err)
		os.Exit(1)
	}
	api := httpapi.New(dataStore, hub, provider, agentRunner, taskRunner, origins, displayName, executionEnvironment)
	server := &http.Server{Addr: ":" + env("PORT", "8080"), Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 75 * time.Second}
	go func() {
		info := provider.Info()
		environmentInfo, _ := executionEnvironment.Inspect(ctx)
		slog.Info("QuanYejun runtime listening", "address", server.Addr, "displayName", displayName, "environment", environmentInfo.Kind, "profile", environmentInfo.Profile, "llmProvider", info.ID, "llmModel", info.Model, "providerSource", info.Source, "skillsRoot", skillRegistry.Root(), "mcpTools", len(mcpRegistry.Definitions()), "maxTurns", harnessConfig.MaxTurns, "maxToolCalls", harnessConfig.MaxToolCalls, "runTimeout", harnessConfig.RunTimeout)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("runtime stopped", "error", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	stopMCP()
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
func buildEnvironment(ctx context.Context) environment.Environment {
	executionEnvironment := environment.NewLocal(environment.ConfigFromEnvironment(os.Getenv))
	if err := executionEnvironment.Prepare(ctx); err != nil {
		slog.Error("execution environment initialization failed", "error", err)
		os.Exit(1)
	}
	return executionEnvironment
}

func buildProvider(ctx context.Context, _ environment.Environment) llm.Provider {
	provider, err := llm.DetectProvider(ctx, llm.DetectOptions{})
	if err != nil {
		slog.Error("LLM provider detection failed", "error", err)
		os.Exit(1)
	}
	return provider
}

func buildSkillsRoot(ctx context.Context, executionEnvironment environment.Environment) string {
	if configured := strings.TrimSpace(os.Getenv("QUANYEJUN_SKILLS_DIR")); configured != "" {
		return configured
	}
	info, err := executionEnvironment.Inspect(ctx)
	if err != nil {
		slog.Error("skills root resolution failed", "error", err)
		os.Exit(1)
	}
	return filepath.Join(info.Paths.Config, "skills")
}

func buildMCPRegistry(ctx context.Context, executionEnvironment environment.Environment) (*runtimemcp.Registry, time.Duration) {
	info, err := executionEnvironment.Inspect(ctx)
	if err != nil {
		slog.Error("MCP configuration resolution failed", "error", err)
		os.Exit(1)
	}
	serversFile := env("QUANYEJUN_MCP_SERVERS_FILE", filepath.Join(info.Paths.Config, "mcp-servers.json"))
	authorityFile := env("QUANYEJUN_MCP_AUTHORITY_FILE", filepath.Join(info.Paths.Config, "mcp-authority.json"))
	registry := runtimemcp.NewRegistry(runtimemcp.Options{
		ServersFile: serversFile, AuthorityFile: authorityFile, IdentityID: domain.IdentityID, Getenv: os.Getenv,
	})
	refreshContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := registry.Refresh(refreshContext); err != nil {
		slog.Warn("MCP registry loaded with no access to failed servers", "error", err)
	}
	seconds := 30
	if value := strings.TrimSpace(os.Getenv("QUANYEJUN_MCP_REFRESH_SECONDS")); value != "" {
		if parsed, parseErr := time.ParseDuration(value + "s"); parseErr == nil && parsed >= 5*time.Second && parsed <= time.Hour {
			seconds = int(parsed / time.Second)
		} else {
			slog.Warn("invalid QUANYEJUN_MCP_REFRESH_SECONDS; using default", "value", value)
		}
	}
	return registry, time.Duration(seconds) * time.Second
}

func watchMCPRegistry(ctx context.Context, registry *runtimemcp.Registry, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshContext, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := registry.ReloadIfChanged(refreshContext)
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Warn("MCP registry refresh failed closed", "error", err)
			}
		}
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
