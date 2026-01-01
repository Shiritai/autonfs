package discover

import (
	"fmt"
	"strings"
)

// ServerInfo 封裝遠端主機的關鍵資訊
type ServerInfo struct {
	Hostname string
	Arch     string
	// Network
	Interface string
	IP        string
	MAC       string
}

// SSHClient abstract the required SSH operations for discovery
type SSHClient interface {
	RunCommand(cmd string) (string, error)
}

// Probe 執行遠端偵測並回傳資訊
func Probe(client SSHClient) (*ServerInfo, error) {
	info := &ServerInfo{}

	// 1. 取得 Hostname
	host, err := client.RunCommand("uname -n")
	if err != nil {
		return nil, fmt.Errorf("取得 hostname 失敗: %v", err)
	}
	info.Hostname = host

	// 2. 取得 Architecture
	arch, err := client.RunCommand("uname -m")
	if err != nil {
		return nil, fmt.Errorf("取得架構失敗: %v", err)
	}
	info.Arch = arch

	// 3. 網路探索 (關鍵邏輯)
	// 原理：
	// 1. ip route get 1.1.1.1: 找出通往外網(預設閘道)的介面
	// 2. awk 提取介面名稱 (dev 之後的欄位)
	// 3. ip -4 addr show: 顯示該介面的 IPv4 資訊
	// 4. cat /sys/class/net/...: 直接讀取 MAC Address 檔案，比解析 ifconfig 安全
	// 輸出格式: "interface_name|ip_address|mac_address"
	cmd := `
	iface=$(ip route get 1.1.1.1 | awk '{for(i=1;i<=NF;i++) if($i=="dev") print $(i+1); exit}');
	ip=$(ip -4 addr show $iface | awk '/inet/ {print $2}' | cut -d/ -f1 | head -n1);
	mac=$(cat /sys/class/net/$iface/address);
	echo "$iface|$ip|$mac"
	`

	netOut, err := client.RunCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("網路探索失敗: %v", err)
	}

	iface, ip, mac, err := parseNetworkInfo(netOut)
	if err != nil {
		return nil, err
	}

	info.Interface = iface
	info.IP = ip
	info.MAC = mac

	return info, nil
}

// parseNetworkInfo 解析 "iface|ip|mac" 格式的字串
func parseNetworkInfo(raw string) (string, string, string, error) {
	parts := strings.Split(strings.TrimSpace(raw), "|")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("網路資訊格式錯誤，預期 3 個欄位，收到: %s", raw)
	}
	return parts[0], parts[1], parts[2], nil
}
