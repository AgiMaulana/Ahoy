// Package config loads the gateway's downstream MCP server definitions.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Transport identifies how the gateway talks to a downstream MCP server.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
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
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.Servers) == 0 {
		return fmt.Errorf("no servers defined")
	}
	for name, s := range c.Servers {
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
