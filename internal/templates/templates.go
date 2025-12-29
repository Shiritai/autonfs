package templates

import (
	"bytes"
	"text/template"
)

// 我們定義四個核心模板
// 1. Client Mount: 定義 NFS 掛載參數與喚醒鉤子 (ExecStartPre)
// 2. Client Automount: 定義按需掛載行為
// 3. Server Service: 定義看門狗服務
// 4. Server Exports: 定義 NFS 分享設定

const ClientMountTmpl = `[Unit]
Description=AutoNFS Mount for {{.RemoteDir}}
After=network.target

[Mount]
What={{.ServerIP}}:{{.RemoteDir}}
Where={{.LocalDir}}
Type=nfs
Options=rw,soft,timeo=100,retrans=3,actimeo=60
# 關鍵：掛載前先喚醒，設定 10 秒逾時避免卡死
ExecStartPre={{.BinaryPath}} wake --mac "{{.MacAddr}}" --ip "{{.ServerIP}}" --port 2049 --timeout 10s
`

// Note: [Install] section removed from Mount unit to prevent enabling it directly.
// It should only be activated by the Automount unit on demand.

const ClientAutomountTmpl = `[Unit]
Description=Automount for {{.LocalDir}}

[Automount]
Where={{.LocalDir}}
TimeoutIdleSec={{.IdleTimeout}}

[Install]
WantedBy=multi-user.target
`

const ServerServiceTmpl = `[Unit]
Description=AutoNFS Idle Watcher
After=network.target nfs-server.service

[Service]
Type=simple
ExecStart={{.BinaryPath}} watch --timeout {{.IdleTimeout}} --load {{.LoadThreshold}}{{if .WatcherDryRun}} --dry-run{{end}}
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`

const ServerExportsTmpl = `{{.RemoteDir}} {{.ClientIP}}(rw,sync,no_subtree_check,no_root_squash)
`

// Config 定義渲染模板所需的變數
type Config struct {
	ServerIP      string
	ClientIP      string
	MacAddr       string
	RemoteDir     string
	LocalDir      string
	BinaryPath    string // /usr/local/bin/autonfs
	IdleTimeout   string // e.g., "30m"
	WakeTimeout   string // e.g., "120s"
	LoadThreshold string // e.g., "0.5"
	WatcherDryRun bool   // 是否開啟 Watcher 的 DryRun 模式
}

// Render 輔助函式
func Render(name, tmplStr string, cfg Config) ([]byte, error) {
	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
