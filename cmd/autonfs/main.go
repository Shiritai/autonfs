package main

import (
	"autonfs/internal/deployer"
	"autonfs/internal/discover"
	"autonfs/internal/watcher"
	"autonfs/pkg/sshutil"
	"autonfs/pkg/wol"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func main() {
	var verbose bool
	var rootCmd = &cobra.Command{
		Use: "autonfs",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			level := slog.LevelInfo
			if verbose {
				level = slog.LevelDebug
			}
			logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level: level,
			}))
			slog.SetDefault(logger)
		},
	}
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")

	// --- Debug Command (Phase 1 & 2) ---
	var debugCmd = &cobra.Command{
		Use:   "debug [ssh_alias]",
		Short: "Test SSH connection and discovery",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			alias := args[0]
			slog.Info("1. Parsing config", "alias", alias)

			client, err := sshutil.NewClient(alias)
			if err != nil {
				slog.Error("Failed to create SSH client", "error", err)
				os.Exit(1)
			}
			slog.Info("Target Host Info", "host", client.Host, "user", client.User)

			slog.Info("2. Performing Remote Discovery...")
			info, err := discover.Probe(client)
			if err != nil {
				slog.Error("Discovery failed", "error", err)
				os.Exit(1)
			}

			fmt.Println("------------------------------------------------")
			fmt.Printf("Hostname    : %s\n", info.Hostname)
			fmt.Printf("Architecture: %s\n", info.Arch)
			fmt.Printf("Interface   : %s\n", info.Interface)
			fmt.Printf("IPv4        : %s (For NFS Mount)\n", info.IP)
			fmt.Printf("MAC Address : %s (For WoL Wake)\n", info.MAC)
			fmt.Println("------------------------------------------------")
			slog.Info("Discovery successful! Sufficient data for configuration.")
		},
	}

	// --- Wake Command (Client Side) ---
	var (
		wakeMac     string
		wakeIP      string
		wakePort    int
		wakeBcast   string
		wakeTimeout time.Duration
	)
	var wakeCmd = &cobra.Command{
		Use:   "wake",
		Short: "Send WoL packet and wait for port open",
		Run: func(cmd *cobra.Command, args []string) {
			slog.Info("Waking MAC", "mac", wakeMac)

			// 1. Send WoL
			packet, err := wol.NewMagicPacket(wakeMac)
			if err != nil {
				slog.Error("Invalid MAC format", "error", err)
				os.Exit(1)
			}
			// Simple broadcast address assumption, refined in Phase 4
			if err := packet.Send(wakeBcast); err != nil {
				slog.Warn("WoL send failed", "error", err)
			} else {
				slog.Info("WoL packet sent")
			}

			// 2. Wait for Port
			slog.Info("Waiting for host to come online", "ip", wakeIP, "port", wakePort, "timeout", wakeTimeout)
			if err := wol.WaitForPort(wakeIP, wakePort, wakeTimeout); err != nil {
				slog.Error("Wake timeout or failed", "error", err)
				os.Exit(1)
			}
			slog.Info("Host is online!")
		},
	}
	wakeCmd.Flags().StringVar(&wakeMac, "mac", "", "MAC Address")
	wakeCmd.Flags().StringVar(&wakeIP, "ip", "", "Target IP")
	wakeCmd.Flags().StringVar(&wakeBcast, "bcast", "255.255.255.255", "Broadcast IP")
	wakeCmd.Flags().IntVar(&wakePort, "port", 2049, "Target Port (Default: NFS 2049)")
	wakeCmd.Flags().DurationVar(&wakeTimeout, "timeout", 120*time.Second, "Timeout for wake up")
	wakeCmd.MarkFlagRequired("mac")
	wakeCmd.MarkFlagRequired("ip")

	// --- Watch Command (Server Side) ---
	var (
		watchIdle   time.Duration
		watchLoad   float64
		watchDryRun bool
	)
	var watchCmd = &cobra.Command{
		Use:   "watch",
		Short: "Monitor NFS connections and system load",
		Run: func(cmd *cobra.Command, args []string) {
			m := watcher.NewMonitor(nil)
			cfg := watcher.WatchConfig{
				IdleTimeout:   watchIdle,
				LoadThreshold: watchLoad,
				// PollInterval: 0, // Use default 10s
				DryRun: watchDryRun,
			}

			// Blocking call
			if err := m.Watch(cmd.Context(), cfg); err != nil {
				slog.Error("Monitor terminated abnormally", "error", err)
				os.Exit(1)
			}
		},
	}
	watchCmd.Flags().DurationVar(&watchIdle, "timeout", 30*time.Minute, "Idle shutdown timeout")
	watchCmd.Flags().Float64Var(&watchLoad, "load", 0.5, "Minimum load threshold")
	watchCmd.Flags().BoolVar(&watchDryRun, "dry-run", false, "Simulation only, do not poweroff")

	// --- Deploy Command ---
	var (
		deployLocal   string
		deployRemote  string
		deployIdle    string
		deployLoad    string
		deployDry     bool
		watcherDryRun bool
	)

	var deployCmd = &cobra.Command{
		Use:   "deploy [ssh_alias]",
		Short: "Deploy AutoNFS to local and remote",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if deployLocal == "" || deployRemote == "" {
				slog.Error("--local-dir and --remote-dir are required")
				cmd.Usage()
				os.Exit(1)
			}

			opts := deployer.Options{
				SSHAlias:      args[0],
				LocalDir:      deployLocal,
				RemoteDir:     deployRemote,
				IdleTimeout:   deployIdle,
				LoadThreshold: deployLoad,
				DryRun:        deployDry,
				WatcherDryRun: watcherDryRun,
			}

			if err := deployer.RunDeploy(opts); err != nil {
				slog.Error("Deploy failed", "error", err)
				os.Exit(1)
			}
		},
	}
	deployCmd.Flags().StringVar(&deployLocal, "local-dir", "", "Local mount point (Required)")
	deployCmd.Flags().StringVar(&deployRemote, "remote-dir", "", "Remote directory (Required)")
	deployCmd.Flags().StringVar(&deployIdle, "idle", "30m", "Idle shutdown time")
	deployCmd.Flags().StringVar(&deployLoad, "load", "0.5", "Load threshold")
	deployCmd.Flags().BoolVar(&deployDry, "dry-run", false, "Preview only")
	deployCmd.Flags().BoolVar(&watcherDryRun, "watcher-dry-run", false, "Watcher in dry-run mode (Log only)")

	// --- Undeploy Command ---
	var undeployLocal string
	var undeployCmd = &cobra.Command{
		Use:   "undeploy [ssh_alias]",
		Short: "Remove AutoNFS local config (Optional: cleanup remote if alias provided)",
		Args:  cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			sshAlias := ""
			if len(args) > 0 {
				sshAlias = args[0]
			}
			opts := deployer.Options{
				LocalDir: undeployLocal,
				SSHAlias: sshAlias,
			}
			if err := deployer.RunUndeploy(opts); err != nil {
				slog.Error("Undeploy failed", "error", err)
				os.Exit(1)
			}
		},
	}
	undeployCmd.Flags().StringVar(&undeployLocal, "local-dir", "/mnt/remote_data", "Local mount point")
	undeployCmd.MarkFlagRequired("local-dir")

	// --- Apply Command ---
	var (
		applyCfgFile    string
		applyDryRun     bool
		applyWatcherDry bool
	)
	var applyCmd = &cobra.Command{
		Use:   "apply",
		Short: "Deploy or update configuration from autonfs.yaml",
		Run: func(cmd *cobra.Command, args []string) {
			opts := ApplyOptions{
				ConfigPath:    applyCfgFile,
				DryRun:        applyDryRun,
				WatcherDryRun: applyWatcherDry,
			}
			if err := RunApply(opts); err != nil {
				slog.Error("Apply failed", "error", err)
				os.Exit(1)
			}
		},
	}
	applyCmd.Flags().StringVarP(&applyCfgFile, "file", "f", "autonfs.yaml", "Config file path")
	applyCmd.Flags().BoolVarP(&applyDryRun, "dry-run", "n", false, "dry-run (no write)")
	applyCmd.Flags().BoolVar(&applyWatcherDry, "watcher-dry-run", false, "Deploy watcher in dry-run mode")

	rootCmd.AddCommand(debugCmd, wakeCmd, watchCmd, deployCmd, undeployCmd, applyCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
