// Package mcpserver exposes the gateway to an agent as exactly two MCP tools:
// discover and invoke.
package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/AgiMaulana/mcpgateway/internal/gateway"
)

const instructions = `This server is an MCP Gateway. You do not have direct access to third-party MCP servers.

To use an external service:
1. discover()                          -> list connected servers (names only).
2. discover(query="<server>")          -> list that server's tools.
3. discover(query="<server>.<tool>")   -> full input schema for one tool.
4. invoke(server="<server>", tool="<tool>", arguments={...}) -> run it.

discover(query="<natural language>") also searches tool names and descriptions.
Discover before invoking if you are unsure of a tool's name or arguments.`

// New builds the gateway MCP server with the discover and invoke tools.
func New(gw *gateway.Gateway) *server.MCPServer {
	s := server.NewMCPServer(
		"mcpgateway",
		"0.1.0",
		server.WithInstructions(instructions),
		server.WithToolCapabilities(true),
	)

	s.AddTool(
		mcp.NewTool("discover",
			mcp.WithDescription("Discover MCP capabilities. No argument lists connected servers; a server name lists its tools; \"server.tool\" returns one tool's input schema; any other text searches tool names and descriptions."),
			mcp.WithString("query",
				mcp.Description("Server name, \"server.tool\" reference, or free text to search. Omit to list servers."),
			),
		),
		func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(gw.Discover(req.GetString("query", ""))), nil
		},
	)

	s.AddTool(
		mcp.NewTool("invoke",
			mcp.WithDescription("Invoke a tool on a connected MCP server through the gateway."),
			mcp.WithString("server",
				mcp.Required(),
				mcp.Description("Name of the target MCP server, as shown by discover."),
			),
			mcp.WithString("tool",
				mcp.Required(),
				mcp.Description("Tool name on that server, e.g. \"get_pull_request\" or \"github.get_pull_request\"."),
			),
			mcp.WithObject("arguments",
				mcp.Description("Arguments object matching the tool's input schema."),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			toolName := req.GetString("tool", "")
			if toolName == "" {
				return mcp.NewToolResultError("`tool` is required"), nil
			}
			result, err := gw.Invoke(ctx, req.GetString("server", ""), toolName, nestedArguments(req))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return result, nil
		},
	)

	return s
}

// nestedArguments extracts the "arguments" object from the invoke call so it is
// forwarded verbatim to the downstream tool.
func nestedArguments(req mcp.CallToolRequest) map[string]any {
	nested, ok := req.GetArguments()["arguments"].(map[string]any)
	if !ok {
		return nil
	}
	return nested
}
