package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/leofelipe/contexta/internal/config"
	"github.com/leofelipe/contexta/internal/ingestion"
	"github.com/leofelipe/contexta/internal/providers/whatsapp/uazapi"
	"github.com/leofelipe/contexta/internal/storage/postgres"
	"github.com/leofelipe/contexta/internal/transport/httpapi"
	"github.com/leofelipe/contexta/internal/transport/mcpserver"
)

func Run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "serve":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load configuration: %w", err)
		}
		return serve(ctx, cfg)
	case "migrate":
		databaseURL := os.Getenv("DATABASE_URL")
		if databaseURL == "" {
			return errors.New("DATABASE_URL is required")
		}
		return postgres.Migrate(ctx, databaseURL)
	case "uazapi-status":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load configuration: %w", err)
		}
		status, err := uazapi.NewClient(cfg.UAZAPI.BaseURL, cfg.UAZAPI.Token).Status(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(status)
	case "uazapi-configure-webhook":
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load configuration: %w", err)
		}
		if cfg.UAZAPI.WebhookPublicURL == "" {
			return errors.New("UAZAPI_WEBHOOK_PUBLIC_URL is required")
		}
		callbackURL := strings.TrimRight(cfg.UAZAPI.WebhookPublicURL, "/") + "/" + cfg.UAZAPI.WebhookSecret
		if err := uazapi.NewClient(cfg.UAZAPI.BaseURL, cfg.UAZAPI.Token).ConfigureWebhook(ctx, callbackURL); err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, "UAZAPI webhook configured")
		return err
	default:
		return usageError()
	}
}

func serve(ctx context.Context, cfg config.Config) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	store, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	ingestionService := ingestion.NewService(store)
	apiHandler := httpapi.New(httpapi.Options{
		Store: store, Ingestion: ingestionService, APIToken: cfg.APIToken,
		WebhookSecret: cfg.UAZAPI.WebhookSecret, ProviderInstanceID: cfg.UAZAPI.InstanceID,
		CaptureDir: cfg.UAZAPI.CaptureDir, Logger: logger,
	})

	root := http.NewServeMux()
	if cfg.MCPEnabled {
		root.Handle("/mcp", mcpserver.New(store, cfg.MCPToken, logger))
	}
	root.Handle("/", apiHandler)

	server := &http.Server{
		Addr:              cfg.HTTPAddress(),
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", cfg.HTTPAddress(), "environment", cfg.Environment, "mcp_enabled", cfg.MCPEnabled)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		logger.Info("server stopping")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		return nil
	}
}

func usageError() error {
	return errors.New("usage: contexta <serve|migrate|uazapi-status|uazapi-configure-webhook>")
}
