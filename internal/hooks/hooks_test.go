package hooks

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureLog redirects the standard logger into a buffer for the duration of
// fn and returns everything that was logged.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	flags := log.Flags()
	out := log.Writer()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(out)
		log.SetFlags(flags)
	}()
	fn()
	return buf.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFileIsNoOp(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("missing hooks file should not error: %v", err)
	}
	if len(f.Events) != 0 {
		t.Fatalf("expected no events, got %v", f.Events)
	}
}

func TestLoadParsesBothEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	writeFile(t, path, `{
		"events": {
			"Boot": [
				{"type": "command", "command": "echo one", "timeout": 5, "statusMessage": "first"},
				{"type": "command", "command": "echo two"}
			],
			"Shutdown": [
				{"type": "command", "command": "echo bye"}
			]
		}
	}`)
	f, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := len(f.Events[EventBoot]); got != 2 {
		t.Fatalf("Boot hooks = %d, want 2", got)
	}
	if got := len(f.Events[EventShutdown]); got != 1 {
		t.Fatalf("Shutdown hooks = %d, want 1", got)
	}
	if f.Events[EventBoot][0].StatusMessage != "first" || f.Events[EventBoot][0].Timeout != 5 {
		t.Fatalf("first boot hook parsed wrong: %+v", f.Events[EventBoot][0])
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	writeFile(t, path, `{"events": {`)
	if _, err := Load(path); err == nil {
		t.Fatal("malformed JSON must error")
	}
}

func TestLoadUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	writeFile(t, path, `{"events": {"Boot": [{"type": "command", "command": "x", "cwd": "/tmp"}]}}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field must error, got %v", err)
	}
}

func TestLoadUnknownEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	writeFile(t, path, `{"events": {"OnCrash": [{"type": "command", "command": "x"}]}}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown event") {
		t.Fatalf("unknown event must error, got %v", err)
	}
}

func TestLoadWrongType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	writeFile(t, path, `{"events": {"Boot": [{"type": "script", "command": "x"}]}}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("non-command type must error, got %v", err)
	}
}

func TestLoadEmptyCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	writeFile(t, path, `{"events": {"Boot": [{"type": "command", "command": ""}]}}`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "\"command\" is required") {
		t.Fatalf("empty command must error, got %v", err)
	}
}

func TestBootOrderAndMarkers(t *testing.T) {
	dir := t.TempDir()
	f, err := Load(BootFile("config.json")) // relative path → same-dir hooks/
	if err != nil {
		t.Fatal(err)
	}
	f.Events[EventBoot] = []Hook{
		{Type: "command", Command: "echo a > boot.order && echo 1 >> boot.order"},
		{Type: "command", Command: "echo 2 >> boot.order"},
	}
	captureLog(t, func() { RunBoot(context.Background(), dir, f) })

	data, err := os.ReadFile(filepath.Join(dir, "boot.order"))
	if err != nil {
		t.Fatalf("boot hooks did not run in dir: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "a\n1\n2" {
		t.Fatalf("boot order = %q, want a,1,2 in declared order", got)
	}
}

func TestShutdownRunsReverseOrder(t *testing.T) {
	dir := t.TempDir()
	f := &File{Events: map[Event][]Hook{
		EventShutdown: {
			{Type: "command", Command: "echo first >> shut.order"},
			{Type: "command", Command: "echo second >> shut.order"},
			{Type: "command", Command: "echo third >> shut.order"},
		},
	}}
	captureLog(t, func() { RunShutdown(context.Background(), dir, f) })

	data, err := os.ReadFile(filepath.Join(dir, "shut.order"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(data))
	want := []string{"third", "second", "first"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("shutdown order = %v, want reverse %v", got, want)
	}
}

func TestFailSoftContinuesAfterFailure(t *testing.T) {
	dir := t.TempDir()
	f := &File{Events: map[Event][]Hook{
		EventBoot: {
			{Type: "command", Command: "exit 7"},
			{Type: "command", Command: "echo survived > after-fail.marker"},
		},
	}}
	logged := captureLog(t, func() { RunBoot(context.Background(), dir, f) })

	if _, err := os.Stat(filepath.Join(dir, "after-fail.marker")); err != nil {
		t.Fatalf("hook after a failing hook must still run: %v", err)
	}
	if !strings.Contains(logged, "failed") {
		t.Fatalf("failure should be logged, got: %s", logged)
	}
}

func TestTimeoutKillsHangingHook(t *testing.T) {
	dir := t.TempDir()
	f := &File{Events: map[Event][]Hook{
		EventBoot: {
			{Type: "command", Command: "sleep 30", Timeout: 1},
		},
	}}
	logged := captureLog(t, func() { RunBoot(context.Background(), dir, f) })
	if !strings.Contains(logged, "timed out") {
		t.Fatalf("timeout should be logged, got: %s", logged)
	}
}

func TestHookSeesConfigDirAndLogsStatusMessage(t *testing.T) {
	dir := t.TempDir()
	f := &File{Events: map[Event][]Hook{
		EventBoot: {
			{Type: "command", Command: "pwd > cwd.txt", StatusMessage: "resolve working dir"},
		},
	}}
	logged := captureLog(t, func() { RunBoot(context.Background(), dir, f) })

	data, err := os.ReadFile(filepath.Join(dir, "cwd.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(data)), dir; got != want {
		t.Fatalf("hook cwd = %q, want %q", got, want)
	}
	if !strings.Contains(logged, "resolve working dir") || !strings.Contains(logged, "hook[Boot 1/1]") {
		t.Fatalf("statusMessage and numbering should appear in log, got: %s", logged)
	}
}

func TestScriptPathWithShebang(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "up.sh")
	writeFile(t, script, "#!/bin/sh\necho from-script > script.marker\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &File{Events: map[Event][]Hook{
		EventBoot: {{Type: "command", Command: script}},
	}}
	captureLog(t, func() { RunBoot(context.Background(), dir, f) })

	if _, err := os.Stat(filepath.Join(dir, "script.marker")); err != nil {
		t.Fatalf("script hook did not run: %v", err)
	}
}

func TestChainingAndPipes(t *testing.T) {
	dir := t.TempDir()
	f := &File{Events: map[Event][]Hook{
		EventBoot: {
			{Type: "command", Command: "echo hello | tr a-z A-Z > piped.txt && echo done >> piped.txt"},
		},
	}}
	captureLog(t, func() { RunBoot(context.Background(), dir, f) })

	data, err := os.ReadFile(filepath.Join(dir, "piped.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(string(data)), []string{"HELLO", "done"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("pipe/chain result = %v, want %v", got, want)
	}
}

func TestBootFileLocation(t *testing.T) {
	got := BootFile("/home/u/.ahoy/config.json")
	want := "/home/u/.ahoy/hooks/hooks.json"
	if got != want {
		t.Fatalf("BootFile = %q, want %q", got, want)
	}
}

func TestNoHooksIsSilent(t *testing.T) {
	f := &File{}
	start := time.Now()
	logged := captureLog(t, func() {
		RunBoot(context.Background(), t.TempDir(), f)
		RunShutdown(context.Background(), t.TempDir(), f)
	})
	if logged != "" {
		t.Fatalf("no hooks should log nothing, got: %s", logged)
	}
	if time.Since(start) > time.Second {
		t.Fatal("no-op run should be instant")
	}
}
