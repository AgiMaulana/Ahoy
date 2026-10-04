// Package gateway connects to downstream MCP servers and exposes a progressive
// discovery + invocation surface to an agent, keeping the agent's initial
// context to a single pair of tools regardless of how many servers exist.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/AgiMaulana/Ahoy/internal/config"
)

const (
	connectTimeout = 20 * time.Second
	callTimeout    = 120 * time.Second
	clientName     = "ahoy"
	clientVersion  = "0.1.0"
)

// Server is a downstream MCP server plus its lazily established connection.
type Server struct {
	Name        string
	Description string
	Config      config.ServerConfig

	mu      sync.RWMutex
	client  *client.Client
	tools   []mcp.Tool
	lastErr error
}

func (s *Server) ensureConnected(ctx context.Context) error {
	s.mu.RLock()
	connected := s.client != nil
	s.mu.RUnlock()
	if connected {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return nil
	}
	return s.connectLocked(ctx)
}

func (s *Server) connectLocked(ctx context.Context) error {
	c, err := s.dial()
	if err != nil {
		s.lastErr = err
		return err
	}

	if err := c.Start(ctx); err != nil {
		_ = c.Close()
		s.lastErr = err
		return err
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: clientName, Version: clientVersion}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		s.lastErr = err
		return err
	}

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		_ = c.Close()
		s.lastErr = err
		return err
	}

	s.client = c
	s.tools = tools.Tools
	s.lastErr = nil
	return nil
}

func (s *Server) dial() (*client.Client, error) {
	switch s.Config.Transport {
	case config.TransportStdio:
		return client.NewStdioMCPClient(s.Config.Command, envSlice(s.Config.Env), s.Config.Args...)
	case config.TransportHTTP:
		var opts []transport.StreamableHTTPCOption
		if len(s.Config.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(s.Config.Headers))
		}
		return client.NewStreamableHttpClient(s.Config.URL, opts...)
	default:
		return nil, fmt.Errorf("unsupported transport %q", s.Config.Transport)
	}
}

func envSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func (s *Server) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		_ = s.client.Close()
		s.client = nil
	}
}

// CallTool forwards a tool invocation to this downstream server. If the
// connection has dropped (the child process died, or an HTTP session was
// terminated), it reconnects once and retries. Only connection-level errors are
// retried, which is safe because they mean the request never reached a live
// server; tool-level failures arrive inside a successful result and are not
// retried.
func (s *Server) CallTool(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	c, err := s.connectedClient(ctx)
	if err != nil {
		return nil, err
	}

	result, err := callTool(ctx, c, tool, args)
	if err == nil || !isConnectionError(err) {
		return result, err
	}

	s.discardClient(c, err)

	reconnectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	reconnected, err := s.connectedClient(reconnectCtx)
	if err != nil {
		return nil, fmt.Errorf("server %q reconnect failed: %w", s.Name, err)
	}
	return callTool(ctx, reconnected, tool, args)
}

func (s *Server) connectedClient(ctx context.Context) (*client.Client, error) {
	if err := s.ensureConnected(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client, nil
}

// discardClient clears the current connection only if it is still the one that
// failed, so a concurrent reconnect is not torn down by a stale error.
func (s *Server) discardClient(failed *client.Client, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == failed {
		_ = s.client.Close()
		s.client = nil
		s.lastErr = cause
	}
}

func callTool(ctx context.Context, c *client.Client, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	if c == nil {
		return nil, fmt.Errorf("not connected")
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	if args != nil {
		req.Params.Arguments = args
	}
	return c.CallTool(ctx, req)
}

// isConnectionError reports whether err means the downstream connection is gone,
// as opposed to a tool-level failure (which mcp-go returns inside a result).
func isConnectionError(err error) bool {
	return errors.Is(err, transport.ErrTransportClosed) ||
		errors.Is(err, transport.ErrSessionTerminated) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, fs.ErrClosed)
}

func (s *Server) toolsCopy() []mcp.Tool {
	s.mu.RLock()
	out := make([]mcp.Tool, len(s.tools))
	copy(out, s.tools)
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ToolCount returns the number of tools discovered on the server.
func (s *Server) ToolCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tools)
}

// Err returns the last connection error, or nil if the server is healthy.
func (s *Server) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastErr
}

// Gateway owns all downstream server connections.
type Gateway struct {
	servers map[string]*Server
	names   []string
}

// New builds a gateway from configuration. No connections are made yet.
func New(cfg *config.Config) *Gateway {
	g := &Gateway{servers: make(map[string]*Server, len(cfg.Servers))}
	for name, sc := range cfg.Servers {
		desc := sc.Description
		if desc == "" {
			desc = "no description provided"
		}
		g.servers[name] = &Server{Name: name, Description: desc, Config: sc}
		g.names = append(g.names, name)
	}
	sort.Strings(g.names)
	return g
}

// ConnectAll connects to every configured server concurrently. Servers that
// fail are still listed (with their error) and retried lazily on first use.
func (g *Gateway) ConnectAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, name := range g.names {
		s := g.servers[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, connectTimeout)
			defer cancel()
			_ = s.ensureConnected(cctx)
		}()
	}
	wg.Wait()
}

// Close shuts down every downstream connection.
func (g *Gateway) Close() {
	for _, name := range g.names {
		g.servers[name].close()
	}
}

// Names returns the configured server names in stable order.
func (g *Gateway) Names() []string {
	out := make([]string, len(g.names))
	copy(out, g.names)
	return out
}

// Servers returns the configured servers in stable order.
func (g *Gateway) Servers() []*Server {
	out := make([]*Server, 0, len(g.names))
	for _, name := range g.names {
		out = append(out, g.servers[name])
	}
	return out
}

func (g *Gateway) lookup(name string) (*Server, bool) {
	if name == "" {
		return nil, false
	}
	if s, ok := g.servers[name]; ok {
		return s, true
	}
	for n, s := range g.servers {
		if strings.EqualFold(n, name) {
			return s, true
		}
	}
	return nil, false
}

// Invoke routes a tool call to the appropriate downstream server. The tool may
// be referenced bare ("get_pull_request") or qualified ("github.get_pull_request").
func (g *Gateway) Invoke(ctx context.Context, serverName, toolName string, args map[string]any) (*mcp.CallToolResult, error) {
	if serverName == "" {
		if prefix, rest, found := strings.Cut(toolName, "."); found {
			serverName, toolName = prefix, rest
		}
	} else if prefix, rest, found := strings.Cut(toolName, "."); found && strings.EqualFold(prefix, serverName) {
		toolName = rest
	}

	s, ok := g.lookup(serverName)
	if !ok {
		return nil, fmt.Errorf("unknown server %q (available: %s)", serverName, strings.Join(g.names, ", "))
	}

	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return s.CallTool(cctx, toolName, args)
}
