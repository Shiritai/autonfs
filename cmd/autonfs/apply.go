package main

import (
	"autonfs/internal/config"
	"autonfs/internal/deployer"
	"fmt"
	"log/slog"
	"os"
)

// ApplyOptions defines flags for the apply command
type ApplyOptions struct {
	ConfigPath    string
	DryRun        bool
	WatcherDryRun bool
}

// RunApply loads the config and triggers the application process
func RunApply(opts ApplyOptions) error {
	if opts.ConfigPath == "" {
		return fmt.Errorf("config file path required")
	}

	slog.Info("Loading config...", "path", opts.ConfigPath)
	data, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	cfg, err := config.ParseConfig(data)
	if err != nil {
		return fmt.Errorf("invalid config: %v", err)
	}

	// Initialize Deployer with defaults (nil client = connection per host)
	d := deployer.NewDeployer(nil)

	// Inject options into Deployer (need to update Deployer.Apply signature or struct)
	// We will pass options to Apply method
	return d.Apply(cfg, deployer.ApplyOptions{
		DryRun:        opts.DryRun,
		WatcherDryRun: opts.WatcherDryRun,
	})
}
