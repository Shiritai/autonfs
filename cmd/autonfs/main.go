package main

import (
	"autonfs/internal/discover"
	"autonfs/internal/watcher"
	"autonfs/pkg/sshutil"
	"autonfs/pkg/wol"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func main() {
	var rootCmd = &cobra.Command{Use: "autonfs"}

	// --- Debug Command (Phase 1 & 2) ---
	var debugCmd = &cobra.Command{
		Use:   "debug [ssh_alias]",
		Short: "測試連線與探索",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			alias := args[0]
			fmt.Printf("1. 解析配置: %s ...\n", alias)

			client, err := sshutil.NewClient(alias)
			if err != nil {
				fmt.Printf("錯誤: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("   -> 目標 Host: %s, User: %s\n", client.Host, client.User)

			fmt.Println("2. 執行遠端探索 (Discovery)...")
			info, err := discover.Probe(client)
			if err != nil {
				fmt.Printf("   -> 探索失敗: %v\n", err)
				os.Exit(1)
			}

			fmt.Println("------------------------------------------------")
			fmt.Printf("主機名稱 : %s\n", info.Hostname)
			fmt.Printf("硬體架構 : %s\n", info.Arch)
			fmt.Printf("網路介面 : %s\n", info.Interface)
			fmt.Printf("IPv4位址 : %s (將用於 NFS 掛載)\n", info.IP)
			fmt.Printf("MAC 位址 : %s (將用於 WoL 喚醒)\n", info.MAC)
			fmt.Println("------------------------------------------------")
			fmt.Println("Phase 2 驗證成功！資料已足夠生成配置檔。")
		},
	}

	// --- Wake Command (Client Side) ---
	var (
		wakeMac   string
		wakeIP    string
		wakePort  int
		wakeBcast string
	)
	var wakeCmd = &cobra.Command{
		Use:   "wake",
		Short: "發送 WoL 並等待 Port 開啟",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("嘗試喚醒 MAC: %s ...\n", wakeMac)
			
			// 1. 發送 WoL
			packet, err := wol.NewMagicPacket(wakeMac)
			if err != nil {
				fmt.Printf("MAC 格式錯誤: %v\n", err)
				os.Exit(1)
			}
			// 這裡簡單假設廣播位址，Phase 4 生成配置時會填入更精確的
			if err := packet.Send(wakeBcast); err != nil {
				fmt.Printf("WoL 發送失敗: %v\n", err)
			} else {
				fmt.Println("WoL 封包已發送")
			}

			// 2. 等待 Port
			fmt.Printf("等待主機 %s:%d 上線...\n", wakeIP, wakePort)
			if err := wol.WaitForPort(wakeIP, wakePort, 120*time.Second); err != nil {
				fmt.Printf("喚醒超時或失敗: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("主機已上線！")
		},
	}
	wakeCmd.Flags().StringVar(&wakeMac, "mac", "", "MAC Address")
	wakeCmd.Flags().StringVar(&wakeIP, "ip", "", "Target IP")
	wakeCmd.Flags().StringVar(&wakeBcast, "bcast", "255.255.255.255", "Broadcast IP")
	wakeCmd.Flags().IntVar(&wakePort, "port", 2049, "Target Port (Default: NFS 2049)")
	wakeCmd.MarkFlagRequired("mac")
	wakeCmd.MarkFlagRequired("ip")

	// --- Watch Command (Server Side) ---
	var (
		watchIdle    time.Duration
		watchLoad    float64
		watchDryRun  bool
	)
	var watchCmd = &cobra.Command{
		Use:   "watch",
		Short: "監控 NFS 連線與系統負載",
		Run: func(cmd *cobra.Command, args []string) {
			m := watcher.NewMonitor()
			cfg := watcher.WatchConfig{
				IdleTimeout:   watchIdle,
				LoadThreshold: watchLoad,
				// PollInterval: 0, // Use default 10s
				DryRun:        watchDryRun,
			}
			
			// Blocking call
			if err := m.Watch(cmd.Context(), cfg); err != nil {
				fmt.Printf("監控異常終止: %v\n", err)
				os.Exit(1)
			}
		},
	}
	watchCmd.Flags().DurationVar(&watchIdle, "timeout", 30*time.Minute, "閒置關機時間")
	watchCmd.Flags().Float64Var(&watchLoad, "load", 0.5, "最低負載閾值")
	watchCmd.Flags().BoolVar(&watchDryRun, "dry-run", false, "僅模擬，不執行關機")

	rootCmd.AddCommand(debugCmd, wakeCmd, watchCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}