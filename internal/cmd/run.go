// Package cmd provides command-line interface functionality for the CLI Proxy API server.
// It includes authentication flows for various AI service providers, service startup,
// and other command-line operations.
package cmd

import (
	"context"
	"errors"
	"os/signal"
	"syscall"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy"
	log "github.com/sirupsen/logrus"
)

// StartService builds and runs the proxy service using the exported SDK.
// It creates a new proxy service instance, sets up signal handling for graceful shutdown,
// and starts the service with the provided configuration.
//
// Parameters:
//   - cfg: The application configuration
//   - configPath: The path to the configuration file
//   - localPassword: Optional password accepted for local management requests
func StartService(cfg *config.Config, configPath string, localPassword string) {
	// Initialize file usage plugin if persistence is configured
	var filePlugin *usage.FileUsagePlugin
	if cfg.UsageStatistics.PersistFile != "" {
		stats := usage.GetRequestStatistics()
		interval, _ := time.ParseDuration(cfg.UsageStatistics.SaveInterval)
		if interval <= 0 && cfg.UsageStatistics.SaveInterval != "" && cfg.UsageStatistics.SaveInterval != "0" {
			log.Warnf("Invalid save-interval %q, disabling periodic save", cfg.UsageStatistics.SaveInterval)
		}
		filePlugin = usage.NewFileUsagePlugin(
			cfg.UsageStatistics.PersistFile,
			interval,
			cfg.UsageStatistics.RestoreOnStart,
			stats,
		)
	}

	builder := cliproxy.NewBuilder().
		WithConfig(cfg).
		WithConfigPath(configPath).
		WithLocalManagementPassword(localPassword)

	// Add hooks to start/stop file plugin
	if filePlugin != nil {
		hooks := cliproxy.Hooks{
			OnAfterStart: func(s *cliproxy.Service) {
				filePlugin.Start()
				log.Infof("Usage statistics persistence enabled: %s", cfg.UsageStatistics.PersistFile)
			},
		}
		builder = builder.WithHooks(hooks)
	}

	ctxSignal, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	runCtx := ctxSignal
	if localPassword != "" {
		var keepAliveCancel context.CancelFunc
		runCtx, keepAliveCancel = context.WithCancel(ctxSignal)
		builder = builder.WithServerOptions(api.WithKeepAliveEndpoint(10*time.Second, func() {
			log.Warn("keep-alive endpoint idle for 10s, shutting down")
			keepAliveCancel()
		}))
	}

	service, err := builder.Build()
	if err != nil {
		log.Errorf("failed to build proxy service: %v", err)
		return
	}

	err = service.Run(runCtx)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Errorf("proxy service exited with error: %v", err)
	}

	// Stop file plugin on shutdown to ensure final save
	if filePlugin != nil {
		filePlugin.Stop()
	}
}

// WaitForCloudDeploy waits indefinitely for shutdown signals in cloud deploy mode
// when no configuration file is available.
func WaitForCloudDeploy() {
	// Clarify that we are intentionally idle for configuration and not running the API server.
	log.Info("Cloud deploy mode: No config found; standing by for configuration. API server is not started. Press Ctrl+C to exit.")

	ctxSignal, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Block until shutdown signal is received
	<-ctxSignal.Done()
	log.Info("Cloud deploy mode: Shutdown signal received; exiting")
}
