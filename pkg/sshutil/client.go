package sshutil

import (
	"fmt"
	"io/ioutil"
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

// Client 封裝 SSH 連線資訊
type Client struct {
	Alias string
	Host  string
	Port  string
	User  string
	Key   string

	conn *ssh.Client // Persistent connection
}

// NewClient 從 ~/.ssh/config 解析並回傳 Client 物件
func NewClient(alias string) (*Client, error) {
	// ... (Parsing logic same as before) ...
	// 載入預設配置
	f, err := os.Open(filepath.Join(os.Getenv("HOME"), ".ssh", "config"))
	if err != nil {
		return nil, fmt.Errorf("無法讀取 ssh config: %v", err)
	}
	defer f.Close()

	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("解析 ssh config 失敗: %v", err)
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

// Connect 建立 SSH 連線
func (c *Client) Connect() error {
	if c.conn != nil {
		return nil // Already connected
	}

	authMethods := []ssh.AuthMethod{}

	// 1. 嘗試 SSH Agent
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

	// 2. 嘗試 IdentityFile (Private Key)
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
		key, err := ioutil.ReadFile(kPath)
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
		return fmt.Errorf("SSH 連線失敗 [%s]: %v", addr, err)
	}

	c.conn = client
	return nil
}

// Close 關閉連線
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// RunCommand 執行單一指令 (非互動式，回傳 Output)
func (c *Client) RunCommand(cmd string) (string, error) {
	if c.conn == nil {
		if err := c.Connect(); err != nil {
			return "", err
		}
	}

	session, err := c.conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("無法建立 Session: %v", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(output), fmt.Errorf("執行指令失敗: %v\nOutput: %s", err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}

// RunTerminal 執行指令並分配 PTY (互動式，支援 sudo 密碼輸入)
// 輸出直接導向 os.Stdout/Stderr，並將本機終端機設為 Raw Mode
func (c *Client) RunTerminal(cmd string) error {
	if c.conn == nil {
		if err := c.Connect(); err != nil {
			return err
		}
	}

	session, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("無法建立 Session: %v", err)
	}
	defer session.Close()

	// 請求 PTY
	modes := ssh.TerminalModes{
		ssh.ECHO:          1, // Default to echo
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	// 獲取視窗大小 (若可行)
	fd := int(os.Stdin.Fd())
	w, h, err := term.GetSize(fd)
	if err != nil {
		w, h = 80, 40
	}

	if err := session.RequestPty("xterm", h, w, modes); err != nil {
		return fmt.Errorf("請求 PTY 失敗: %v", err)
	}

	// 串接標準輸入輸出
	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// 設為 Raw Mode (關鍵：這讓遠端 sudo 能控制 echo，且能正確接收按鍵)
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("無法設定 Raw Terminal: %v", err)
		}
		defer term.Restore(fd, oldState)
	}

	if err := session.Run(cmd); err != nil {
		// 在 Raw mode 下錯誤訊息可能顯示異常，但在 restore 後應正常
		return fmt.Errorf("指令執行失敗: %v", err)
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
