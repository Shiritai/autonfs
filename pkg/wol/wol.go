package wol

import (
	"fmt"
	"net"
	"time"
)

// MagicPacket 固定長度 102 Bytes (6 bytes header + 16 * 6 bytes MAC)
type MagicPacket [102]byte

// NewMagicPacket 建立 WoL 封包
func NewMagicPacket(macAddr string) (*MagicPacket, error) {
	mac, err := net.ParseMAC(macAddr)
	if err != nil {
		return nil, err
	}

	var packet MagicPacket
	// Header: 6 bytes of 0xFF
	copy(packet[0:], []byte{255, 255, 255, 255, 255, 255})
	// Body: MAC repeated 16 times
	offset := 6
	for i := 0; i < 16; i++ {
		copy(packet[offset:], mac)
		offset += 6
	}
	return &packet, nil
}

// Send 發送 WoL 封包 (Broadcast)
func (mp *MagicPacket) Send(broadcastIP string) error {
	addr := fmt.Sprintf("%s:9", broadcastIP) // Port 9 is standard for WoL
	conn, err := net.Dial("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(mp[:])
	return err
}

// WaitForPort 等待目標 Port 開啟 (TCP Check)
func WaitForPort(ip string, port int, timeout time.Duration) error {
	target := fmt.Sprintf("%s:%d", ip, port)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", target, 1*time.Second)
		if err == nil {
			conn.Close()
			return nil // 成功連線
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("等待 %s 超時", target)
}
