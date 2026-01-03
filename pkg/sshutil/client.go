package sshutil

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/term"
)

// Client wraps SSH connection information
type Client struct {
	Alias string
	Host  string
	Port  string
	User  string
	Key   string

	conn *ssh.Client // Persistent connection
}

// NewClient parses ~/.ssh/config and returns a Client object
func NewClient(alias string) (*Client, error) {
	// ... (Parsing logic same as before) ...
	// Load default config
	f, err := os.Open(filepath.Join(os.Getenv("HOME"), ".ssh", "config"))
	if err != nil {
		return nil, fmt.Errorf("failed to read ssh config: %v", err)
	}
	defer f.Close()

	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ssh config: %v", err)
	}

	host, _ := cfg.Get(alias, "HostName")
	if host == "" {
		host = alias
	}

	user, _ := cfg.Get(alias, "User")
	if user == "" {
		user = os.Getenv("USER")
	}

	port, _ := cfg.Get(alias, "Port")
	if port == "" {
		port = "22"
	}

	key, _ := cfg.Get(alias, "IdentityFile")

	return &Client{
		Alias: alias,
		Host:  host,
		Port:  port,
		User:  user,
		Key:   key,
	}, nil
}

// Connect establishes an SSH connection
func (c *Client) Connect() error {
	if c.conn != nil {
		return nil // Already connected
	}

	authMethods := []ssh.AuthMethod{}

	// 1. Try SSH Agent
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			signers, err := agentClient.Signers()
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signers...))
			}
		}
	}

	// 2. Try IdentityFile (Private Key)
	keyFiles := []string{}
	if c.Key != "" && c.Key != "~/.ssh/identity" {
		keyFiles = append(keyFiles, expandPath(c.Key))
	}
	defaultKeys := []string{
		filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa"),
		filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519"),
		filepath.Join(os.Getenv("HOME"), ".ssh", "id_ecdsa"),
	}
	for _, dk := range defaultKeys {
		if _, err := os.Stat(dk); err == nil {
			keyFiles = append(keyFiles, dk)
		}
	}

	for _, kPath := range keyFiles {
		key, err := os.ReadFile(kPath)
		if err == nil {
			signer, err := ssh.ParsePrivateKey(key)
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}

	config := &ssh.ClientConfig{
		User:            c.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%s", c.Host, c.Port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("SSH connect failed [%s]: %v", addr, err)
	}

	c.conn = client
	return nil
}

// Close closes the connection
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// RunCommand executes a single command (non-interactive, returns Output)
func (c *Client) RunCommand(cmd string) (string, error) {
	if c.conn == nil {
		if err := c.Connect(); err != nil {
			return "", err
		}
	}

	session, err := c.conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(output), fmt.Errorf("command execution failed: %v\nOutput: %s", err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}

// RunTerminal executes command with PTY (interactive, supports sudo password input)
// Stdin/Stdout/Stderr are piped directly, and local terminal is set to Raw Mode
func (c *Client) RunTerminal(cmd string) error {
	if c.conn == nil {
		if err := c.Connect(); err != nil {
			return err
		}
	}

	session, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	// Request PTY
	modes := ssh.TerminalModes{
		ssh.ECHO:          1, // Default to echo
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	// Get window size (if valid)
	fd := int(os.Stdin.Fd())
	w, h, err := term.GetSize(fd)
	if err != nil {
		w, h = 80, 40
	}

	if err := session.RequestPty("xterm", h, w, modes); err != nil {
		return fmt.Errorf("request PTY failed: %v", err)
	}

	// Pipe stdio
	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// Set to Raw Mode (Critical: allows remote sudo to handle echo/input correctly)
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("failed to set raw terminal: %v", err)
		}
		defer term.Restore(fd, oldState)
	}

	if err := session.Run(cmd); err != nil {
		// Error message might look weird in raw mode, but should be fine after restore
		return fmt.Errorf("command execution failed: %v", err)
	}
	return nil
}

// Scp uploads a local file to remote destination using scp command
func (c *Client) Scp(localPath, remotePath string) error {
	// Note: using exec scp depends on system binary.
	// Future improvement: implement pure-go scp or sftp to remove external dependency.
	cmd := exec.Command("scp", localPath, fmt.Sprintf("%s:%s", c.Alias, remotePath))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("SCP failed: %v, Output: %s", err, string(output))
	}
	return nil
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(os.Getenv("HOME"), path[2:])
	}
	return path
}
