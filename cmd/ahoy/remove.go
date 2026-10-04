package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/AgiMaulana/Ahoy/internal/config"
)

const removeUsage = `Remove a downstream MCP server from the config.

Usage:
  ahoy remove <name> [-config path]`

func remove(args []string) {
	fs := flag.NewFlagSet("ahoy remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, removeUsage) }
	configPath := fs.String("config", "", "path to the gateway config file")

	positionals, _, err := parseArgs(fs, args)
	if err != nil {
		fail(err)
	}
	if len(positionals) != 1 {
		fs.Usage()
		os.Exit(2)
	}
	name := positionals[0]

	path, err := config.Resolve(*configPath)
	if err != nil {
		fail(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		fail(err)
	}
	key, ok := matchServer(cfg, name)
	if !ok {
		fail(fmt.Errorf("unknown server %q (configured: %s)", name, joinNames(cfg)))
	}
	delete(cfg.Servers, key)
	if err := config.Save(path, cfg); err != nil {
		fail(err)
	}
	fmt.Printf("removed %q\n", key)
}

// matchServer finds a configured server by exact name, then case-insensitively.
func matchServer(cfg *config.Config, name string) (string, bool) {
	if _, ok := cfg.Servers[name]; ok {
		return name, true
	}
	for n := range cfg.Servers {
		if strings.EqualFold(n, name) {
			return n, true
		}
	}
	return "", false
}
