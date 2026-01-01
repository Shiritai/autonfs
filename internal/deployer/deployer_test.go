package deployer

import (
	"autonfs/internal/config"
	"fmt"
	"io/ioutil"
	"strings"
	"testing"
)

func init() {
	RunByTest = true
}

// MockSSHClient mocks the SSHClient interface for testing
type MockSSHClient struct {
	Connected     bool
	Closed        bool
	Cmds          []string
	ScpCalls      []string
	FailOnConnect bool
	FailOnCmd     string // if cmd contains this string, return error
	DiscoveryInfo string // return this for discovery command
}

func (m *MockSSHClient) Connect() error {
	if m.FailOnConnect {
		return fmt.Errorf("mock connection failed")
	}
	m.Connected = true
	return nil
}

func (m *MockSSHClient) Close() error {
	m.Closed = true
	return nil
}

func (m *MockSSHClient) RunTerminal(cmd string) error {
	if m.FailOnCmd != "" && strings.Contains(cmd, m.FailOnCmd) {
		return fmt.Errorf("mock command failed: %s", cmd)
	}
	m.Cmds = append(m.Cmds, cmd)
	return nil
}

func (m *MockSSHClient) Scp(localPath, remotePath string) error {
	m.ScpCalls = append(m.ScpCalls, fmt.Sprintf("%s -> %s", localPath, remotePath))
	return nil
}

func (m *MockSSHClient) RunCommand(cmd string) (string, error) {
	if m.FailOnCmd != "" && strings.Contains(cmd, m.FailOnCmd) {
		return "", fmt.Errorf("mock command failed: %s", cmd)
	}
	// Simple mock for discovery
	if strings.Contains(cmd, "uname -n") {
		return "mock-host", nil
	}
	if strings.Contains(cmd, "uname -m") {
		return "x86_64", nil
	}
	if strings.Contains(cmd, "ip route get") {
		// return mocked network info: interface|ip|mac
		if m.DiscoveryInfo != "" {
			return m.DiscoveryInfo, nil
		}
		return "eth0|192.168.1.100|00:11:22:33:44:55", nil
	}
	return "", nil
}

func TestEscapeSystemdPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/mnt/data", "mnt-data"},
		{"mnt/data", "mnt-data"},
		{"/var/lib/my-app", "var-lib-my\\x2dapp"},
		{"/home/user/nc-disk/data", "home-user-nc\\x2ddisk-data"},
		{"/", "-"},
		{"", "-"},
	}

	for _, tt := range tests {
		got := escapeSystemdPath(tt.input)
		if got != tt.want {
			t.Errorf("escapeSystemdPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGetOutboundIP(t *testing.T) {
	ip := getOutboundIP("8.8.8.8")
	if ip == "" {
		t.Error("getOutboundIP returned empty string")
	}
	if strings.Count(ip, ".") != 3 {
		t.Errorf("getOutboundIP returned invalid format: %s", ip)
	}
}

// MockBuilder mocks the build process
type MockBuilder struct{}

func (m *MockBuilder) Build(arch, src, dst string) error {
	// Mock successful build
	return nil
}

type MockLocalExecutor struct {
	Cmds      []string
	FailOnCmd string
	Files     map[string][]byte
}

func (m *MockLocalExecutor) ReadFile(path string) ([]byte, error) {
	if m.Files == nil {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	content, ok := m.Files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return content, nil
}

func (m *MockLocalExecutor) RunCommand(name string, args ...string) error {
	cmdStr := name + " " + strings.Join(args, " ")
	if m.FailOnCmd != "" && strings.Contains(cmdStr, m.FailOnCmd) {
		return fmt.Errorf("mock local failure: %s", cmdStr)
	}
	m.Cmds = append(m.Cmds, cmdStr)

	// Simulate mv for Idempotency Test
	// Command: sudo mv tmpPath targetPath
	if name == "sudo" && len(args) >= 3 && args[0] == "mv" {
		src := args[1]
		dst := args[2]
		// Read src from real disk (since localWrite writes to disk)
		content, err := ioutil.ReadFile(src)
		if err == nil {
			if m.Files == nil {
				m.Files = make(map[string][]byte)
			}
			m.Files[dst] = content
		}
	}
	return nil
}

func TestDeployer_Apply_MultiHost(t *testing.T) {
	// Mock 2 clients

	// Since Apply creates new clients if nil, but our mock NewDeployerWithDeps
	// takes a single client. Apply doesn't use d.client if it's nil?
	// Actually Apply logic: if d.client != nil { use it } else { create new }
	// To test multi-host with MOCKS, we need a way to inject a "MockClientFactory" or existing clients?
	// Our current DI is simple: single injected client.
	// If we want to test multi-host iteration in Apply, we must support injecting a map of clients or a factory.
	// But `autonfs.yaml` uses Aliases.
	// HACK for Test: Inject a single client that returns success for all ops, representing "connection reused" or "mock works for all".
	// But wait, Apply logic loops and re-uses d.client if set?
	// Logic:
	// Iterate hosts:
	//   if d.client != nil { client = d.client } else { create new }
	// So if we inject d.client (Mock), it will be REUSED for ALL hosts.
	// This confirms we CAN test multi-host logic using a single MockClient that accumulates calls.

	mockClient := &MockSSHClient{
		DiscoveryInfo: "eth0|192.168.1.100|00:00:00:00:00:00",
	}
	mockBuilder := &MockBuilder{}
	mockLocal := &MockLocalExecutor{}

	cfg := &config.Config{
		Hosts: []config.HostConfig{
			{Alias: "host1", Mounts: []config.MountConfig{{Local: "/m1", Remote: "/r1"}}},
			{Alias: "host2", Mounts: []config.MountConfig{{Local: "/m2", Remote: "/r2"}}},
		},
	}

	d := NewDeployerWithDeps(mockClient, mockBuilder, mockLocal)
	if err := d.Apply(cfg, ApplyOptions{}); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Verify we ran logic twice (roughly)
	// Check SCP calls count. 3 files per host = 6 calls total.
	// Binary, Service, Exports.
	expectedScp := 6
	if len(mockClient.ScpCalls) != expectedScp {
		t.Errorf("Expected %d SCP calls, got %d", expectedScp, len(mockClient.ScpCalls))
	}

	// Verify Local Mounts: 2 hosts * 1 mount = 2 local sets
	// 2 mount files + 2 automount files = 4 writes
	mvCount := 0
	for _, cmd := range mockLocal.Cmds {
		if strings.Contains(cmd, "mv") {
			mvCount++
		}
	}
	// Local writes use `mv`.
	// Mount + Automount for Host1 = 2
	// Mount + Automount for Host2 = 2
	// Total 4.
	if mvCount != 4 {
		t.Errorf("Expected 4 local mv operations (mount+automount units), got %d. Cmds: %v", mvCount, mockLocal.Cmds)
	}
}

func TestDeployer_Apply_Error_SSH(t *testing.T) {
	mockClient := &MockSSHClient{
		FailOnConnect: true,
	}
	d := NewDeployerWithDeps(mockClient, &MockBuilder{}, &MockLocalExecutor{})

	cfg := &config.Config{
		Hosts: []config.HostConfig{{Alias: "bad-host", Mounts: []config.MountConfig{{Local: "/l", Remote: "/r"}}}},
	}

	// In the shared-client mode (injected), the mock returns error on Connect if implemented?
	// Wait, d.client is set. Logic:
	// if d.client != nil { client = d.client }
	// We don't call Connect() on injected client inside loop?
	// Let's check Apply logic:
	/*
		if d.client != nil {
			client = d.client
		} else {
			... client.Connect() ...
		}
	*/
	// So injected client is assumed connected?
	// Our MockSSHClient.Connect() is likely not called if injected.
	// We need to fail a later step, e.g., Probe (RunCommand) or Scp.

	mockClient.FailOnCmd = "uname" // Probe uses uname

	err := d.Apply(cfg, ApplyOptions{})
	if err == nil {
		t.Error("Expected error due to SSH failure, got nil")
	}
}

func TestDeployer_Apply_Error_Local(t *testing.T) {
	mockClient := &MockSSHClient{DiscoveryInfo: "eth0|192.168.1.50|00:11:22:33:44:55"}
	mockLocal := &MockLocalExecutor{
		FailOnCmd: "systemctl enable",
	}

	d := NewDeployerWithDeps(mockClient, &MockBuilder{}, mockLocal)
	cfg := &config.Config{
		Hosts: []config.HostConfig{{Alias: "h1", Mounts: []config.MountConfig{{Local: "/l", Remote: "/r"}}}},
	}

	err := d.Apply(cfg, ApplyOptions{})
	if err == nil {
		t.Error("Expected error due to Local Sudo failure, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "mock local failure") {
		t.Errorf("Expected mock failure error, got: %v", err)
	}
}

func TestDeployer_Apply_Idempotent(t *testing.T) {
	mockClient := &MockSSHClient{DiscoveryInfo: "eth0|192.168.1.100|00:00:00:00:00:00"}
	mockLocal := &MockLocalExecutor{
		// Simulate files already exist with same content?
		// Hard to predict generated content exactly without rendering.
		// Strategy:
		// 1. Run Apply once (First run). Capture "mv" calls.
		// 2. Populate MockLocalExecutor.Files with the written content.
		// 3. Clear Cmds.
		// 4. Run Apply again.
		// 5. Expect 0 "mv" calls and 0 "restart" calls.
		Files: make(map[string][]byte),
	}
	// We need to capture writes. MockLocalExecutor doesn't capture content in 'mv' call easily unless we parse.
	// But `localWrite` writes to temp file then mv.
	// We mocked `ReadFile`.
	// To support STEP 2, we need a way to "Store what was written".
	// Since we can't easily intercept the temp file content from `mv cmd`,
	// we will manually Pre-seed the Files with what we expect, OR
	// modify MockLocalExecutor to intercept `mv temp target`?
	// But temp file is real file on disk (created by ioutil.WriteFile).
	// So MockLocalExecutor COULD read it!

	// Better: Update MockLocalExecutor.RunCommand to handle "mv src dst":
	// If command is mv, read src, write to m.Files[dst].
	d := NewDeployerWithDeps(mockClient, &MockBuilder{}, mockLocal)
	cfg := &config.Config{
		Hosts: []config.HostConfig{{Alias: "h1", Mounts: []config.MountConfig{{Local: "/idempotent", Remote: "/r"}}}},
	}

	// 1st Run
	if err := d.Apply(cfg, ApplyOptions{}); err != nil {
		t.Fatalf("First Apply failed: %v", err)
	}

	// Check we had writes
	firstRunMv := 0
	for _, cmd := range mockLocal.Cmds {
		if strings.Contains(cmd, "mv") {
			firstRunMv++
		}
	}
	if firstRunMv == 0 {
		t.Fatal("First run should have writes")
	}

	// 2nd Run
	mockLocal.Cmds = nil // Clear history
	if err := d.Apply(cfg, ApplyOptions{}); err != nil {
		t.Fatalf("Second Apply failed: %v", err)
	}

	// Expect NO mv calls (idempotent)
	secondRunMv := 0
	for _, cmd := range mockLocal.Cmds {
		if strings.Contains(cmd, "mv") {
			secondRunMv++
		}
	}

	if secondRunMv != 0 {
		t.Errorf("Expected 0 mv calls in second run, got %d. Cmds: %v", secondRunMv, mockLocal.Cmds)
	}
}

func TestDeployer_Apply_DryRun(t *testing.T) {
	mockClient := &MockSSHClient{
		DiscoveryInfo: "eth0|192.168.1.100|00:00:00:00:00:00",
		// Allow "cat" commands to fail (simulating file not found -> changed)
		// or succeed. Since it's DryRun, we just want to ensure it doesn't crash.
		// If cat fails, it returns error, remoteHasChange returns true.
		// If we don't set CmdOutput, RunCommand might return empty string.
	}
	mockLocal := &MockLocalExecutor{Files: make(map[string][]byte)}
	d := NewDeployerWithDeps(mockClient, &MockBuilder{}, mockLocal)

	cfg := &config.Config{
		Hosts: []config.HostConfig{{Alias: "h1", Mounts: []config.MountConfig{{Local: "/dry", Remote: "/r"}}}},
	}

	// Run with DryRun = true
	opts := ApplyOptions{DryRun: true}
	if err := d.Apply(cfg, opts); err != nil {
		t.Fatalf("DryRun Apply failed: %v", err)
	}

	// Verify NO mutating commands
	// 1. No SCP calls
	if len(mockClient.ScpCalls) > 0 {
		t.Errorf("Expected 0 SCP calls in DryRun, got %d: %v", len(mockClient.ScpCalls), mockClient.ScpCalls)
	}

	// 2. No Local Writes (mv)
	mvCount := 0
	for _, cmd := range mockLocal.Cmds {
		if strings.Contains(cmd, "mv") {
			mvCount++
		}
	}
	if mvCount > 0 {
		t.Errorf("Expected 0 local mv calls in DryRun, got %d: %v", mvCount, mockLocal.Cmds)
	}

	// 3. No Systemd Restart/Enable
	sysCount := 0
	for _, cmd := range mockLocal.Cmds {
		if strings.Contains(cmd, "systemctl restart") || strings.Contains(cmd, "systemctl enable") {
			sysCount++
		}
	}
	if sysCount > 0 {
		t.Errorf("Expected 0 systemctl calls in DryRun, got %d: %v", sysCount, mockLocal.Cmds)
	}
}
