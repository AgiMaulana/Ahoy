package main

import (
	"flag"
	"reflect"
	"testing"

	"github.com/AgiMaulana/Ahoy/internal/config"
)

func TestParseArgs(t *testing.T) {
	newSet := func() (*flag.FlagSet, *string) {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		desc := fs.String("description", "", "")
		return fs, desc
	}

	t.Run("flags before name", func(t *testing.T) {
		fs, desc := newSet()
		pos, cmd, err := parseArgs(fs, []string{"-description", "hi", "github"})
		if err != nil {
			t.Fatal(err)
		}
		if *desc != "hi" {
			t.Errorf("description = %q, want hi", *desc)
		}
		if !reflect.DeepEqual(pos, []string{"github"}) || cmd != nil {
			t.Errorf("pos = %v, cmd = %v", pos, cmd)
		}
	})

	t.Run("name before flags", func(t *testing.T) {
		fs, desc := newSet()
		pos, _, err := parseArgs(fs, []string{"github", "-description", "hi"})
		if err != nil {
			t.Fatal(err)
		}
		if *desc != "hi" {
			t.Errorf("description = %q, want hi", *desc)
		}
		if !reflect.DeepEqual(pos, []string{"github"}) {
			t.Errorf("pos = %v", pos)
		}
	})

	t.Run("command after separator is verbatim", func(t *testing.T) {
		fs, _ := newSet()
		pos, cmd, err := parseArgs(fs, []string{"github", "--", "npx", "-y", "@scope/pkg"})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pos, []string{"github"}) {
			t.Errorf("pos = %v", pos)
		}
		if !reflect.DeepEqual(cmd, []string{"npx", "-y", "@scope/pkg"}) {
			t.Errorf("cmd = %v", cmd)
		}
	})

	t.Run("flags after separator are not consumed", func(t *testing.T) {
		fs, desc := newSet()
		_, cmd, err := parseArgs(fs, []string{"github", "--", "npx", "-description", "x"})
		if err != nil {
			t.Fatal(err)
		}
		if *desc != "" {
			t.Errorf("description = %q, want empty (belongs to the command)", *desc)
		}
		if !reflect.DeepEqual(cmd, []string{"npx", "-description", "x"}) {
			t.Errorf("cmd = %v", cmd)
		}
	})
}

func TestBuildServerStdio(t *testing.T) {
	sc, name, err := buildServer("github", "", []string{"npx", "-y", "@modelcontextprotocol/server-github"}, nil, []string{"TOKEN=x"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "github" {
		t.Errorf("name = %q, want github", name)
	}
	if sc.Transport != config.TransportStdio || sc.Command != "npx" {
		t.Errorf("sc = %+v", sc)
	}
	if !reflect.DeepEqual(sc.Args, []string{"-y", "@modelcontextprotocol/server-github"}) {
		t.Errorf("args = %v", sc.Args)
	}
	if sc.Env["TOKEN"] != "x" {
		t.Errorf("env = %v", sc.Env)
	}
}

func TestBuildServerStdioNameFlag(t *testing.T) {
	// No positional name: -name supplies it.
	sc, name, err := buildServer("", "my-server", []string{"uvx", "mcp-server-fetch"}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "my-server" || sc.Command != "uvx" {
		t.Errorf("name = %q, sc = %+v", name, sc)
	}
}

func TestBuildServerHTTP(t *testing.T) {
	sc, name, err := buildServer("https://mcp.sentry.dev/mcp", "", nil, []string{"Authorization: Bearer z"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "sentry" {
		t.Errorf("name = %q, want sentry", name)
	}
	if sc.Transport != config.TransportHTTP || sc.URL != "https://mcp.sentry.dev/mcp" {
		t.Errorf("sc = %+v", sc)
	}
	if sc.Headers["Authorization"] != "Bearer z" {
		t.Errorf("headers = %v", sc.Headers)
	}
}

func TestBuildServerNameOverride(t *testing.T) {
	_, name, err := buildServer("https://mcp.sentry.dev/mcp", "My Sentry", nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "my-sentry" {
		t.Errorf("name = %q, want my-sentry", name)
	}
}

func TestBuildServerOAuth(t *testing.T) {
	sc, _, err := buildServer("https://mcp.sentry.dev/mcp", "", nil, nil, nil, "oauth")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Auth != config.AuthOAuth {
		t.Errorf("auth = %q, want oauth", sc.Auth)
	}
}

func TestBuildServerErrors(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		cmd     []string
		headers []string
		env     []string
		auth    string
	}{
		{"bare name without command", "github", nil, nil, nil, ""},
		{"url with stdio command", "https://x.dev/mcp", []string{"npx"}, nil, nil, ""},
		{"header on stdio", "github", []string{"npx"}, []string{"A: b"}, nil, ""},
		{"env on http", "https://x.dev/mcp", nil, nil, []string{"A=b"}, ""},
		{"auth on stdio", "github", []string{"npx"}, nil, nil, "oauth"},
		{"stdio without a name", "", []string{"npx"}, nil, nil, ""},
		{"bad env", "github", []string{"npx"}, nil, []string{"noequals"}, ""},
	}
	for _, tc := range cases {
		if _, _, err := buildServer(tc.target, "", tc.cmd, tc.headers, tc.env, tc.auth); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
}

func TestNameFromURL(t *testing.T) {
	cases := map[string]string{
		"https://mcp.sentry.dev/mcp": "sentry",
		"https://sentry.dev/mcp":     "sentry",
		"https://api.example.com":    "example",
		"http://localhost:8080/mcp":  "localhost",
		"https://www.foo.com/mcp":    "foo",
		"https://127.0.0.1:9090/mcp": "server",
	}
	for in, want := range cases {
		if got := nameFromURL(in); got != want {
			t.Errorf("nameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"my sentry":  "my-sentry",
		"Foo.Bar":    "foo-bar",
		"  spaced  ": "spaced",
		"a--b":       "a-b",
		"!!!":        "server",
		"keep_under": "keep_under",
		"tab\tbreak": "tab-break",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
