package wol

import (
	"fmt"
	"net"
	"time"
)

// MagicPacket is 102 Bytes long (6 bytes header + 16 * 6 bytes MAC)
type MagicPacket [102]byte

// NewMagicPacket creates a WoL packet
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

// Send broadcasts the WoL packet
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

// WaitForPort waits for the target TCP port to open
func WaitForPort(ip string, port int, timeout time.Duration) error {
	target := fmt.Sprintf("%s:%d", ip, port)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", target, 1*time.Second)
		if err == nil {
			conn.Close()
			return nil // Connection successful
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %s", target)
}
