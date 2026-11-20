package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test directory")
		}
		dir = parent
	}
}

func buildBinary(t *testing.T, root, binDir, pkg, name string) string {
	t.Helper()
	out := filepath.Join(binDir, name)
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, b)
	}
	return out
}

func demoConfig(demoBin string) string {
	return `{"servers":{"demo":{"description":"Demo server","transport":"stdio","command":"` + demoBin + `"}}}`
}

// startGateway builds the gateway binary, writes cfgJSON to a config file, and
// returns an initialized stdio client connected to it.
func startGateway(t *testing.T, root, binDir, cfgJSON string) *client.Client {
	t.Helper()
	gwBin := buildBinary(t, root, binDir, "./cmd/ahoy", "ahoy")
	cfgPath := filepath.Join(binDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	gw, err := client.NewStdioMCPClient(gwBin, nil, "-config", cfgPath)
	if err != nil {
		t.Fatalf("spawn gateway: %v", err)
	}
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := gw.Start(ctx); err != nil {
		t.Fatal(err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "smoke", Version: "0"}
	if _, err := gw.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initialize gateway: %v", err)
	}
	return gw
}

func TestGatewayEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end test in short mode")
	}

	root := moduleRoot(t)
	binDir := t.TempDir()
	demoBin := buildBinary(t, root, binDir, "./cmd/downstream-demo", "downstream-demo")
	gw := startGateway(t, root, binDir, demoConfig(demoBin))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tools, err := gw.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("gateway should expose exactly 2 tools, got %d", len(tools.Tools))
	}

	if got := callText(t, ctx, gw, "discover", map[string]any{}); !strings.Contains(got, "demo") {
		t.Fatalf("discover() = %q, want mention of demo", got)
	}
	if got := callText(t, ctx, gw, "discover", map[string]any{"query": "demo"}); !strings.Contains(got, "echo") || !strings.Contains(got, "add") {
		t.Fatalf("discover(demo) = %q, want echo and add", got)
	}
	if got := callText(t, ctx, gw, "invoke", map[string]any{
		"server":    "demo",
		"tool":      "add",
		"arguments": map[string]any{"a": 2, "b": 3},
	}); !strings.Contains(got, "5") {
		t.Fatalf("invoke(add, 2, 3) = %q, want 5", got)
	}
}

func TestGatewayReconnectsAfterDownstreamDeath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end test in short mode")
	}

	root := moduleRoot(t)
	binDir := t.TempDir()
	demoBin := buildBinary(t, root, binDir, "./cmd/downstream-demo", "downstream-demo")
	gw := startGateway(t, root, binDir, demoConfig(demoBin))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	firstPID := callText(t, ctx, gw, "invoke", map[string]any{"server": "demo", "tool": "pid"})
	if firstPID == "" {
		t.Fatal("demo server returned an empty pid")
	}

	if got := callText(t, ctx, gw, "invoke", map[string]any{"server": "demo", "tool": "self_destruct"}); !strings.Contains(got, "dying") {
		t.Fatalf("self_destruct = %q, want dying", got)
	}

	// The next pid call must transparently reconnect to a fresh process.
	deadline := time.Now().Add(15 * time.Second)
	for {
		if pid := callText(t, ctx, gw, "invoke", map[string]any{"server": "demo", "tool": "pid"}); pid != "" && pid != firstPID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("downstream pid stayed %q; expected a reconnected process", firstPID)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestGatewayLifecycleHooks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end test in short mode")
	}

	root := moduleRoot(t)
	binDir := t.TempDir()
	demoBin := buildBinary(t, root, binDir, "./cmd/downstream-demo", "downstream-demo")

	// Hooks resolve to <dir-of-config>/hooks/hooks.json. The commands use
	// relative paths on purpose: they must run with the config directory as
	// their working directory. The boot hook also chains and pipes to prove
	// shell semantics reach the hook.
	hooksJSON := `{
		"events": {
			"Boot": [
				{"type": "command", "command": "echo hello | tr a-z A-Z > boot.marker",
				 "statusMessage": "boot marker"},
				{"type": "command", "command": "echo second >> boot.marker"}
			],
			"Shutdown": [
				{"type": "command", "command": "echo bye > shutdown.marker"}
			]
		}
	}`
	if err := os.MkdirAll(filepath.Join(binDir, "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "hooks", "hooks.json"), []byte(hooksJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	gw := startGateway(t, root, binDir, demoConfig(demoBin))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// startGateway completed MCP initialize, which happens after ConnectAll,
	// so the boot hooks must have run by now — in order, in the config dir.
	data, err := os.ReadFile(filepath.Join(binDir, "boot.marker"))
	if err != nil {
		t.Fatalf("boot hook did not create boot.marker: %v", err)
	}
	if got, want := strings.Fields(string(data)), []string{"HELLO", "second"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("boot.marker = %v, want %v (ordered)", got, want)
	}

	if got := callText(t, ctx, gw, "invoke", map[string]any{
		"server":    "demo",
		"tool":      "add",
		"arguments": map[string]any{"a": 1, "b": 2},
	}); !strings.Contains(got, "3") {
		t.Fatalf("invoke(add, 1, 2) = %q, want 3 — gateway must serve normally after boot hooks", got)
	}

	// Closing the client closes the gateway's stdin, which ends ServeStdio and
	// must run the shutdown hooks before the process exits.
	_ = gw.Close()

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(binDir, "shutdown.marker")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("shutdown hook did not create shutdown.marker after gateway exit")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func callText(t *testing.T, ctx context.Context, c *client.Client, name string, args map[string]any) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var sb strings.Builder
	for _, content := range res.Content {
		switch v := content.(type) {
		case mcp.TextContent:
			sb.WriteString(v.Text)
		case *mcp.TextContent:
			sb.WriteString(v.Text)
		}
	}
	return sb.String()
}
