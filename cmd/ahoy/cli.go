package main

import (
	"errors"
	"flag"
	"io/fs"
	"log"
	"sort"
	"strings"

	"github.com/AgiMaulana/Ahoy/internal/config"
)

// fail reports a fatal user-facing error (stderr, "ahoy:" prefix) and exits 1.
func fail(err error) {
	log.Fatalf("%v", err)
}

// loadConfig reads the config at path. A missing file is not an error: it is
// treated as empty so the editing commands can create one.
func loadConfig(path string) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return &config.Config{Servers: map[string]config.ServerConfig{}}, nil
	}
	return nil, err
}

// parseArgs parses fs over args while letting flags and a positional NAME
// appear in either order. Everything after the first "--" is returned verbatim
// as command, so flags meant for a downstream process are not consumed.
func parseArgs(fs *flag.FlagSet, args []string) (positionals, command []string, err error) {
	head := args
	for i, a := range args {
		if a == "--" {
			head, command = args[:i], args[i+1:]
			break
		}
	}
	rest := head
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positionals = append(positionals, rest[0])
		rest = rest[1:]
	}
	return positionals, command, nil
}

// describeTarget renders a server's command line (stdio) or URL (http).
func describeTarget(sc config.ServerConfig) string {
	if sc.Transport == config.TransportHTTP {
		return sc.URL
	}
	return strings.Join(append([]string{sc.Command}, sc.Args...), " ")
}

// sortedServerNames returns the configured server names in stable order.
func sortedServerNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// joinNames renders the configured server names for error messages.
func joinNames(cfg *config.Config) string {
	return strings.Join(sortedServerNames(cfg), ", ")
}
