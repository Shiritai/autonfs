package deployer

import (
	"autonfs/internal/builder"
	"autonfs/internal/discover"
	"autonfs/internal/templates"
	"autonfs/pkg/sshutil"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Options 部署選項
type Options struct {
	SSHAlias      string
	LocalDir      string
	RemoteDir     string
	IdleTimeout   string
	LoadThreshold string
	DryRun        bool
	WatcherDryRun bool // New option
}

// RunDeploy 執行完整部署流程
func RunDeploy(opts Options) error {
	// 0. 本機 Sudo 預熱 (避免 ugly NOPASSWD)
	fmt.Println(">> [0/5] 檢查本機 Sudo 權限...")
	sudoCmd := exec.Command("sudo", "-v")
	sudoCmd.Stdin = os.Stdin
	sudoCmd.Stdout = os.Stdout
	sudoCmd.Stderr = os.Stderr
	if err := sudoCmd.Run(); err != nil {
		return fmt.Errorf("無法取得本機 Sudo 權限: %v", err)
	}

	// 1. 連線與探索
	fmt.Println(">> [1/5] 連線並探索環境...")
	client, err := sshutil.NewClient(opts.SSHAlias)
	if err != nil {
		return err
	}
	// 建立持久連線
	if err := client.Connect(); err != nil {
		return err
	}
	defer client.Close()

	info, err := discover.Probe(client) // Probe 仍使用 RunCommand (非互動)
	if err != nil {
		return err
	}
	fmt.Printf("   Remote: %s (%s, %s)\n", info.Hostname, info.IP, info.Arch)

	// ... (中間省略: IP, Build, Render) ...
	// 取得本機 IP (相對於遠端)，用於 NFS Exports
	localIP := getOutboundIP(info.IP)
	fmt.Printf("   Local IP for NFS access: %s\n", localIP)

	// 2. 準備 Binary
	fmt.Println(">> [2/5] 準備 autonfs binary...")
	tmpBin := filepath.Join(os.TempDir(), "autonfs-deploy-bin")

	// 總是重新編譯以確保版本最新，且符合架構
	if err := builder.BuildForArch(info.Arch, "./cmd/autonfs", tmpBin); err != nil {
		return fmt.Errorf("編譯失敗: %v", err)
	}
	defer os.Remove(tmpBin)

	// 3. 生成設定檔內容
	fmt.Println(">> [3/5] 生成配置檔...")
	cfg := templates.Config{
		ServerIP:      info.IP,
		ClientIP:      localIP,
		MacAddr:       info.MAC,
		RemoteDir:     opts.RemoteDir,
		LocalDir:      opts.LocalDir,
		BinaryPath:    "/usr/local/bin/autonfs",
		IdleTimeout:   opts.IdleTimeout,
		LoadThreshold: opts.LoadThreshold,
		WatcherDryRun: opts.WatcherDryRun,
	}

	mountContent, _ := templates.Render("mount", templates.ClientMountTmpl, cfg)
	automountContent, _ := templates.Render("automount", templates.ClientAutomountTmpl, cfg)
	serviceContent, _ := templates.Render("service", templates.ServerServiceTmpl, cfg)
	exportsContent, _ := templates.Render("exports", templates.ServerExportsTmpl, cfg)

	if opts.DryRun {
		// ... (DryRun logic unchanged) ...
		fmt.Println("\n--- [DRY RUN] Server Service ---")
		fmt.Println(string(serviceContent))
		fmt.Println("--- [DRY RUN] Server Exports ---")
		fmt.Println(string(exportsContent))
		fmt.Println("--- [DRY RUN] Client Mount ---")
		fmt.Println(string(mountContent))
		fmt.Println("--- [DRY RUN] Client Automount ---")
		fmt.Println(string(automountContent))
		return nil
	}

	// 4. 部署到遠端 (Slave)
	fmt.Println(">> [4/5] 部署遠端 (Slave)...")

	// 4a. 傳送 Binary
	fmt.Println("   Uploading binary...")
	scpCmd := exec.Command("scp", tmpBin, fmt.Sprintf("%s:/tmp/autonfs", opts.SSHAlias))
	if err := scpCmd.Run(); err != nil {
		return fmt.Errorf("SCP 失敗: %v", err)
	}

	// 4b. 上傳 Systemd Service
	// 這裡必須先上傳到 /tmp，稍後的 batch install 才會 mv 到 /etc
	fmt.Println("   Uploading service file...")
	if err := writeToRemoteTmp(client, serviceContent, "/tmp/autonfs-watcher.service"); err != nil {
		return fmt.Errorf("上傳服務檔失敗: %v", err)
	}

	// 4c. 上傳 Exports Config
	// 同理，先上傳到 /tmp
	fmt.Println("   Uploading exports config...")
	if err := writeToRemoteTmp(client, exportsContent, "/tmp/autonfs.exports"); err != nil {
		return fmt.Errorf("上傳 Exports 設定失敗: %v", err)
	}

	// 4d. 執行安裝指令 (Sudo Required)
	fmt.Println("   Executing remote installation (Sudo required)...")

	installCmds := []string{
		// Install Binary
		"mv /tmp/autonfs /usr/local/bin/autonfs",
		"chmod +x /usr/local/bin/autonfs",

		// Install Service
		"mv /tmp/autonfs-watcher.service /etc/systemd/system/autonfs-watcher.service",

		// Install Exports
		"mkdir -p /etc/exports.d",
		"mv /tmp/autonfs.exports /etc/exports.d/autonfs.exports",

		// Reload & Enable
		"systemctl daemon-reload",
		"systemctl enable --now nfs-server",
		"systemctl enable --now autonfs-watcher.service",
		"exportfs -r",
	}

	// 組合指令: sudo bash -c 'set -e; cmd1 && cmd2 && ...'
	fullCmd := fmt.Sprintf("sudo bash -c 'set -e; %s'", strings.Join(installCmds, " && "))

	if err := client.RunTerminal(fullCmd); err != nil {
		return fmt.Errorf("遠端安裝失敗: %v", err)
	}

	// 5. 部署到本機 (Master)
	fmt.Println(">> [5/5] 部署本機 (Master)...")

	unitName := escapeSystemdPath(opts.LocalDir)
	mountFile := fmt.Sprintf("/etc/systemd/system/%s.mount", unitName)
	automountFile := fmt.Sprintf("/etc/systemd/system/%s.automount", unitName)

	if err := localWrite(mountFile, mountContent); err != nil {
		return err
	}
	if err := localWrite(automountFile, automountContent); err != nil {
		return err
	}

	fmt.Println("   Reloading local services...")
	// 本機 Sudo 已經在開頭 -v 過了，這裡直接執行
	exec.Command("sudo", "systemctl", "daemon-reload").Run()

	// 啟用並 "重啟" Automount 以確保新設定 (如 TimeoutIdleSec) 生效
	// 單純 enable --now 如果原本已經 running 就不會 reload
	exec.Command("sudo", "systemctl", "enable", fmt.Sprintf("%s.automount", unitName)).Run()
	cmd := exec.Command("sudo", "systemctl", "restart", fmt.Sprintf("%s.automount", unitName))

	// 連接 Stdin/Stdout 以防萬一 timeout
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("重啟 Automount 失敗: %v", err)
	}

	fmt.Println("\n✅ 部署完成！")
	return nil
}

// 輔助：SCP 檔案
func scpToRemote(c *sshutil.Client, localPath, remotePath string) error {
	scpCmd := exec.Command("scp", localPath, fmt.Sprintf("%s:%s", c.Alias, remotePath))
	if err := scpCmd.Run(); err != nil {
		return fmt.Errorf("SCP %s -> %s 失敗: %v", localPath, remotePath, err)
	}
	return nil
}

// 輔助：寫入內容到遠端暫存檔 (無 sudo)
func writeToRemoteTmp(c *sshutil.Client, content []byte, remotePath string) error {
	tmpFile := "temp_deploy_config_" + filepath.Base(remotePath)
	if err := ioutil.WriteFile(tmpFile, content, 0644); err != nil {
		return err
	}
	defer os.Remove(tmpFile)
	return scpToRemote(c, tmpFile, remotePath)
}

// 輔助：寫入本地檔案 (sudo)
func localWrite(path string, content []byte) error {
	tmp := "temp_local_write"
	if err := ioutil.WriteFile(tmp, content, 0644); err != nil {
		return err
	}
	defer os.Remove(tmp)

	cmd := exec.Command("sudo", "mv", tmp, path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("寫入本機檔案失敗 (%s): %v", path, err)
	}
	return nil
}

// 輔助：獲取本機對外 IP
func getOutboundIP(target string) string {
	conn, err := net.Dial("udp", target+":80")
	if err != nil {
		return "0.0.0.0"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// 輔助：將路徑轉換為 systemd escaped string (e.g. /mnt/data -> mnt-data)
func escapeSystemdPath(path string) string {
	cmd := exec.Command("systemd-escape", "--path", path)
	out, err := cmd.Output()
	if err != nil {
		// Fallback for non-systemd environments (unlikely but safe)
		// Minimal fallback: replace / with -
		path = strings.Trim(path, "/")
		return strings.ReplaceAll(path, "/", "-")
	}
	return strings.TrimSpace(string(out))
}

// RunUndeploy 執行反部署，清理本機與遠端 (可選) Systemd 設定
func RunUndeploy(opts Options) error {
	// 0. 本機 Sudo 預熱
	sudoCmd := exec.Command("sudo", "-v")
	sudoCmd.Stdin = os.Stdin
	sudoCmd.Stdout = os.Stdout
	sudoCmd.Stderr = os.Stderr
	if err := sudoCmd.Run(); err != nil {
		return fmt.Errorf("無法取得本機 Sudo 權限: %v", err)
	}

	// === Local Cleanup ===
	unitName := escapeSystemdPath(opts.LocalDir)
	automountUnit := fmt.Sprintf("%s.automount", unitName)
	mountUnit := fmt.Sprintf("%s.mount", unitName)

	fmt.Printf(">> [Local] 正在移除 AutoNFS 本機設定 (%s)...\n", opts.LocalDir)

	fmt.Println("   Stopping automount & mount...")
	exec.Command("sudo", "systemctl", "disable", "--now", automountUnit).Run()
	exec.Command("sudo", "systemctl", "stop", mountUnit).Run()
	exec.Command("sudo", "systemctl", "disable", mountUnit).Run()

	fmt.Println("   Removing unit files...")
	mountFile := fmt.Sprintf("/etc/systemd/system/%s", mountUnit)
	automountFile := fmt.Sprintf("/etc/systemd/system/%s", automountUnit)

	exec.Command("sudo", "rm", "-f", mountFile).Run()
	exec.Command("sudo", "rm", "-f", automountFile).Run()

	fmt.Println("   Reloading local systemd...")
	exec.Command("sudo", "systemctl", "daemon-reload").Run()

	// === Remote Cleanup (Optional) ===
	if opts.SSHAlias != "" {
		fmt.Printf("\n>> [Remote] 正在清理遠端主機 (%s)...\n", opts.SSHAlias)

		client, err := sshutil.NewClient(opts.SSHAlias)
		if err != nil {
			return fmt.Errorf("無法建立 SSH 連線: %v", err)
		}
		if err := client.Connect(); err != nil {
			return fmt.Errorf("SSH 連線失敗: %v", err)
		}
		defer client.Close()

		cleanupCmds := []string{
			"systemctl disable --now autonfs-watcher.service",
			"rm -f /etc/systemd/system/autonfs-watcher.service",
			"rm -f /etc/exports.d/autonfs.exports",
			"systemctl daemon-reload",
			"exportfs -r",
		}

		fullCmd := fmt.Sprintf("sudo bash -c '%s'", strings.Join(cleanupCmds, " && "))
		fmt.Println("   Executing remote cleanup commands...")
		if err := client.RunTerminal(fullCmd); err != nil {
			return fmt.Errorf("遠端清理失敗: %v", err)
		}
		fmt.Println("   Remote cleanup done.")
	}

	fmt.Println("\n✅ 反部署完成！")
	return nil
}
