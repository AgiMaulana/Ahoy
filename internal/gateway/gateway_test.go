package gateway

import (
	"reflect"
	"testing"

	"github.com/AgiMaulana/mcpgateway/internal/config"
)

func TestStdioCommandWithoutInject(t *testing.T) {
	s := &Server{Config: config.ServerConfig{Command: "npx", Args: []string{"-y", "server"}}}
	cmd, args := s.stdioCommand()
	if cmd != "npx" || !reflect.DeepEqual(args, []string{"-y", "server"}) {
		t.Fatalf("stdioCommand() = %q %v", cmd, args)
	}
}

func TestStdioCommandWithInject(t *testing.T) {
	s := &Server{Config: config.ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "server"},
		Env:     []string{"A", "B"},
	}}
	cmd, args := s.stdioCommand()
	if cmd != "xenv" {
		t.Fatalf("cmd = %q, want xenv", cmd)
	}
	want := []string{"inject", "A", "B", "--", "npx", "-y", "server"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestStdioCommandWithSecretFile(t *testing.T) {
	s := &Server{Config: config.ServerConfig{
		Command:    "node",
		Args:       []string{"srv.js"},
		Env:        []string{"TOKEN"},
		SecretFile: ".env.production",
	}}
	cmd, args := s.stdioCommand()
	if cmd != "xenv" {
		t.Fatalf("cmd = %q, want xenv", cmd)
	}
	want := []string{"-i", ".env.production", "inject", "TOKEN", "--", "node", "srv.js"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}
