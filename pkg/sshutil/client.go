package sshutil

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Client 封裝 SSH 連線資訊
type Client struct {
	Alias string
	Host  string
	Port  string
	User  string
	Key   string
}

// NewClient 從 ~/.ssh/config 解析並回傳 Client 物件
func NewClient(alias string) (*Client, error) {
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
		// 如果沒有 HostName，可能使用者直接輸入了 IP 或 Alias 就是 HostName
		host = alias
	}

	user, _ := cfg.Get(alias, "User")
	if user == "" {
		user = os.Getenv("USER") // 預設使用當前使用者
	}

	port, _ := cfg.Get(alias, "Port")
	if port == "" {
		port = "22"
	}

	key, _ := cfg.Get(alias, "IdentityFile")
	// ssh_config 預設回傳 "~/.ssh/id_rsa"，如果找不到則回傳空字串或預設值
	// 我們這裡先保留路徑處理邏輯

	return &Client{
		Alias: alias,
		Host:  host,
		Port:  port,
		User:  user,
		Key:   key,
	}, nil
}

// RunCommand 建立連線並執行單一指令
func (c *Client) RunCommand(cmd string) (string, error) {
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

	// 添加 Config 指定的 Key
	if c.Key != "" && c.Key != "~/.ssh/identity" { 
		keyFiles = append(keyFiles, expandPath(c.Key))
	}

	// 添加預設 Keys (Fallback)
	defaultKeys := []string{
		filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa"),
		filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519"),
		filepath.Join(os.Getenv("HOME"), ".ssh", "id_ecdsa"),
	}
	
	// 如果沒有指定 Key，或是為了最大相容性，我們嘗試載入存在的預設 Key
	for _, dk := range defaultKeys {
		if _, err := os.Stat(dk); err == nil {
			keyFiles = append(keyFiles, dk)
		}
	}

	// 載入所有找到的 Private Keys
	for _, kPath := range keyFiles {
		key, err := ioutil.ReadFile(kPath)
		if err == nil {
			signer, err := ssh.ParsePrivateKey(key)
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}

	// SSH Client Config
	config := &ssh.ClientConfig{
		User:            c.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Phase 1: 暫時忽略 Host Key 檢查 (TODO: Fix this for security)
		Timeout:         5 * time.Second,
	}

	// 連線
	addr := fmt.Sprintf("%s:%s", c.Host, c.Port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return "", fmt.Errorf("SSH 連線失敗 [%s]: %v", addr, err)
	}
	defer client.Close()

	// 建立 Session
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("無法建立 Session: %v", err)
	}
	defer session.Close()

	// 執行指令
	output, err := session.CombinedOutput(cmd)
	if err != nil {
		return string(output), fmt.Errorf("執行指令失敗: %v\nOutput: %s", err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(os.Getenv("HOME"), path[2:])
	}
	return path
}