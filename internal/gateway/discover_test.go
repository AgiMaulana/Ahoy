package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/AgiMaulana/Ahoy/internal/config"
)

func seededGateway() *Gateway {
	g := New(&config.Config{Servers: map[string]config.ServerConfig{
		"github": {Description: "GitHub repos, issues and pull requests"},
		"slack":  {Description: "Slack messages and channels"},
	}})
	g.servers["github"].tools = []mcp.Tool{
		{Name: "get_pull_request", Description: "Get details of a pull request"},
		{Name: "create_issue", Description: "Create a new issue in a repository"},
	}
	g.servers["slack"].tools = []mcp.Tool{
		{Name: "send_message", Description: "Send a message to a channel"},
	}
	return g
}

func TestDiscoverServerList(t *testing.T) {
	got := seededGateway().Discover("")
	if !strings.Contains(got, "github") || !strings.Contains(got, "[2 tools]") {
		t.Fatalf("discover() = %q", got)
	}
	if strings.Contains(got, "get_pull_request") {
		t.Fatalf("server list must not leak tool names: %q", got)
	}
}

func TestDiscoverServerTools(t *testing.T) {
	got := seededGateway().Discover("github")
	if !strings.Contains(got, "get_pull_request") || !strings.Contains(got, "create_issue") {
		t.Fatalf("discover(github) = %q", got)
	}
	if strings.Contains(got, "Input schema") {
		t.Fatalf("server listing should not include schemas: %q", got)
	}
}

func TestDiscoverToolSchema(t *testing.T) {
	got := seededGateway().Discover("github.get_pull_request")
	if !strings.Contains(got, "Input schema") || !strings.Contains(got, "github.get_pull_request") {
		t.Fatalf("discover(schema) = %q", got)
	}
}

func TestDiscoverSearch(t *testing.T) {
	got := seededGateway().Discover("pull request")
	if !strings.Contains(got, "github.get_pull_request") {
		t.Fatalf("discover(search) = %q", got)
	}
}

func TestDiscoverNoMatch(t *testing.T) {
	got := seededGateway().Discover("zzz-does-not-exist")
	if !strings.Contains(got, "No tools or servers match") {
		t.Fatalf("discover(no match) = %q", got)
	}
}

func TestInvokeUnknownServer(t *testing.T) {
	if _, err := seededGateway().Invoke(context.Background(), "nope", "tool", nil); err == nil {
		t.Fatal("want error for unknown server")
	}
}
