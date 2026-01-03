package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the top-level structure of autonfs.yaml
type Config struct {
	Hosts []HostConfig `yaml:"hosts"`
}

// HostConfig defines the configuration for a single NFS connection
type HostConfig struct {
	Alias       string        `yaml:"alias"`        // SSH Alias or Hostname
	Mounts      []MountConfig `yaml:"mounts"`       // List of mounts
	IdleTimeout string        `yaml:"idle_timeout"` // Default idle timeout for this host (e.g., "5m")
	WakeTimeout string        `yaml:"wake_timeout"` // Timeout for WoL/Wake (e.g., "120s")
	ShutdownCmd string        `yaml:"shutdown_cmd"` // Custom shutdown command
}

// MountConfig defines a single directory mapping
type MountConfig struct {
	Local   string `yaml:"local"`   // Local mount point
	Remote  string `yaml:"remote"`  // Remote export path
	Options string `yaml:"options"` // Mount options (e.g. "rw,soft,timeo=100")
}

// ParseConfig parses YAML content into a Config struct
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate ensures the configuration is valid
func (c *Config) Validate() error {
	if len(c.Hosts) == 0 {
		return fmt.Errorf("no hosts defined in config")
	}

	for i, host := range c.Hosts {
		if host.Alias == "" {
			return fmt.Errorf("host #%d missing alias", i)
		}
		if len(host.Mounts) == 0 {
			return fmt.Errorf("host %s has no mounts defined", host.Alias)
		}
		for j, m := range host.Mounts {
			if m.Local == "" {
				return fmt.Errorf("host %s mount #%d missing local path", host.Alias, j)
			}
			if m.Remote == "" {
				return fmt.Errorf("host %s mount #%d missing remote path", host.Alias, j)
			}
		}
		// Validate duration strings if present
		if host.IdleTimeout != "" {
			if _, err := time.ParseDuration(host.IdleTimeout); err != nil {
				return fmt.Errorf("host %s invalid idle_timeout: %v", host.Alias, err)
			}
		}
		if host.WakeTimeout != "" {
			if _, err := time.ParseDuration(host.WakeTimeout); err != nil {
				return fmt.Errorf("host %s invalid wake_timeout: %v", host.Alias, err)
			}
		}
	}
	return nil
}
