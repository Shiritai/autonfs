package discover

import (
	"fmt"
	"strings"
)

// ServerInfo encapsulates key information about the remote host
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

// Probe executes remote detection and returns information
func Probe(client SSHClient) (*ServerInfo, error) {
	info := &ServerInfo{}

	// 1. Get Hostname
	host, err := client.RunCommand("uname -n")
	if err != nil {
		return nil, fmt.Errorf("failed to get hostname: %v", err)
	}
	info.Hostname = host

	// 2. Get Architecture
	arch, err := client.RunCommand("uname -m")
	if err != nil {
		return nil, fmt.Errorf("failed to get architecture: %v", err)
	}
	info.Arch = arch

	// 3. Network Discovery (Critical Logic)
	// Principle:
	// 1. ip route get 1.1.1.1: Find interface to external network (default gateway)
	// 2. awk extract interface name (field after dev)
	// 3. ip -4 addr show: Show IPv4 info for that interface
	// 4. cat /sys/class/net/...: Read MAC Address directly, safer than parsing ifconfig
	// Output format: "interface_name|ip_address|mac_address"
	cmd := `
	# Find default interface
	iface=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev") print $(i+1); exit}')
	if [ -z "$iface" ]; then
		# Fallback: check default route
		iface=$(ip route show default 2>/dev/null | awk '/default/ {for(i=1;i<=NF;i++) if($i=="dev") print $(i+1); exit}')
	fi
	
	# Fallback 2: First non-loopback interface
	if [ -z "$iface" ]; then
		iface=$(ls /sys/class/net/ | grep -v lo | head -n1)
	fi

	if [ -z "$iface" ]; then
		echo "error|no_interface_found|00:00:00:00:00:00"
		exit 1
	fi

	# Get IP (IPv4)
	ip=$(ip -4 addr show $iface 2>/dev/null | awk '/inet/ {print $2}' | cut -d/ -f1 | head -n1)
	if [ -z "$ip" ]; then
		ip="0.0.0.0"
	fi

	# Get MAC
	if [ -f "/sys/class/net/$iface/address" ]; then
		mac=$(cat /sys/class/net/$iface/address)
	else
		mac="00:00:00:00:00:00"
	fi

	echo "$iface|$ip|$mac"
	`

	netOut, err := client.RunCommand(cmd)
	if err != nil {
		return nil, fmt.Errorf("network discovery failed: %v", err)
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

// parseNetworkInfo parses string in "iface|ip|mac" format
func parseNetworkInfo(raw string) (string, string, string, error) {
	parts := strings.Split(strings.TrimSpace(raw), "|")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid network info format, expected 3 fields, got: %s", raw)
	}
	return parts[0], parts[1], parts[2], nil
}
