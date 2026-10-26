package templates

import (
	"bytes"
	"text/template"
)

// We define five core templates:
// 1. Client Mount: Defines NFS mount parameters; requires the Client Wake unit
// 2. Client Wake: Sends Wake-on-LAN and waits for NFS before each mount
// 3. Client Automount: Defines on-demand mount behavior
// 4. Server Service: Defines the idle watcher service
// 5. Server Exports: Defines NFS export configuration

// systemd ignores Exec* keys in [Mount], so the wake step lives in its own
// unit that the mount requires and is ordered after.
const ClientMountTmpl = `[Unit]
Description=AutoNFS Mount for {{.RemoteDir}}
Requires={{.WakeUnit}}
After={{.WakeUnit}}

[Mount]
What={{.ServerIP}}:{{.RemoteDir}}
Where={{.LocalDir}}
Type=nfs
Options={{.MountOptions}}
`

// Note: [Install] section removed from Mount unit to prevent enabling it directly.
// It should only be activated by the Automount unit on demand.

// RemainAfterExit keeps the wake "active" while its mount needs it, and
// StopWhenUnneeded stops it once the mount goes away, so every new mount
// (e.g. after an idle unmount and server shutdown) wakes the server again.
// The wake command enforces --timeout itself, hence no start timeout here.
const ClientWakeTmpl = `[Unit]
Description=AutoNFS Wake for {{.ServerIP}} ({{.LocalDir}})
Wants=network-online.target
After=network-online.target
StopWhenUnneeded=yes

[Service]
Type=oneshot
RemainAfterExit=yes
TimeoutStartSec=infinity
ExecStart={{.BinaryPath}} wake --mac "{{.MacAddr}}" --ip "{{.ServerIP}}" --port 2049 --timeout {{.WakeTimeout}}
`

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
ExecStart={{.BinaryPath}} watch --timeout {{.IdleTimeout}} --load {{.LoadThreshold}}{{if .WatcherDryRun}} --dry-run{{end}}{{if .ShutdownCmd}} --shutdown-cmd "{{.ShutdownCmd}}"{{end}}
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`

const ServerExportsTmpl = `{{range .Exports}}
{{.Path}} {{.ClientIP}}(rw,sync,no_subtree_check,no_root_squash)
{{end}}`

// ExportInfo defines NFS Share information
type ExportInfo struct {
	Path     string
	ClientIP string
}

// Config defines variables for template rendering
type Config struct {
	ServerIP      string
	ClientIP      string // Keep for Single-Mount templates usage if needed
	MacAddr       string
	RemoteDir     string // Keep for valid fields in ClientMountTmpl
	LocalDir      string // Keep for valid fields in ClientMountTmpl
	BinaryPath    string
	IdleTimeout   string
	WakeTimeout   string
	WakeUnit      string // Unit name of the Client Wake service a mount requires
	LoadThreshold string
	WatcherDryRun bool
	ShutdownCmd   string       // New field
	MountOptions  string       // New field
	Exports       []ExportInfo // New field for multi-export
}

// Render helper function
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
