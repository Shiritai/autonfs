package deployer

import (
	"autonfs/internal/builder"
	"autonfs/internal/config"
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

// Options Deployment Options
type Options struct {
	SSHAlias      string
	LocalDir      string
	RemoteDir     string
	IdleTimeout   string
	WakeTimeout   string // Configurable wake timeout
	LoadThreshold string
	DryRun        bool
	WatcherDryRun bool // New option
}

// ArtifactBuilder abstracts the build process
type ArtifactBuilder interface {
	Build(arch, src, dst string) error
}

// defaultBuilder implements ArtifactBuilder using internal/builder
type defaultBuilder struct{}

func (b *defaultBuilder) Build(arch, src, dst string) error {
	return builder.BuildForArch(arch, src, dst)
}

// LocalExecutor abstracts local command execution and file reading
type LocalExecutor interface {
	RunCommand(name string, args ...string) error
	ReadFile(path string) ([]byte, error)
}

// defaultLocalExecutor uses exec.Command and ioutil/os
type defaultLocalExecutor struct{}

func (e *defaultLocalExecutor) RunCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	// Bind to stdio for visibility, or at least Stderr?
	// For deployer interactive sudo, we need Stdin.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (e *defaultLocalExecutor) ReadFile(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

// Deployer handles deployment logic, supports dependency injection
type Deployer struct {
	client    SSHClient
	builder   ArtifactBuilder
	localExec LocalExecutor
}

// NewDeployer creates a new Deployer
func NewDeployer(client SSHClient) *Deployer {
	return &Deployer{
		client:    client,
		builder:   &defaultBuilder{},
		localExec: &defaultLocalExecutor{},
	}
}

// NewDeployerWithDeps allows injecting dependencies (for testing)
func NewDeployerWithDeps(client SSHClient, builder ArtifactBuilder, localExec LocalExecutor) *Deployer {
	return &Deployer{
		client:    client,
		builder:   builder,
		localExec: localExec,
	}
}

// RunDeploy executes the full deployment process
func RunDeploy(opts Options) error {
	// 1. Connect
	fmt.Println(">> [1/5] Connecting and discovering environment...")
	client, err := sshutil.NewClient(opts.SSHAlias)
	if err != nil {
		return err
	}
	// Persistent connection
	if err := client.Connect(); err != nil {
		return err
	}
	defer client.Close()

	d := NewDeployer(client)
	return d.Run(opts)
}

// ApplyOptions defines options for the Apply method
type ApplyOptions struct {
	DryRun        bool
	WatcherDryRun bool
}

// Apply executes the deployment based on the provided configuration
func (d *Deployer) Apply(cfg *config.Config, opts ApplyOptions) error {
	// 0. Iterate Hosts
	for _, host := range cfg.Hosts {
		if err := d.applyHost(host, opts); err != nil {
			return fmt.Errorf("failed to deploy host %s: %v", host.Alias, err)
		}
	}
	return nil
}

// applyHost handles deployment for a single host
func (d *Deployer) applyHost(host config.HostConfig, opts ApplyOptions) error {
	fmt.Printf(">> Deploying to Host: %s", host.Alias)
	if opts.DryRun {
		fmt.Printf(" [DRY-RUN]\n")
	} else {
		fmt.Printf("\n")
	}

	// 1. Establish Connection (Reused logic)
	// Apply logic creates new client per host if not injected.
	var client SSHClient
	if d.client != nil {
		client = d.client
	} else {
		c, err := sshutil.NewClient(host.Alias)
		if err != nil {
			return err
		}
		// Connect now to fail fast
		if err := c.Connect(); err != nil {
			return fmt.Errorf("SSH connect failed: %v", err)
		}
		defer c.Close()
		client = c
	}

	// 2. Discovery
	info, err := discover.Probe(client)
	if err != nil {
		return err
	}
	fmt.Printf("   Remote: %s (%s, %s)\n", info.Hostname, info.IP, info.Arch)
	localIP := getOutboundIP(info.IP)

	// 3. Build Binary
	fmt.Println("   Preparing autonfs binary...")
	tmpBin := filepath.Join(os.TempDir(), fmt.Sprintf("autonfs-bin-%s", info.Arch))
	if err := d.builder.Build(info.Arch, "./cmd/autonfs", tmpBin); err != nil {
		return fmt.Errorf("build failed: %v", err)
	}
	defer os.Remove(tmpBin) // In real run; in test mock builder does nothing

	// 4. Prepare Server Configs (Service & Exports)
	// Gather exports
	var exports []templates.ExportInfo
	for _, m := range host.Mounts {
		exports = append(exports, templates.ExportInfo{
			Path:     m.Remote,
			ClientIP: localIP,
		})
	}

	// Use first mount for basic template vars if needed, or defaults
	tmplCfg := templates.Config{
		ServerIP:      info.IP,
		ClientIP:      localIP,
		MacAddr:       info.MAC,
		BinaryPath:    "/usr/local/bin/autonfs",
		IdleTimeout:   host.IdleTimeout,
		WakeTimeout:   host.WakeTimeout,
		LoadThreshold: "0.5", // Default? Add to YAML?
		Exports:       exports,
		WatcherDryRun: opts.WatcherDryRun, // Pass Watcher Dry Run flag
	}
	if tmplCfg.IdleTimeout == "" {
		tmplCfg.IdleTimeout = "5m"
	} // Default
	if tmplCfg.WakeTimeout == "" {
		tmplCfg.WakeTimeout = "120s"
	}

	serviceContent, _ := templates.Render("service", templates.ServerServiceTmpl, tmplCfg)
	exportsContent, _ := templates.Render("exports", templates.ServerExportsTmpl, tmplCfg)

	// 5. Upload & Install Server Components
	if opts.DryRun {
		fmt.Println("   [DRY-RUN] Would upload binary to /usr/local/bin/autonfs")
		fmt.Println("   [DRY-RUN] Would install systemd service: autonfs-watcher.service")
		fmt.Println("   [DRY-RUN] Would configure NFS exports: /etc/exports.d/autonfs.exports")
		fmt.Println("   [DRY-RUN] Would reload/restart remote services")
	} else {
		// Checks
		serviceChanged := remoteHasChange(client, "/etc/systemd/system/autonfs-watcher.service", serviceContent)
		_ = remoteHasChange(client, "/etc/exports.d/autonfs.exports", exportsContent) // Check but ignore result for now

		// Upload Binary (Always upload binary for now, or check checksum? Stick to always for simplicity/safety)
		if err := client.Scp(tmpBin, "/tmp/autonfs"); err != nil {
			return fmt.Errorf("SCP binary failed: %v", err)
		}
		// Upload Service
		if err := writeToRemoteTmp(client, serviceContent, "/tmp/autonfs-watcher.service"); err != nil {
			return err
		}
		// Upload Exports
		if err := writeToRemoteTmp(client, exportsContent, "/tmp/autonfs.exports"); err != nil {
			return err
		}

		// Install Commands
		// Conditional move? No, always move to overwrite.
		// Key is RESTART.
		installCmds := []string{
			"mv /tmp/autonfs /usr/local/bin/autonfs",
			"chmod +x /usr/local/bin/autonfs",
			"mv /tmp/autonfs-watcher.service /etc/systemd/system/autonfs-watcher.service",
			"mkdir -p /etc/exports.d",
			"mv /tmp/autonfs.exports /etc/exports.d/autonfs.exports",
			"systemctl daemon-reload",
			"systemctl enable --now autonfs-watcher.service", // Ensure enabled & started (self-healing)
			"exportfs -ra",
		}

		if serviceChanged {
			fmt.Println("   [Remote] Watcher Service Changed -> Restarting")
			installCmds = append(installCmds, "systemctl restart autonfs-watcher.service")
		}

		// Note: exportsChanged doesn't need service restart, exportfs -ra is enough (included above).
		// But if binary changed? We uploaded binary. We assume if we run Apply, we might want to restart?
		// For now, let's stick to Service Config changes triggering Restart.
		// Ideally verify binary version too, but that's complex.

		fullCmd := fmt.Sprintf("sudo bash -c 'set -e; %s'", strings.Join(installCmds, " && "))
		if err := client.RunTerminal(fullCmd); err != nil {
			return fmt.Errorf("remote install failed: %v", err)
		}
	}

	fmt.Println("   Remote configured.")

	// 6. Local Setup (Client Side)
	fmt.Println("   Deploying Local Units...")
	anyHostChange := false

	for _, m := range host.Mounts {
		unitName := escapeSystemdPath(m.Local)
		mountTmplCfg := templates.Config{
			ServerIP:    info.IP,
			RemoteDir:   m.Remote,
			LocalDir:    m.Local,
			MacAddr:     info.MAC,
			BinaryPath:  "/usr/local/bin/autonfs",
			IdleTimeout: host.IdleTimeout,
		}

		// Lookup executable logic
		exe, _ := os.Executable()
		mountTmplCfg.BinaryPath = exe

		mountContent, _ := templates.Render("mount", templates.ClientMountTmpl, mountTmplCfg)
		automountContent, _ := templates.Render("automount", templates.ClientAutomountTmpl, mountTmplCfg)

		mountFile := fmt.Sprintf("/etc/systemd/system/%s.mount", unitName)
		automountFile := fmt.Sprintf("/etc/systemd/system/%s.automount", unitName)

		unitChanged := false

		// Check & Write Mount Unit
		if hasChange(d.localExec, mountFile, mountContent) {
			fmt.Printf("   -> Updating %s\n", mountFile)
			if opts.DryRun {
				fmt.Printf("      [DRY-RUN] Would write content to %s\n", mountFile)
			} else {
				if err := localWrite(d.localExec, mountFile, mountContent); err != nil {
					return err
				}
			}
			unitChanged = true
			anyHostChange = true
		}

		// Check & Write Automount Unit
		if hasChange(d.localExec, automountFile, automountContent) {
			fmt.Printf("   -> Updating %s\n", automountFile)
			if opts.DryRun {
				fmt.Printf("      [DRY-RUN] Would write content to %s\n", automountFile)
			} else {
				if err := localWrite(d.localExec, automountFile, automountContent); err != nil {
					return err
				}
			}
			unitChanged = true
			anyHostChange = true
		}

		// Always ensure enabled
		if opts.DryRun {
			fmt.Printf("      [DRY-RUN] Would enable --now %s\n", fmt.Sprintf("%s.automount", unitName))
		} else {
			if err := d.localExec.RunCommand("sudo", "systemctl", "enable", "--now", fmt.Sprintf("%s.automount", unitName)); err != nil {
				return fmt.Errorf("failed to enable automount for %s: %v", m.Local, err)
			}
		}

		// Restart only if changed
		if unitChanged {
			if opts.DryRun {
				fmt.Printf("      [DRY-RUN] Would restart %s\n", fmt.Sprintf("%s.automount", unitName))
			} else {
				// Daemon reload for this unit change is needed before restart
				d.localExec.RunCommand("sudo", "systemctl", "daemon-reload")
				if err := d.localExec.RunCommand("sudo", "systemctl", "restart", fmt.Sprintf("%s.automount", unitName)); err != nil {
					return fmt.Errorf("failed to restart automount for %s: %v", m.Local, err)
				}
			}
		}
	}

	if anyHostChange {
		if opts.DryRun {
			fmt.Println("   [DRY-RUN] Would daemon-reload")
		} else {
			d.localExec.RunCommand("sudo", "systemctl", "daemon-reload")
		}
	}

	fmt.Println("\n✅ Deployment Applied Successfully!")
	return nil
}

// Run executes the deployment logic
func (d *Deployer) Run(opts Options) error {
	// SUDO Check (Legacy)
	if !opts.DryRun {
		fmt.Println(">> [0/5] Checking local Sudo privileges...")
		if err := d.localExec.RunCommand("sudo", "-v"); err != nil {
			fmt.Printf("Warning: Failed to obtain sudo privileges (%v). Subsequent operations may fail.\n", err)
		}
	} else {
		fmt.Println(">> [DryRun] Skipping sudo check.")
	}

	// 4. Delegate to Apply logic
	// We need to construct Config from legacy Options
	cfg := &config.Config{
		Hosts: []config.HostConfig{
			{
				Alias:       opts.SSHAlias,
				IdleTimeout: opts.IdleTimeout,
				WakeTimeout: opts.WakeTimeout,
				Mounts: []config.MountConfig{
					{
						Local:  opts.LocalDir,
						Remote: opts.RemoteDir,
					},
				},
			},
		},
	}

	applyOpts := ApplyOptions{
		DryRun:        opts.DryRun,
		WatcherDryRun: opts.WatcherDryRun,
	}

	return d.Apply(cfg, applyOpts)
}

var RunByTest = false // Helper for testing

// Helper: Write content to remote temp file (no sudo)
func writeToRemoteTmp(c SSHClient, content []byte, remotePath string) error {
	tmpFile, err := ioutil.TempFile("", "deploy_config_*_"+filepath.Base(remotePath))
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	return c.Scp(tmpPath, remotePath)
}

// Helper: Write to local file (sudo)
func localWrite(executor LocalExecutor, path string, content []byte) error {
	tmpFile, err := ioutil.TempFile("", "local_write_*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	if err := executor.RunCommand("sudo", "mv", tmpPath, path); err != nil {
		return fmt.Errorf("failed to write local file (%s): %v", path, err)
	}
	return nil
}

// Helper: Get local outbound IP
func getOutboundIP(target string) string {
	if RunByTest {
		return "127.0.0.1"
	}
	conn, err := net.Dial("udp", target+":80")
	if err != nil {
		return "0.0.0.0"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// Helper: Check if file content changed
func hasChange(executor LocalExecutor, path string, newContent []byte) bool {
	existing, err := executor.ReadFile(path)
	if err != nil {
		return true // File doesn't exist or error -> updated
	}
	return string(existing) != string(newContent)
}

// Helper: Check if remote file content changed
func remoteHasChange(client SSHClient, path string, newContent []byte) bool {
	out, err := client.RunCommand("cat " + path)
	if err != nil {
		return true // File doesn't exist or error -> updated
	}
	// RunCommand output might contain newlines?
	// Usually RunCommand returns trimmed output?
	// The original RunCommand implementation returns []byte or string?
	// Checking interfaces.go: RunCommand(cmd string) (string, error)
	// So it returns string. We should compare string(newContent).
	// But `cat` output implies exact content?
	// Let's assume exact match.
	return out != string(newContent)
}

// Helper: Convert path to systemd escaped string (e.g. /mnt/data -> mnt-data)
func escapeSystemdPath(path string) string {
	cmd := exec.Command("systemd-escape", "--path", path)
	out, err := cmd.Output()
	if err != nil {
		// Fallback
		path = strings.Trim(path, "/")
		return strings.ReplaceAll(path, "/", "-")
	}
	return strings.TrimSpace(string(out))
}

// RunUndeploy Execute undeploy, cleanup local and remote (optional)
func RunUndeploy(opts Options) error {
	// 0. Local Sudo Warmup
	exec.Command("sudo", "-v").Run()

	// === Local Cleanup ===
	unitName := escapeSystemdPath(opts.LocalDir)
	automountUnit := fmt.Sprintf("%s.automount", unitName)
	mountUnit := fmt.Sprintf("%s.mount", unitName)

	fmt.Printf(">> [Local] Removing AutoNFS local config (%s)...\n", opts.LocalDir)

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
		fmt.Printf("\n>> [Remote] Cleaning up remote host (%s)...\n", opts.SSHAlias)

		client, err := sshutil.NewClient(opts.SSHAlias)
		if err != nil {
			return fmt.Errorf("failed to establish SSH connection: %v", err)
		}
		if err := client.Connect(); err != nil {
			return fmt.Errorf("SSH connection failed: %v", err)
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
			return fmt.Errorf("remote cleanup failed: %v", err)
		}
		fmt.Println("   Remote cleanup done.")
	}

	fmt.Println("\n✅ Undeploy Completed!")
	return nil
}
