package gateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	maxServerTools   = 50
	maxSearchResults = 30
	maxDescLen       = 140
)

// Discover resolves a discovery query into a compact, human-readable answer.
// An empty query lists servers; a server name lists its tools; a "server.tool"
// reference returns that tool's full input schema; anything else searches tool
// names and descriptions. Full schemas are only ever returned for a single,
// explicitly referenced tool, so discovery stays demand-driven.
func (g *Gateway) Discover(query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return g.listServers()
	}
	if s, ok := g.lookup(q); ok {
		return g.listServerTools(s)
	}
	if s, tool, ok := g.findTool(q); ok {
		return formatToolSchema(s, tool)
	}
	return g.search(q)
}

func (g *Gateway) listServers() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ahoy: %d connected server(s).\n", len(g.names))
	for _, name := range g.names {
		s := g.servers[name]
		if err := s.Err(); err != nil {
			fmt.Fprintf(&b, "- %s: %s [unavailable: %v]\n", name, s.Description, err)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s [%d tools]\n", name, s.Description, s.ToolCount())
	}
	b.WriteString(`Next: discover(query="<server>") to list tools, ` +
		`discover(query="<server>.<tool>") for an input schema, then ` +
		`invoke(server, tool, arguments).`)
	return b.String()
}

func (g *Gateway) listServerTools(s *Server) string {
	tools := s.toolsCopy()
	if len(tools) == 0 {
		if err := s.Err(); err != nil {
			return fmt.Sprintf("Server %q is unavailable: %v", s.Name, err)
		}
		return fmt.Sprintf("Server %q exposes no tools.", s.Name)
	}

	shown := tools
	if len(shown) > maxServerTools {
		shown = shown[:maxServerTools]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d tool(s), showing %d.\n", s.Name, len(tools), len(shown))
	for _, t := range shown {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name, summarize(t.Description))
	}
	if len(tools) > len(shown) {
		fmt.Fprintf(&b, "... %d more; narrow with discover(query=\"...\").\n", len(tools)-len(shown))
	}
	fmt.Fprintf(&b, "Next: discover(query=\"%s.<tool>\") for the input schema.", s.Name)
	return b.String()
}

// findTool resolves an exact tool reference: either "server.tool" or a bare
// tool name that is unique across all servers.
func (g *Gateway) findTool(ref string) (*Server, mcp.Tool, bool) {
	if prefix, name, found := strings.Cut(ref, "."); found {
		s, ok := g.lookup(prefix)
		if !ok {
			return nil, mcp.Tool{}, false
		}
		for _, t := range s.toolsCopy() {
			if strings.EqualFold(t.Name, name) {
				return s, t, true
			}
		}
		return nil, mcp.Tool{}, false
	}

	var (
		match *Server
		tool  mcp.Tool
		count int
	)
	for _, serverName := range g.names {
		s := g.servers[serverName]
		for _, t := range s.toolsCopy() {
			if strings.EqualFold(t.Name, ref) {
				match, tool = s, t
				count++
			}
		}
	}
	if count == 1 {
		return match, tool, true
	}
	return nil, mcp.Tool{}, false
}

func (g *Gateway) search(query string) string {
	needle := strings.ToLower(query)

	type hit struct {
		server *Server
		tool   mcp.Tool
	}
	var hits []hit

	for _, name := range g.names {
		s := g.servers[name]
		for _, t := range s.toolsCopy() {
			hay := strings.ToLower(name + "." + t.Name + " " + t.Description)
			if strings.Contains(hay, needle) {
				hits = append(hits, hit{s, t})
			}
		}
	}

	var serverHits []*Server
	for _, name := range g.names {
		s := g.servers[name]
		if strings.Contains(strings.ToLower(name), needle) ||
			strings.Contains(strings.ToLower(s.Description), needle) {
			serverHits = append(serverHits, s)
		}
	}

	if len(hits) == 0 && len(serverHits) == 0 {
		return fmt.Sprintf("No tools or servers match %q. Call discover() to list servers.", query)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Matches for %q: %d tool(s), %d server(s).\n", query, len(hits), len(serverHits))

	for _, s := range serverHits {
		fmt.Fprintf(&b, "server %s: %s [%d tools]\n", s.Name, s.Description, s.ToolCount())
	}

	shown := hits
	if len(shown) > maxSearchResults {
		shown = shown[:maxSearchResults]
	}
	for _, h := range shown {
		fmt.Fprintf(&b, "- %s.%s: %s\n", h.server.Name, h.tool.Name, summarize(h.tool.Description))
	}
	if len(hits) > len(shown) {
		fmt.Fprintf(&b, "... %d more match(es); narrow your query.\n", len(hits)-len(shown))
	}
	b.WriteString(`Next: discover(query="<server>.<tool>") for the input schema.`)
	return b.String()
}

func formatToolSchema(s *Server, tool mcp.Tool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tool: %s.%s\n", s.Name, tool.Name)
	if tool.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", summarize(tool.Description))
	}
	if schema, err := json.MarshalIndent(tool.InputSchema, "", "  "); err == nil {
		fmt.Fprintf(&b, "Input schema:\n%s\n", schema)
	}
	fmt.Fprintf(&b, "Invoke: invoke(server=%q, tool=%q, arguments={...})", s.Name, tool.Name)
	return b.String()
}

func summarize(desc string) string {
	desc = strings.Join(strings.Fields(desc), " ")
	if desc == "" {
		return "(no description)"
	}
	r := []rune(desc)
	if len(r) > maxDescLen {
		return string(r[:maxDescLen-1]) + "..."
	}
	return desc
}
