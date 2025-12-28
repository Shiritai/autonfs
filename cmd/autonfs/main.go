package main

import (
	"autonfs/internal/discover" // 新增引用
	"autonfs/pkg/sshutil"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "autonfs",
		Short: "AutoNFS - 自動化 NFS 掛載與電源管理工具",
	}

	var debugCmd = &cobra.Command{
		Use:   "debug [ssh_alias]",
		Short: "測試 SSH 連線並獲取遠端資訊",
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

	rootCmd.AddCommand(debugCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}