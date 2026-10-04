package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultPathHonorsAhoyHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AHOY_HOME", home)

	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "config.json"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestResolve(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AHOY_HOME", home)
	t.Chdir(t.TempDir())

	if _, err := Resolve(""); err == nil {
		t.Fatal("Resolve with no config anywhere should error")
	}

	// A cwd-local config is used when no per-user config exists.
	if err := os.WriteFile("config.json", []byte(`{"servers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(""); err != nil || got != "config.json" {
		t.Fatalf("Resolve() = %q, %v; want config.json", got, err)
	}

	// The per-user config wins over the local one.
	homeCfg := filepath.Join(home, "config.json")
	if err := os.WriteFile(homeCfg, []byte(`{"servers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(""); err != nil || got != homeCfg {
		t.Fatalf("Resolve() = %q, %v; want %q", got, err, homeCfg)
	}

	// An explicit path always wins, existing or not.
	if got, err := Resolve("explicit.json"); err != nil || got != "explicit.json" {
		t.Fatalf("Resolve(explicit) = %q, %v", got, err)
	}
}

func TestResolveForWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AHOY_HOME", home)
	t.Chdir(t.TempDir())

	// Nothing exists yet: create at the per-user path.
	want := filepath.Join(home, "config.json")
	if got, err := ResolveForWrite(""); err != nil || got != want {
		t.Fatalf("ResolveForWrite() = %q, %v; want %q", got, err, want)
	}

	// An existing local config is edited in place.
	if err := os.WriteFile("config.json", []byte(`{"servers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveForWrite(""); err != nil || got != "config.json" {
		t.Fatalf("ResolveForWrite() = %q, %v; want config.json", got, err)
	}

	if got, err := ResolveForWrite("custom.json"); err != nil || got != "custom.json" {
		t.Fatalf("ResolveForWrite(explicit) = %q, %v", got, err)
	}
}

func TestSaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	if err := Save(path, &Config{Servers: map[string]ServerConfig{
		"s": {Transport: TransportStdio, Command: "x"},
	}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(got.Servers))
	}
}
