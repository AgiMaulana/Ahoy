package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/AgiMaulana/Ahoy/internal/config"
	"github.com/AgiMaulana/Ahoy/internal/gateway"
)

const addUsage = `Add a downstream MCP server to the config.

Usage:
  ahoy add <url> [flags]                          streamable HTTP, e.g. https://mcp.sentry.dev/mcp
  ahoy add <name> [flags] -- <command> [args...]  stdio, e.g. -- npx -y @modelcontextprotocol/server-github

Flags:
  -name string          server name (default: derived from the URL)
  -description string   one-line description shown by discover()
  -auth string          http auth method: "oauth" (browser login via ahoy login)
  -header KEY:VALUE     HTTP header, repeatable (http only)
  -env KEY=VALUE        environment variable, repeatable (stdio only)
  -force                overwrite an existing server with the same name
  -no-verify            save without checking the connection first
  -config string        path to the config file (default: $AHOY_HOME/config.json, else ~/.ahoy/config.json)`

// stringList collects a repeatable flag value (e.g. -header / -env).
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func add(args []string) {
	fs := flag.NewFlagSet("ahoy add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, addUsage) }

	var (
		configPath  = fs.String("config", "", "path to the gateway config file")
		name        = fs.String("name", "", "server name")
		description = fs.String("description", "", "one-line description shown by discover()")
		auth        = fs.String("auth", "", "http auth method: \"oauth\"")
		force       = fs.Bool("force", false, "overwrite an existing server")
		noVerify    = fs.Bool("no-verify", false, "save without checking the connection")
	)
	var headers, env stringList
	fs.Var(&headers, "header", "HTTP header KEY:VALUE (repeatable)")
	fs.Var(&env, "env", "environment variable KEY=VALUE (repeatable)")

	positionals, command, err := parseArgs(fs, args)
	if err != nil {
		fail(err)
	}
	// http takes exactly one positional (the URL); stdio takes at most one
	// (the name), with the command after "--".
	if len(command) > 0 {
		if len(positionals) > 1 {
			fs.Usage()
			os.Exit(2)
		}
	} else if len(positionals) != 1 {
		fs.Usage()
		os.Exit(2)
	}

	target := ""
	if len(positionals) == 1 {
		target = positionals[0]
	}
	sc, serverName, err := buildServer(target, *name, command, headers, env, *auth)
	if err != nil {
		fail(err)
	}
	sc.Description = *description

	path, err := config.ResolveForWrite(*configPath)
	if err != nil {
		fail(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		fail(err)
	}
	if _, exists := cfg.Servers[serverName]; exists && !*force {
		fail(fmt.Errorf("server %q already exists (use -force to overwrite)", serverName))
	}
	cfg.Servers[serverName] = sc
	if err := cfg.Validate(); err != nil {
		fail(err)
	}

	// Verify by connecting before saving, so a broken entry is not committed.
	// An OAuth server cannot be verified until it has been logged into.
	tools := -1
	if sc.Auth != config.AuthOAuth && !*noVerify {
		tools, err = verifyServer(serverName, sc)
		if err != nil {
			fail(fmt.Errorf("could not connect to %q: %w\n\nFix the entry, or re-run with -no-verify to save it anyway", serverName, err))
		}
	}

	if err := config.Save(path, cfg); err != nil {
		fail(err)
	}

	fmt.Printf("added %q (%s) -> %s\n", serverName, sc.Transport, describeTarget(sc))
	switch {
	case sc.Auth == config.AuthOAuth:
		fmt.Printf("run \"ahoy login %s\" to authorize it\n", serverName)
	case tools >= 0:
		fmt.Printf("verified: %d tool(s) available\n", tools)
	default:
		fmt.Println("saved without verification")
	}
}

// buildServer turns parsed arguments into a server config and its final name.
// A non-empty command means stdio; otherwise target must be an http(s) URL.
func buildServer(target, name string, command []string, headers, env []string, auth string) (config.ServerConfig, string, error) {
	if len(command) > 0 {
		if isURL(target) {
			return config.ServerConfig{}, "", fmt.Errorf("a stdio server takes a NAME then -- <command>, not a URL")
		}
		if len(headers) > 0 {
			return config.ServerConfig{}, "", fmt.Errorf("-header applies to http servers; use -env for stdio")
		}
		if auth != "" {
			return config.ServerConfig{}, "", fmt.Errorf("-auth applies to http servers; use -env for stdio")
		}
		final := name
		if final == "" {
			final = target
		}
		if final == "" {
			return config.ServerConfig{}, "", fmt.Errorf("a stdio server needs a name (a positional NAME or -name)")
		}
		vars, err := keyValues(env, "=")
		if err != nil {
			return config.ServerConfig{}, "", fmt.Errorf("-env: %w", err)
		}
		return config.ServerConfig{
			Transport: config.TransportStdio,
			Command:   command[0],
			Args:      command[1:],
			Env:       vars,
		}, sanitizeName(final), nil
	}

	if !isURL(target) {
		return config.ServerConfig{}, "", fmt.Errorf("provide a URL for an http server, or NAME -- <command> for stdio")
	}
	if len(env) > 0 {
		return config.ServerConfig{}, "", fmt.Errorf("-env applies to stdio servers; use -header for http")
	}
	h, err := keyValues(headers, ":")
	if err != nil {
		return config.ServerConfig{}, "", fmt.Errorf("-header: %w", err)
	}
	if name == "" {
		name = nameFromURL(target)
	}
	return config.ServerConfig{
		Transport: config.TransportHTTP,
		URL:       target,
		Headers:   h,
		Auth:      config.AuthType(auth),
	}, sanitizeName(name), nil
}

// verifyServer dials a single server and reports how many tools it exposes.
func verifyServer(name string, sc config.ServerConfig) (int, error) {
	gw := gateway.New(&config.Config{Servers: map[string]config.ServerConfig{name: sc}})
	defer gw.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	gw.ConnectAll(ctx)

	s := gw.Servers()[0]
	if err := s.Err(); err != nil {
		return 0, err
	}
	return s.ToolCount(), nil
}

func isURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

// nameFromURL derives a short server name from a URL host, e.g.
// https://mcp.sentry.dev/mcp -> "sentry", http://localhost:8080/mcp -> "localhost".
func nameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "server"
	}
	host := u.Hostname()
	if host == "" || net.ParseIP(host) != nil {
		return "server"
	}
	host = strings.TrimPrefix(host, "www.")
	labels := strings.Split(host, ".")
	if len(labels) >= 2 {
		return sanitizeName(labels[len(labels)-2])
	}
	return sanitizeName(labels[0])
}

// sanitizeName lowercases a name and replaces anything outside [a-z0-9_-] with
// a single dash, so a name never contains the "." that separates server.tool.
func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out
	}
	return "server"
}

// keyValues parses repeatable "KEY<sep>VALUE" flags into a map.
func keyValues(pairs []string, sep string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, sep)
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("expected KEY%sVALUE, got %q", sep, p)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}
