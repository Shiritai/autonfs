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
	UploadCalls   []string
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

func (m *MockSSHClient) UploadFile(localPath, remotePath string) error {
	m.UploadCalls = append(m.UploadCalls, fmt.Sprintf("%s -> %s", localPath, remotePath))
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

	// Simulate install for Idempotency Test
	// Command: sudo install <flags...> tmpPath targetPath
	if name == "sudo" && len(args) >= 3 && args[0] == "install" {
		src := args[len(args)-2]
		dst := args[len(args)-1]
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

// localInstall is the command prefix localWrite uses to place a unit file
const localInstall = "sudo install -m 644 -o root -g root"

func countCmds(cmds []string, substr string) int {
	n := 0
	for _, cmd := range cmds {
		if strings.Contains(cmd, substr) {
			n++
		}
	}
	return n
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
	// Check Upload calls count. 3 files per host = 6 calls total.
	// Binary, Service, Exports.
	expectedUploads := 6
	if len(mockClient.UploadCalls) != expectedUploads {
		t.Errorf("Expected %d Upload calls, got %d", expectedUploads, len(mockClient.UploadCalls))
	}

	// Verify Local Mounts: 2 hosts * 1 mount = 2 local sets
	// Each set writes mount + automount + wake units = 6 writes
	if n := countCmds(mockLocal.Cmds, localInstall); n != 6 {
		t.Errorf("Expected 6 local installs (mount+automount+wake units), got %d. Cmds: %v", n, mockLocal.Cmds)
	}
}

// Files must land root-owned with fixed modes: moving the deploying user's
// 0600 temp files left units user-owned (writable by a non-root user, yet
// loaded or executed by root) and unreadable to others.
func TestDeployer_Apply_InstallsRootOwned(t *testing.T) {
	mockClient := &MockSSHClient{DiscoveryInfo: "eth0|192.168.1.100|00:11:22:33:44:55"}
	mockLocal := &MockLocalExecutor{}
	d := NewDeployerWithDeps(mockClient, &MockBuilder{}, mockLocal)

	cfg := &config.Config{
		Hosts: []config.HostConfig{{Alias: "h1", Mounts: []config.MountConfig{{Local: "/l", Remote: "/r"}}}},
	}
	if err := d.Apply(cfg, ApplyOptions{}); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if n := countCmds(mockLocal.Cmds, "sudo mv"); n != 0 {
		t.Errorf("Expected no local mv, got %d. Cmds: %v", n, mockLocal.Cmds)
	}

	remote := strings.Join(mockClient.Cmds, "\n")
	for _, want := range []string{
		"install -m 755 -o root -g root /tmp/autonfs /usr/local/bin/autonfs",
		"install -m 644 -o root -g root /tmp/autonfs-watcher.service /etc/systemd/system/autonfs-watcher.service",
		"install -m 644 -o root -g root /tmp/autonfs.exports /etc/exports.d/autonfs.exports",
	} {
		if !strings.Contains(remote, want) {
			t.Errorf("Remote install missing %q. Got:\n%s", want, remote)
		}
	}
	if strings.Contains(remote, "mv /tmp/") {
		t.Errorf("Expected no remote mv from /tmp. Got:\n%s", remote)
	}
}

func TestDeployer_Apply_ClientUnits(t *testing.T) {
	mockClient := &MockSSHClient{DiscoveryInfo: "eth0|192.168.1.100|00:11:22:33:44:55"}
	mockLocal := &MockLocalExecutor{Files: make(map[string][]byte)}
	d := NewDeployerWithDeps(mockClient, &MockBuilder{}, mockLocal)

	// idle_timeout is left unset to check that host defaults reach client units.
	cfg := &config.Config{
		Hosts: []config.HostConfig{{
			Alias:       "h1",
			WakeTimeout: "180s",
			Mounts:      []config.MountConfig{{Local: "/mnt/data", Remote: "/r"}},
		}},
	}
	if err := d.Apply(cfg, ApplyOptions{}); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	mount := string(mockLocal.Files["/etc/systemd/system/mnt-data.mount"])
	automount := string(mockLocal.Files["/etc/systemd/system/mnt-data.automount"])
	wake := string(mockLocal.Files["/etc/systemd/system/autonfs-wake-mnt-data.service"])

	checks := []struct {
		unit, content, want string
	}{
		{"mount", mount, "Requires=autonfs-wake-mnt-data.service"},
		{"mount", mount, "After=autonfs-wake-mnt-data.service"},
		{"wake", wake, `--mac "00:11:22:33:44:55" --ip "192.168.1.100" --port 2049 --timeout 180s`},
		{"automount", automount, "TimeoutIdleSec=5m"},
	}
	for _, c := range checks {
		if !strings.Contains(c.content, c.want) {
			t.Errorf("%s unit missing %q. Got:\n%s", c.unit, c.want, c.content)
		}
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
		// 1. Run Apply once (First run). Capture "install" calls.
		// 2. Populate MockLocalExecutor.Files with the written content.
		// 3. Clear Cmds.
		// 4. Run Apply again.
		// 5. Expect 0 "install" calls and 0 "restart" calls.
		Files: make(map[string][]byte),
	}
	// localWrite stages content in a real temp file and then installs it, so
	// MockLocalExecutor.RunCommand intercepts "install ... src dst", reads src
	// and records it in m.Files[dst] (STEP 2).
	d := NewDeployerWithDeps(mockClient, &MockBuilder{}, mockLocal)
	cfg := &config.Config{
		Hosts: []config.HostConfig{{Alias: "h1", Mounts: []config.MountConfig{{Local: "/idempotent", Remote: "/r"}}}},
	}

	// 1st Run
	if err := d.Apply(cfg, ApplyOptions{}); err != nil {
		t.Fatalf("First Apply failed: %v", err)
	}

	// Check we had writes
	if countCmds(mockLocal.Cmds, localInstall) == 0 {
		t.Fatal("First run should have writes")
	}

	// 2nd Run
	mockLocal.Cmds = nil // Clear history
	if err := d.Apply(cfg, ApplyOptions{}); err != nil {
		t.Fatalf("Second Apply failed: %v", err)
	}

	// Expect NO writes (idempotent)
	if n := countCmds(mockLocal.Cmds, localInstall); n != 0 {
		t.Errorf("Expected 0 local installs in second run, got %d. Cmds: %v", n, mockLocal.Cmds)
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
	// 1. No Upload calls
	if len(mockClient.UploadCalls) > 0 {
		t.Errorf("Expected 0 Upload calls in DryRun, got %d: %v", len(mockClient.UploadCalls), mockClient.UploadCalls)
	}

	// 2. No Local Writes
	if n := countCmds(mockLocal.Cmds, localInstall); n > 0 {
		t.Errorf("Expected 0 local installs in DryRun, got %d: %v", n, mockLocal.Cmds)
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
