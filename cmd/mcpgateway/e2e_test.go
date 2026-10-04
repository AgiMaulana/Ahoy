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

func TestGatewayEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end test in short mode")
	}

	root := moduleRoot(t)
	binDir := t.TempDir()
	build := func(pkg, name string) string {
		out := filepath.Join(binDir, name)
		cmd := exec.Command("go", "build", "-o", out, pkg)
		cmd.Dir = root
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, b)
		}
		return out
	}

	gwBin := build("./cmd/mcpgateway", "mcpgateway")
	demoBin := build("./cmd/downstream-demo", "downstream-demo")

	cfgPath := filepath.Join(binDir, "config.json")
	cfg := `{"servers":{"demo":{"description":"Demo server","transport":"stdio","command":"` + demoBin + `"}}}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	gw, err := client.NewStdioMCPClient(gwBin, nil, "-config", cfgPath)
	if err != nil {
		t.Fatalf("spawn gateway: %v", err)
	}
	defer gw.Close()

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
