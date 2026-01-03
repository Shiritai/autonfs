package templates

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	cfg := Config{
		ServerIP:      "192.168.1.50",
		ClientIP:      "192.168.1.100",
		MacAddr:       "AA:BB:CC:DD:EE:FF",
		RemoteDir:     "/data",
		LocalDir:      "/mnt/data",
		BinaryPath:    "/usr/bin/autonfs",
		IdleTimeout:   "10m",
		LoadThreshold: "0.8",
		Exports: []ExportInfo{
			{Path: "/data", ClientIP: "192.168.1.100"},
		},
	}

	tests := []struct {
		name     string
		tmplName string
		tmpl     string
		want     []string // Substrings that must appear
	}{
		{
			name:     "ClientMount",
			tmplName: "mount",
			tmpl:     ClientMountTmpl,
			want: []string{
				"Description=AutoNFS Mount for /data",
				"What=192.168.1.50:/data",
				"Where=/mnt/data",
				"ExecStartPre=/usr/bin/autonfs wake --mac \"AA:BB:CC:DD:EE:FF\" --ip \"192.168.1.50\"",
			},
		},
		{
			name:     "ServerService",
			tmplName: "service",
			tmpl:     ServerServiceTmpl,
			want: []string{
				"ExecStart=/usr/bin/autonfs watch --timeout 10m --load 0.8",
			},
		},
		{
			name:     "ServerExports",
			tmplName: "exports",
			tmpl:     ServerExportsTmpl,
			want: []string{
				"/data 192.168.1.100(rw,sync,no_subtree_check,no_root_squash)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBytes, err := Render(tt.tmplName, tt.tmpl, cfg)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			got := string(gotBytes)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("Render() result missing expected string: %q", w)
					t.Logf("Got:\n%s", got)
				}
			}
		})
	}
}
