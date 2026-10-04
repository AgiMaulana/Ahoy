// Command ahoy runs Ahoy over stdio. It connects to the downstream MCP
// servers in its config and exposes only "discover" and "invoke" to the
// agent. The add, list and remove subcommands edit that config file.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/server"

	"github.com/AgiMaulana/Ahoy/internal/config"
	"github.com/AgiMaulana/Ahoy/internal/gateway"
	"github.com/AgiMaulana/Ahoy/internal/mcpserver"
)

// version is the release version, stamped at build time with
// -ldflags "-X main.version=<tag>" (see .github/workflows/release.yml).
var version = "dev"

const usage = `Ahoy — one MCP server in front of many.

Usage:
  ahoy [serve] [-config path]              run the gateway over stdio (default)
  ahoy add <url> [flags]                   add a streamable-HTTP server
  ahoy add <name> [flags] -- <cmd> [args]  add a stdio server
  ahoy login <name> [-config path]         authenticate an OAuth server
  ahoy list [-config path]                 list configured servers
  ahoy remove <name> [-config path]        remove a server
  ahoy version                             print the version

Use "ahoy add -h", "ahoy login -h", "ahoy list -h" or "ahoy remove -h" for command flags.`

func main() {
	log.SetOutput(os.Stderr) // stdout is reserved for the MCP stdio channel.
	log.SetPrefix("ahoy: ")

	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Fprintln(os.Stderr, usage)
			return
		case "version", "-version", "--version":
			fmt.Fprintln(os.Stderr, version)
			return
		case "serve", "run":
			serve(args[1:])
			return
		case "add":
			add(args[1:])
			return
		case "login", "auth":
			login(args[1:])
			return
		case "list", "ls":
			list(args[1:])
			return
		case "remove", "rm":
			remove(args[1:])
			return
		}
		// An unknown bare word is a typo; a leading "-" is the serve flags.
		if !strings.HasPrefix(args[0], "-") {
			fmt.Fprintf(os.Stderr, "ahoy: unknown command %q\n\n%s\n", args[0], usage)
			os.Exit(2)
		}
	}
	serve(args)
}

func serve(args []string) {
	fs := flag.NewFlagSet("ahoy", flag.ExitOnError)
	configPath := fs.String("config", "", "path to the gateway config file")
	_ = fs.Parse(args)

	path, err := config.Resolve(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if len(cfg.Servers) == 0 {
		log.Fatalf("no servers defined in %s", path)
	}

	gw := gateway.New(cfg)
	gw.ConnectAll(context.Background())
	defer gw.Close()

	for _, s := range gw.Servers() {
		if err := s.Err(); err != nil {
			log.Printf("server %s: unavailable: %v", s.Name, err)
			continue
		}
		log.Printf("server %s: %d tool(s)", s.Name, s.ToolCount())
	}

	if err := server.ServeStdio(mcpserver.New(gw)); err != nil {
		log.Fatalf("gateway server error: %v", err)
	}
}
