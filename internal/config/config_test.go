package config

import (
	"testing"
)

func TestParseConfig_Valid(t *testing.T) {
	yamlData := `
hosts:
  - alias: "nas"
    idle_timeout: "5m"
    wake_timeout: "120s"
    mounts:
      - local: "/mnt/data"
        remote: "/volume1/data"
      - local: "/mnt/backup"
        remote: "/volume1/backup"
`
	cfg, err := ParseConfig([]byte(yamlData))
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}

	if len(cfg.Hosts) != 1 {
		t.Errorf("Expected 1 host, got %d", len(cfg.Hosts))
	}

	host := cfg.Hosts[0]
	if host.Alias != "nas" {
		t.Errorf("Expected alias 'nas', got '%s'", host.Alias)
	}
	if host.IdleTimeout != "5m" {
		t.Errorf("Expected idle_timeout '5m', got '%s'", host.IdleTimeout)
	}

	if len(host.Mounts) != 2 {
		t.Errorf("Expected 2 mounts, got %d", len(host.Mounts))
	}
	if host.Mounts[0].Local != "/mnt/data" {
		t.Errorf("Mount 0 local mismatch")
	}
}

func TestParseConfig_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "missing host alias",
			yaml: `
hosts:
  - mounts: [{local: /a, remote: /b}]
`,
			wantErr: true,
		},
		{
			name: "missing mounts",
			yaml: `
hosts:
  - alias: nas
`,
			wantErr: true,
		},
		{
			name: "invalid timeout",
			yaml: `
hosts:
  - alias: nas
    idle_timeout: "invalid"
    mounts: [{local: /a, remote: /b}]
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tt.yaml))
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
