package watcher

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestCheckLoad(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "loadavg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	// Mock high load: 1.50 > 0.5
	if _, err := tmpfile.Write([]byte("1.50 0.50 0.20 1/500 12345")); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	// Use Monitor with injected path
	m := NewMonitor()
	m.ProcLoadAvg = tmpfile.Name()

	isLow, load, err := m.checkLoad(0.5)
	if err != nil {
		t.Errorf("CheckLoad error: %v", err)
	}
	if isLow {
		t.Errorf("Expected high load (false), got low load (true). Load: %f", load)
	}
}

func TestCheckNFSConnection(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	content := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0801 00000000:0000 01 00000000:00000000 00:00000000 00000000     0        0 12345 1 ffff888000000000 100 0 0 10 0
`
	tmpfile.Write([]byte(content))
	tmpfile.Close()

	m := NewMonitor()
	m.ProcNetTCP = tmpfile.Name()
	m.ProcNetTCP6 = "/non/existent"

	hasConn, err := m.checkNFSConnection()
	if err != nil {
		t.Errorf("CheckNFSConnection error: %v", err)
	}
	if !hasConn {
		t.Error("Expected active connection, got none")
	}
}

// TestMonitor_Watch_Integration Simulates the full loop behavior
func TestMonitor_Watch_Integration(t *testing.T) {
	// 1. Setup Mock Files
	loadAvgFile, _ := os.CreateTemp("", "loadavg_int")
	defer os.Remove(loadAvgFile.Name())
	
	tcpFile, _ := os.CreateTemp("", "tcp_int")
	defer os.Remove(tcpFile.Name())
	
	// Initial State: High Load (to prevent idle count at start, or Low Load to start counting)
	// Let's start with Low Load, No Conn -> expect Shutdown
	loadAvgFile.Write([]byte("0.10 0.10 0.10 1/500 1234"))
	loadAvgFile.Sync() // Ensure write flush

	// No connection
	tcpFile.Write([]byte("  sl  local_address ...\n"))
	tcpFile.Sync()

	shutdownCalled := false
	var wg sync.WaitGroup
	wg.Add(1)

	// 2. Setup Monitor
	m := NewMonitor()
	m.ProcLoadAvg = loadAvgFile.Name()
	m.ProcNetTCP = tcpFile.Name()
	m.ProcNetTCP6 = "/non/existent"
	m.ShutdownFunc = func() error {
		shutdownCalled = true
		wg.Done()
		return nil
	}

	// 3. Run Watch with short interval
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := WatchConfig{
		IdleTimeout:   500 * time.Millisecond,
		LoadThreshold: 0.5,
		PollInterval:  100 * time.Millisecond,
		DryRun:        false,
	}

	go func() {
		m.Watch(ctx, cfg)
	}()

	// 4. Wait for shutdown (Should happen after ~500ms)
	// Use channel to avoid deadlock if test fails
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		if !shutdownCalled {
			t.Error("ShutdownFunc should have been called")
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for ShutdownFunc")
	}
}
