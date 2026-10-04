// Package config loads the gateway's downstream MCP server definitions.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Transport identifies how the gateway talks to a downstream MCP server.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
)

// AuthType identifies how the gateway authenticates to an HTTP server.
type AuthType string

const (
	// AuthOAuth performs an interactive browser OAuth login (see "ahoy login")
	// and stores the resulting token; the gateway then reuses it.
	AuthOAuth AuthType = "oauth"
)

// ServerConfig describes a single downstream MCP server.
type ServerConfig struct {
	Description string            `json:"description,omitempty"`
	Transport   Transport         `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Auth        AuthType          `json:"auth,omitempty"`
}

// Config is the top-level gateway configuration.
type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

// Load reads and validates a JSON configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &cfg, nil
}

// Validate checks that every server has a usable transport. An empty server set
// is allowed so the CLI can build and edit a config incrementally; the serving
// path rejects a config with no servers.
func (c *Config) Validate() error {
	for name, s := range c.Servers {
		if s.Auth != "" {
			if s.Transport != TransportHTTP {
				return fmt.Errorf("server %q: auth applies to http transport only", name)
			}
			if s.Auth != AuthOAuth {
				return fmt.Errorf("server %q: unsupported auth %q (only %q)", name, s.Auth, AuthOAuth)
			}
		}
		switch s.Transport {
		case TransportStdio:
			if s.Command == "" {
				return fmt.Errorf("server %q: stdio transport requires \"command\"", name)
			}
		case TransportHTTP:
			if s.URL == "" {
				return fmt.Errorf("server %q: http transport requires \"url\"", name)
			}
		case "":
			return fmt.Errorf("server %q: transport is required (stdio or http)", name)
		default:
			return fmt.Errorf("server %q: unsupported transport %q", name, s.Transport)
		}
	}
	return nil
}

// Save writes cfg to path atomically (temp file + rename) so a crash never
// leaves a half-written config. The file is created with 0600 permissions
// because a config can hold secrets such as API tokens and env values; an
// existing file keeps its current mode.
func Save(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')

	perm := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config %s: %w", path, err)
	}
	return nil
}
