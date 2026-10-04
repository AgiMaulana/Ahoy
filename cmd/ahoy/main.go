// Command ahoy runs Ahoy over stdio. It connects to the downstream MCP
// servers in its config and exposes only "discover" and "invoke" to the
// agent.
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/mark3labs/mcp-go/server"

	"github.com/AgiMaulana/Ahoy/internal/config"
	"github.com/AgiMaulana/Ahoy/internal/gateway"
	"github.com/AgiMaulana/Ahoy/internal/mcpserver"
)

func main() {
	configPath := flag.String("config", "config.json", "path to the gateway config file")
	flag.Parse()

	// stdout is the MCP stdio channel: all diagnostics must go to stderr.
	log.SetOutput(os.Stderr)
	log.SetPrefix("ahoy: ")

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
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
