// Command downstream-demo is a tiny stdio MCP server used to exercise the
// gateway: it exposes an "echo" tool and an "add" tool, plus "pid" and
// "self_destruct" for testing reconnection.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	log.SetOutput(os.Stderr)

	s := server.NewMCPServer("downstream-demo", "0.1.0")

	s.AddTool(
		mcp.NewTool("echo",
			mcp.WithDescription("Echo back the provided text."),
			mcp.WithString("text", mcp.Required(), mcp.Description("Text to echo.")),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(req.GetString("text", "")), nil
		},
	)

	s.AddTool(
		mcp.NewTool("add",
			mcp.WithDescription("Add two integers."),
			mcp.WithNumber("a", mcp.Required(), mcp.Description("First addend.")),
			mcp.WithNumber("b", mcp.Required(), mcp.Description("Second addend.")),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sum := req.GetInt("a", 0) + req.GetInt("b", 0)
			return mcp.NewToolResultText(fmt.Sprintf("%d", sum)), nil
		},
	)

	s.AddTool(
		mcp.NewTool("pid",
			mcp.WithDescription("Return the process id of this server."),
		),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(fmt.Sprintf("%d", os.Getpid())), nil
		},
	)

	s.AddTool(
		mcp.NewTool("self_destruct",
			mcp.WithDescription("Return, then exit so the gateway must reconnect."),
		),
		func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			go func() {
				time.Sleep(100 * time.Millisecond)
				os.Exit(0)
			}()
			return mcp.NewToolResultText("dying"), nil
		},
	)

	if err := server.ServeStdio(s); err != nil {
		log.Fatalf("downstream-demo error: %v", err)
	}
}
