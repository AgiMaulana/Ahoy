// Package hooks loads and runs Ahoy's lifecycle hooks: shell commands declared
// in hooks/hooks.json (next to the config file) that run before the gateway
// connects to its downstream servers (Boot) and after it closes them
// (Shutdown). Hooks are environment prep, not server ownership: they are
// server-agnostic, fail-soft, and never touch the MCP stdio channel.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Default timeouts, used when a hook does not declare its own.
const (
	defaultBootTimeout     = 30 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

// Event is the set of lifecycle points that can carry hooks.
type Event string

const (
	EventBoot     Event = "Boot"
	EventShutdown Event = "Shutdown"
)

// Hook is one lifecycle hook: a shell command run via "sh -c" (or "cmd /C" on
// Windows), so chaining, pipes and direct script paths all work. A hook runs
// with the config directory as its working directory and inherits Ahoy's
// environment.
type Hook struct {
	Type          string `json:"type"`
	Command       string `json:"command"`
	Timeout       int    `json:"timeout,omitempty"` // seconds; 0 = event default
	StatusMessage string `json:"statusMessage,omitempty"`
}

// File is the parsed hooks.json.
type File struct {
	Events map[Event][]Hook `json:"events"`
}

// Load reads the hooks file at path. A missing file is not an error: hooks are
// opt-in, so no hooks.json simply means no hooks. Anything malformed is an
// error, matching the strictness of config.json.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{Events: map[Event][]Hook{}}, nil
		}
		return nil, fmt.Errorf("read hooks %s: %w", path, err)
	}

	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse hooks %s: %w", path, err)
	}
	if f.Events == nil {
		f.Events = map[Event][]Hook{}
	}
	for event, list := range f.Events {
		if event != EventBoot && event != EventShutdown {
			return nil, fmt.Errorf("hooks %s: unknown event %q (only %q and %q)", path, event, EventBoot, EventShutdown)
		}
		for i, h := range list {
			if h.Type != "command" {
				return nil, fmt.Errorf("hooks %s: %s[%d]: unsupported type %q (only \"command\")", path, event, i, h.Type)
			}
			if h.Command == "" {
				return nil, fmt.Errorf("hooks %s: %s[%d]: \"command\" is required", path, event, i)
			}
			if h.Timeout < 0 {
				return nil, fmt.Errorf("hooks %s: %s[%d]: \"timeout\" must not be negative", path, event, i)
			}
		}
	}
	return &f, nil
}

// BootFile and ShutdownFile resolve the hooks file next to a config file:
// <dir-of-config>/hooks/hooks.json.
func BootFile(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "hooks", "hooks.json")
}

// RunBoot runs the Boot hooks in declared order. Failures are logged and do
// not stop the remaining hooks: one broken command must never prevent Ahoy
// from serving.
func RunBoot(ctx context.Context, dir string, f *File) {
	run(ctx, dir, f.Events[EventBoot], EventBoot, false)
}

// RunShutdown runs the Shutdown hooks in reverse declared order (teardown
// unwinds setup). Failures are logged and do not stop the remaining hooks.
func RunShutdown(ctx context.Context, dir string, f *File) {
	run(ctx, dir, f.Events[EventShutdown], EventShutdown, true)
}

// run executes the hook list sequentially. Shutdown runs in reverse order.
func run(ctx context.Context, dir string, list []Hook, event Event, reverse bool) {
	if len(list) == 0 {
		return
	}
	for i := 0; i < len(list); i++ {
		if ctx.Err() != nil {
			log.Printf("hook[%s] stopping: %v", event, ctx.Err())
			return
		}
		idx := i
		if reverse {
			idx = len(list) - 1 - i
		}
		runOne(ctx, dir, list[idx], event, i+1, len(list))
	}
}

func runOne(ctx context.Context, dir string, h Hook, event Event, n, total int) {
	label := h.StatusMessage
	if label == "" {
		label = truncate(h.Command, 60)
	}
	timeout := defaultBootTimeout
	if event == EventShutdown {
		timeout = defaultShutdownTimeout
	}
	if h.Timeout > 0 {
		timeout = time.Duration(h.Timeout) * time.Second
	}

	log.Printf("hook[%s %d/%d] %s", event, n, total, label)

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := shellCommand(cctx, h.Command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if len(bytes.TrimSpace(out)) > 0 {
		log.Printf("hook[%s %d/%d] output: %s", event, n, total, bytes.TrimSpace(out))
	}
	switch {
	case cctx.Err() != nil && err != nil:
		log.Printf("hook[%s %d/%d] timed out after %s", event, n, total, timeout)
	case err != nil:
		log.Printf("hook[%s %d/%d] failed: %v", event, n, total, err)
	default:
		log.Printf("hook[%s %d/%d] ok", event, n, total)
	}
}

// shellCommand builds the command used to execute a hook: "sh -c" everywhere
// except Windows, where "cmd /C" is the closest equivalent. The command is
// bound to ctx so a hook that exceeds its timeout is killed.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
