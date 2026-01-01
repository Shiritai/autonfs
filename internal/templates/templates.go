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

const ServerExportsTmpl = `{{range .Exports}}
{{.Path}} {{.ClientIP}}(rw,sync,no_subtree_check,no_root_squash)
{{end}}`

// ExportInfo 定義 NFS Share 資訊
type ExportInfo struct {
	Path     string
	ClientIP string
}

// Config 定義渲染模板所需的變數
type Config struct {
	ServerIP      string
	ClientIP      string // Keep for Single-Mount templates usage if needed
	MacAddr       string
	RemoteDir     string // Keep for valid fields in ClientMountTmpl
	LocalDir      string // Keep for valid fields in ClientMountTmpl
	BinaryPath    string
	IdleTimeout   string
	WakeTimeout   string
	LoadThreshold string
	WatcherDryRun bool
	Exports       []ExportInfo // New field for multi-export
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
