package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/AgiMaulana/Ahoy/internal/config"
)

const listUsage = `List the downstream MCP servers in the config.

Usage:
  ahoy list [-config path]`

func list(args []string) {
	fs := flag.NewFlagSet("ahoy list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, listUsage) }
	configPath := fs.String("config", "", "path to the gateway config file")

	positionals, _, err := parseArgs(fs, args)
	if err != nil {
		fail(err)
	}
	if len(positionals) > 0 {
		fs.Usage()
		os.Exit(2)
	}

	path, err := config.Resolve(*configPath)
	if err != nil {
		fail(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		fail(err)
	}
	names := sortedServerNames(cfg)
	if len(names) == 0 {
		fmt.Printf("no servers configured in %s\n", path)
		return
	}

	width := len("NAME")
	for _, n := range names {
		if len(n) > width {
			width = len(n)
		}
	}
	fmt.Printf("%-*s  %-6s  %-5s  %s\n", width, "NAME", "TYPE", "AUTH", "TARGET")
	for _, n := range names {
		sc := cfg.Servers[n]
		fmt.Printf("%-*s  %-6s  %-5s  %s\n", width, n, sc.Transport, sc.Auth, describeTarget(sc))
	}
}
