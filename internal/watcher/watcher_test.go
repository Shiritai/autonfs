package watcher

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Helper to create a temp file with content
func createTempFile(t *testing.T, dir, pattern, content string) *os.File {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	f.Close()
	return f
}

func TestCheckLoad(t *testing.T) {
	// Mock High Load: 1.5 > 0.5
	highLoadFile := createTempFile(t, "", "loadavg_high", "1.50 0.50 0.20 1/500 12345")
	defer os.Remove(highLoadFile.Name())

	m := NewMonitor(nil)
	m.ProcLoadAvg = highLoadFile.Name()

	isLow, load, err := m.checkLoad(0.5)
	if err != nil {
		t.Fatalf("checkLoad failed: %v", err)
	}
	if isLow {
		t.Errorf("Expected isLow=false for load 1.5 (>0.5), got true. Load: %f", load)
	}

	// Mock Low Load: 0.1 < 0.5
	lowLoadFile := createTempFile(t, "", "loadavg_low", "0.10 0.20 0.20 1/500 12345")
	defer os.Remove(lowLoadFile.Name())

	m.ProcLoadAvg = lowLoadFile.Name()
	isLow, load, err = m.checkLoad(0.5)
	if err != nil {
		t.Fatalf("checkLoad failed: %v", err)
	}
	if !isLow {
		t.Errorf("Expected isLow=true for load 0.1 (<0.5), got false. Load: %f", load)
	}
}

func TestGetNFSv4Clients(t *testing.T) {
	// Mock Clients Directory
	tmpDir, err := os.MkdirTemp("", "nfsd_clients")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	m := NewMonitor(nil)
	m.ProcNFSv4 = tmpDir

	// Case 1: Empty directory (No clients)
	clients, err := m.getNFSv4Clients()
	if err != nil {
		t.Fatalf("getNFSv4Clients failed: %v", err)
	}
	if len(clients) != 0 {
		t.Errorf("Expected 0 clients, got %d", len(clients))
	}

	// Case 2: One client
	clientDir := filepath.Join(tmpDir, "client_1")
	if err := os.Mkdir(clientDir, 0755); err != nil {
		t.Fatal(err)
	}
	infoPath := filepath.Join(clientDir, "info")
	if err := os.WriteFile(infoPath, []byte("address: \"192.168.1.200:54321\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	clients, err = m.getNFSv4Clients()
	if err != nil {
		t.Fatalf("getNFSv4Clients failed: %v", err)
	}
	if len(clients) != 1 {
		t.Errorf("Expected 1 client, got %d", len(clients))
	}
}

func TestGetNFSProcCount(t *testing.T) {
	// Mock RPC File
	// Format:
	// net 123 ...
	// rpc 456 ...
	// proc3 22 1 2 ... (22 fields)
	// proc4 2 1 ... (2 fields)
	content := `net 100 200
rpc 300 5
proc2 18 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
proc3 22 10 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
proc4 2 5 0
`
	rpcFile := createTempFile(t, "", "nfsd_rpc", content)
	defer os.Remove(rpcFile.Name())

	m := NewMonitor(nil)
	m.ProcRPC = rpcFile.Name()

	count, err := m.getNFSProcCount()
	if err != nil {
		t.Fatalf("getNFSProcCount failed: %v", err)
	}

	// Expected: proc3 (10) + proc4 (5) = 15. The first number is field count.
	// Wait, the format in readProcNFSd says:
	// "proc3 22 10..." -> fields[1] is 22 (count), fields[2] is the first op count?
	// Let's check watcher.go logic:
	// if parts[0] == "proc3" || parts[0] == "proc4":
	//    skip parts[1] (field count), sum parts[2:]
	// So for "proc3 22 10 0 ...", parts[1]="22", parts[2]="10". Sum = 10.
	// For "proc4 2 5 0", parts[1]="2", parts[2]="5", parts[3]="0". Sum = 5.
	// Total = 15.
	expected := uint64(15)
	if count != expected {
		t.Errorf("Expected %d ops, got %d", expected, count)
	}
}

func TestMonitor_Watch_Integration_V2(t *testing.T) {
	// 1. Setup Mock Environment
	loadFile := createTempFile(t, "", "load", "0.00 0.00 0.00 1/100 1")
	defer os.Remove(loadFile.Name())

	clientsDir, _ := os.MkdirTemp("", "clients")
	defer os.RemoveAll(clientsDir)

	// Initial RPC content
	rpcFile := createTempFile(t, "", "rpc", "proc3 2 0 0\nproc4 2 0 0\n")
	defer os.Remove(rpcFile.Name())

	var wg sync.WaitGroup
	wg.Add(1)
	shutdownCalled := false

	m := NewMonitor(nil)
	m.ProcLoadAvg = loadFile.Name()
	m.ProcNFSv4 = clientsDir // Empty initially
	m.ProcRPC = rpcFile.Name()
	m.ShutdownFunc = func() error {
		shutdownCalled = true
		wg.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Short timeout for testing
	cfg := WatchConfig{
		IdleTimeout:   200 * time.Millisecond,
		LoadThreshold: 0.5,
		PollInterval:  50 * time.Millisecond,
		DryRun:        false,
	}

	go m.Watch(ctx, cfg)

	// Wait for shutdown (Should be idle immediately)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
		if !shutdownCalled {
			t.Error("ShutdownFunc expected to be called")
		}
	case <-time.After(1 * time.Second):
		t.Error("Timed out waiting for shutdown")
	}
}
