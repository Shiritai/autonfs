package deployer

import (
	"strings"
	"testing"
)

func TestEscapeSystemdPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/mnt/data", "mnt-data"},
		{"mnt/data", "mnt-data"},
		{"/var/lib/my-app", "var-lib-my\\x2dapp"},
		{"/home/user/nc-disk/data", "home-user-nc\\x2ddisk-data"}, // Real systemd escaping
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

// TestGetOutboundIP_SmokeTest checks if it returns a valid IP-like string
func TestGetOutboundIP(t *testing.T) {
	ip := getOutboundIP("8.8.8.8")
	if ip == "" {
		t.Error("getOutboundIP returned empty string")
	}
	if strings.Count(ip, ".") != 3 {
		t.Errorf("getOutboundIP returned invalid format: %s", ip)
	}
}

// To verify RunDeploy, we rely on DryRun mode to avoid side effects.
// However, since RunDeploy calls sshutil.NewClient (which tries to parse ~/.ssh/config),
// this test might require a valid ~/.ssh/config or fail if the alias is not found.
// We can mock the sshutil part if we refactor, but for now let's skip specific Orchestration logic
// unless we have a strong need to refactor `sshutil` dependency injection.
// The DryRun test is valuable but fragile in this specific "config-less" design without deep mocks.
