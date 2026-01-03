package watcher

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// OSOperator defines interface for OS interactions
type OSOperator interface {
	ReadFile(name string) ([]byte, error)
	ReadDir(name string) ([]os.DirEntry, error)
	RunCommand(name string, arg ...string) error
}

// RealOSOperator implements OSOperator using real OS calls
type RealOSOperator struct{}

func (o *RealOSOperator) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (o *RealOSOperator) ReadDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(name)
}

func (o *RealOSOperator) RunCommand(name string, arg ...string) error {
	return exec.Command(name, arg...).Run()
}

// Monitor responsible for system state monitoring
type Monitor struct {
	ProcLoadAvg  string
	ProcRPC      string
	ProcNFSv4    string // /proc/fs/nfsd/clients/
	ShutdownFunc func() error
	OS           OSOperator
}

// WatchConfig monitor configuration
type WatchConfig struct {
	IdleTimeout   time.Duration
	LoadThreshold float64
	PollInterval  time.Duration // Check interval, default 10s
	DryRun        bool
}

// NewMonitor creates a new metrics monitor
func NewMonitor(osOp OSOperator) *Monitor {
	if osOp == nil {
		osOp = &RealOSOperator{}
	}
	m := &Monitor{
		ProcLoadAvg: "/proc/loadavg",
		ProcRPC:     "/proc/net/rpc/nfsd",
		ProcNFSv4:   "/proc/fs/nfsd/clients",
		OS:          osOp,
	}
	m.ShutdownFunc = func() error {
		return m.OS.RunCommand("systemctl", "poweroff")
	}
	return m
}

// Watch starts the monitoring loop (Blocking)
func (m *Monitor) Watch(ctx context.Context, cfg WatchConfig) error {
	interval := cfg.PollInterval
	if interval == 0 {
		interval = 10 * time.Second
	}

	fmt.Printf("=== AutoNFS Watcher Started ===\n")
	fmt.Printf("Config: Idle=%v, Load<%.2f, Interval=%v, DryRun=%v\n",
		cfg.IdleTimeout, cfg.LoadThreshold, interval, cfg.DryRun)

	idleStart := time.Now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastOps uint64 = 0

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// --- Data Collection Phase ---

			// 1. Get Load
			isLowLoad, loadVal, err := m.checkLoad(cfg.LoadThreshold)
			if err != nil {
				fmt.Printf("[Error] Read Load: %v\n", err)
			}

			// 2. Get NFSv4 Clients
			clients, err := m.getNFSv4Clients()
			if err != nil {
				// Normal behavior if not mounted or NFSv4 not active
			}

			// 3. Get NFS Ops Delta
			currOps, err := m.getNFSProcCount()
			var opsDelta uint64 = 0
			if err == nil {
				if lastOps > 0 {
					opsDelta = currOps - lastOps
				}
				lastOps = currOps
			} else {
				// Only log critical RPC read errors
				fmt.Printf("[Warn] Read RPC: %v\n", err)
			}

			// --- Decision Phase ---

			// Reasons to be Active:
			// 1. High Load -> Busy
			// 2. Connected NFSv4 Clients -> Mounted (Strongest Active Signal)
			// 3. High Ops Delta -> Data Transfer (Fallback)

			isActive := false
			activeReason := ""

			if !isLowLoad {
				isActive = true
				activeReason = fmt.Sprintf("High Load (%.2f)", loadVal)
			} else if len(clients) > 0 {
				isActive = true
				clientList := strings.Join(clients, ", ")
				activeReason = fmt.Sprintf("Client Connected (%s)", clientList)
			} else if opsDelta > 0 {
				isActive = true
				activeReason = fmt.Sprintf("NFS Activity (Delta %d)", opsDelta)
			}

			// --- Logging & Action Phase ---

			now := time.Now().Format("15:04:05")

			if isActive {
				idleStart = time.Now()
				fmt.Printf("%s [ACTIVE] %s | Load: %.2f | Ops: %d\n", now, activeReason, loadVal, opsDelta)
			} else {
				rawIdleDur := time.Since(idleStart)
				displayIdleDur := rawIdleDur.Truncate(time.Second)
				timeLeft := cfg.IdleTimeout - rawIdleDur
				if timeLeft < 0 {
					timeLeft = 0
				}
				// Round timeLeft for nicer display
				displayTimeLeft := timeLeft.Round(time.Second)
				if timeLeft < time.Second {
					displayTimeLeft = timeLeft // Show ms if < 1s
				}

				fmt.Printf("%s [IDLE]   Dataset: 0 clients, %d ops | Load: %.2f | Idle: %v (Shutdown in %v)\n",
					now, opsDelta, loadVal, displayIdleDur, displayTimeLeft)

				if rawIdleDur > cfg.IdleTimeout {
					fmt.Printf("%s [SHUTDOWN] Idle threshold reached.\n", now)
					if !cfg.DryRun {
						if err := m.ShutdownFunc(); err != nil {
							fmt.Printf("[Error] Shutdown failed: %v\n", err)
						}
					} else {
						fmt.Printf("%s [DRY-RUN] Simulated poweroff command.\n", now)
						idleStart = time.Now() // Reset to avoid log flooding
					}
				}
			}
		}
	}
}

// checkLoad checks system load
func (m *Monitor) checkLoad(threshold float64) (bool, float64, error) {
	data, err := m.OS.ReadFile(m.ProcLoadAvg)
	if err != nil {
		return false, 0, err
	}
	parts := strings.Fields(string(data))
	if len(parts) < 1 {
		return false, 0, fmt.Errorf("invalid loadavg")
	}
	load, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return false, 0, err
	}
	return load < threshold, load, nil
}

// getNFSv4Clients returns list of IP addresses of connected v4 clients
func (m *Monitor) getNFSv4Clients() ([]string, error) {
	files, err := m.OS.ReadDir(m.ProcNFSv4)
	if err != nil {
		return nil, err
	}

	var clientIPs []string
	for _, f := range files {
		if !f.IsDir() {
			continue
		}

		infoPath := filepath.Join(m.ProcNFSv4, f.Name(), "info")
		content, err := m.OS.ReadFile(infoPath)
		if err != nil {
			continue
		}

		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "address:") {
				// address: "192.168.1.x:port"
				parts := strings.Split(line, "\"")
				if len(parts) >= 2 {
					ipPort := parts[1]
					if host, _, found := strings.Cut(ipPort, ":"); host != "" && found {
						clientIPs = append(clientIPs, host)
					} else {
						clientIPs = append(clientIPs, ipPort)
					}
				}
			}
		}
	}
	return clientIPs, nil
}

// getNFSProcCount reads total operations from /proc/net/rpc/nfsd
func (m *Monitor) getNFSProcCount() (uint64, error) {
	data, err := m.OS.ReadFile(m.ProcRPC)
	if err != nil {
		return 0, err
	}

	var totalOps uint64 = 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		header := fields[0]
		if header == "proc3" || header == "proc4" {
			// fields[1] is the number of fields, counters start at fields[2]
			for i := 2; i < len(fields); i++ {
				if cnt, err := strconv.ParseUint(fields[i], 10, 64); err == nil {
					totalOps += cnt
				}
			}
		}
	}
	return totalOps, nil
}

func (m *Monitor) shutdown() error {
	cmd := exec.Command("systemctl", "poweroff")
	return cmd.Run()
}
