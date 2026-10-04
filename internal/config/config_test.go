package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := &Config{Servers: map[string]ServerConfig{
		"remote": {
			Transport: TransportHTTP,
			URL:       "https://mcp.sentry.dev/mcp",
			Headers:   map[string]string{"Authorization": "Bearer x"},
		},
		"local": {
			Description: "demo",
			Transport:   TransportStdio,
			Command:     "npx",
			Args:        []string{"-y", "pkg"},
			Env:         map[string]string{"K": "V"},
		},
	}}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(got.Servers))
	}
	if got.Servers["remote"].URL != "https://mcp.sentry.dev/mcp" {
		t.Errorf("remote url not preserved: %+v", got.Servers["remote"])
	}
	if got.Servers["remote"].Headers["Authorization"] != "Bearer x" {
		t.Errorf("remote header not preserved: %+v", got.Servers["remote"])
	}
	local := got.Servers["local"]
	if local.Command != "npx" || len(local.Args) != 2 || local.Env["K"] != "V" {
		t.Errorf("local not preserved: %+v", local)
	}
}

func TestSavePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Save(path, &Config{Servers: map[string]ServerConfig{}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
}

func TestSaveKeepsExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"servers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, &Config{Servers: map[string]ServerConfig{}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("perm = %o, want 644 (preserved)", perm)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		servers map[string]ServerConfig
		wantErr bool
	}{
		{"empty is allowed", map[string]ServerConfig{}, false},
		{"stdio ok", map[string]ServerConfig{"s": {Transport: TransportStdio, Command: "x"}}, false},
		{"stdio missing command", map[string]ServerConfig{"s": {Transport: TransportStdio}}, true},
		{"http ok", map[string]ServerConfig{"s": {Transport: TransportHTTP, URL: "u"}}, false},
		{"oauth ok", map[string]ServerConfig{"s": {Transport: TransportHTTP, URL: "u", Auth: AuthOAuth}}, false},
		{"http missing url", map[string]ServerConfig{"s": {Transport: TransportHTTP}}, true},
		{"auth on stdio", map[string]ServerConfig{"s": {Transport: TransportStdio, Command: "x", Auth: AuthOAuth}}, true},
		{"unsupported auth", map[string]ServerConfig{"s": {Transport: TransportHTTP, URL: "u", Auth: "basic"}}, true},
		{"missing transport", map[string]ServerConfig{"s": {}}, true},
		{"unsupported transport", map[string]ServerConfig{"s": {Transport: "sse"}}, true},
	}
	for _, tc := range cases {
		err := (&Config{Servers: tc.servers}).Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: Validate() err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}
