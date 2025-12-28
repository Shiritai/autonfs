package discover

import "testing"

func TestParseNetworkInfo(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantIf  string
		wantIP  string
		wantMac string
		wantErr bool
	}{
		{
			name:    "Normal case",
			input:   "eth0|192.168.1.50|aa:bb:cc:dd:ee:ff",
			wantIf:  "eth0",
			wantIP:  "192.168.1.50",
			wantMac: "aa:bb:cc:dd:ee:ff",
			wantErr: false,
		},
		{
			name:    "With newline",
			input:   "enp3s0|10.0.0.1|00:11:22:33:44:55\n",
			wantIf:  "enp3s0",
			wantIP:  "10.0.0.1",
			wantMac: "00:11:22:33:44:55",
			wantErr: false,
		},
		{
			name:    "Missing fields",
			input:   "eth0|192.168.1.50",
			wantErr: true,
		},
		{
			name:    "Empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "Extra fields",
			input:   "eth0|192.168.1.50|aa:bb:cc|extra",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIf, gotIP, gotMac, err := parseNetworkInfo(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseNetworkInfo() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if gotIf != tt.wantIf {
					t.Errorf("parseNetworkInfo() gotIf = %v, want %v", gotIf, tt.wantIf)
				}
				if gotIP != tt.wantIP {
					t.Errorf("parseNetworkInfo() gotIP = %v, want %v", gotIP, tt.wantIP)
				}
				if gotMac != tt.wantMac {
					t.Errorf("parseNetworkInfo() gotMac = %v, want %v", gotMac, tt.wantMac)
				}
			}
		})
	}
}
