package watcher

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Monitor 負責系統狀態監控
type Monitor struct {
	ProcLoadAvg  string
	ProcNetTCP   string
	ProcNetTCP6  string
	ShutdownFunc func() error // 用於 Mock 關機行為
}

// WatchConfig 監控配置
type WatchConfig struct {
	IdleTimeout   time.Duration
	LoadThreshold float64
	PollInterval  time.Duration // 檢查間隔，預設 10s
	DryRun        bool
}

// NewMonitor 建立監控器，使用預設路徑
func NewMonitor() *Monitor {
	return &Monitor{
		ProcLoadAvg: "/proc/loadavg",
		ProcNetTCP:  "/proc/net/tcp",
		ProcNetTCP6: "/proc/net/tcp6",
		ShutdownFunc: func() error { // 預設實作：呼叫 systemctl
			cmd := exec.Command("systemctl", "poweroff")
			return cmd.Run()
		},
	}
}

// Watch 啟動監控迴圈
// 這是一個 Blocking call，直到 context 被取消
func (m *Monitor) Watch(ctx context.Context, cfg WatchConfig) error {
	interval := cfg.PollInterval
	if interval == 0 {
		interval = 10 * time.Second
	}

	fmt.Printf("啟動監控 (Idle: %v, Load < %.2f, Interval: %v)\n", cfg.IdleTimeout, cfg.LoadThreshold, interval)
	
	idleStart := time.Now()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// 1. 檢查 NFS 連線
			hasConn, err := m.checkNFSConnection()
			if err != nil {
				fmt.Printf("檢查連線錯誤: %v\n", err)
				continue
			}

			// 2. 檢查 Load
			lowLoad, loadVal, _ := m.checkLoad(cfg.LoadThreshold)

			if hasConn {
				fmt.Printf("[Active] 發現 NFS 連線 (Load: %.2f)\n", loadVal)
				idleStart = time.Now()
			} else if !lowLoad {
				fmt.Printf("[Busy] 系統負載過高 (Load: %.2f)\n", loadVal)
				idleStart = time.Now()
			} else {
				idleDuration := time.Since(idleStart)
				fmt.Printf("[Idle] 已閒置 %v (Load: %.2f)\n", idleDuration, loadVal)

				if idleDuration > cfg.IdleTimeout {
					fmt.Println("達到閒置閾值，準備關機...")
					if !cfg.DryRun {
						if err := m.ShutdownFunc(); err != nil {
							fmt.Printf("關機失敗: %v\n", err)
						}
					} else {
						fmt.Println("[Dry Run] 模擬關機指令已發送")
						idleStart = time.Now() // 重置以利持續觀察
					}
				}
			}
		}
	}
}

func (m *Monitor) checkLoad(threshold float64) (bool, float64, error) {
	data, err := ioutil.ReadFile(m.ProcLoadAvg)
	if err != nil {
		return false, 0, err
	}
	parts := strings.Fields(string(data))
	if len(parts) < 1 {
		return false, 0, fmt.Errorf("無法解析 loadavg")
	}
	load, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return false, 0, err
	}
	return load < threshold, load, nil
}

func (m *Monitor) checkNFSConnection() (bool, error) {
	files := []string{m.ProcNetTCP, m.ProcNetTCP6}
	
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Scan()
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 {
				continue
			}
			// local_address: IP:Port (in Hex)
			localAddr := fields[1]
			state := fields[3]

			// Check Port 2049 (Hex: 0801) & ESTABLISHED (01)
			if strings.HasSuffix(localAddr, ":0801") && state == "01" {
				return true, nil
			}
		}
	}
	return false, nil
}

func (m *Monitor) shutdown() error {
	cmd := exec.Command("systemctl", "poweroff")
	return cmd.Run()
}
