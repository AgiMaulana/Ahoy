# Ahoy

Ahoy is a single MCP server that fronts any number of downstream MCP servers
and tells the agent: *this is Ahoy — discover what you need, then invoke it*.

The problem it solves: connecting an agent to N MCP servers normally floods its
initial context with hundreds of tool schemas. Ahoy exposes exactly **two**
tools no matter how many servers are connected, so adding a server never grows
the agent's tool list.

```
                    Agent
                      │  sees only: discover, invoke
                      ▼
              ┌─────────────────┐
              │       Ahoy      │
              └────────┬────────┘
          ┌────────────┼─────────────┐
          ▼            ▼             ▼
      github MCP    slack MCP    remote HTTP MCP
       (stdio)      (stdio)      (streamable HTTP)
```

## Tools

**`discover`** — one tool, one optional `query`:

| Call | Returns |
| --- | --- |
| `discover()` | Connected servers, names only |
| `discover(query="github")` | That server's tool names + short descriptions |
| `discover(query="github.get_pull_request")` | Full input schema for one tool |
| `discover(query="modify a pull request")` | Substring search over names/descriptions |

Discovery is progressive: full schemas are returned only for a single,
explicitly referenced tool, so listing 127 GitHub tools never dumps 127 schemas.

**`invoke`** — the only execution surface:

```json
{ "server": "github", "tool": "get_pull_request",
  "arguments": { "owner": "AgiMaulana", "repo": "xenv", "pull_number": 1 } }
```

`invoke` is deliberately separate from `discover`: discovery is read-only,
while `invoke` is the authorization boundary (the same "discovery ≠ access"
line used by `xenv`). If a downstream stdio process dies, Ahoy transparently
reconnects and retries the call once.

## Config

JSON file (`-config`, default `$AHOY_HOME/config.json`, else `~/.ahoy/config.json`):

```json
{
  "servers": {
    "github": {
      "description": "GitHub repositories, issues, pull requests",
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_PERSONAL_ACCESS_TOKEN": "REPLACE_ME" }
    },
    "remote": {
      "description": "A streamable-HTTP MCP server",
      "transport": "http",
      "url": "http://localhost:8080/mcp",
      "headers": { "Authorization": "Bearer REPLACE_ME" }
    },
    "sentry": {
      "description": "Sentry issues and projects (browser OAuth login)",
      "transport": "http",
      "url": "https://mcp.sentry.dev/mcp",
      "auth": "oauth"
    }
  }
}
```

- `transport`: `stdio` (needs `command`, optional `args`/`env`) or `http`
  (needs `url`, optional `headers`).
- `description` is shown by `discover()`; keep it to one line.
- `env` is appended to the inherited environment, so `PATH` etc. still work.
- `auth`: `oauth` for an HTTP server that authenticates with a browser login.
  Run `ahoy login <name>` once; the token is cached under `~/.ahoy/tokens` (or
  `$AHOY_HOME`) and reused by the gateway afterwards.
- When `-config` is omitted, Ahoy looks for `$AHOY_HOME/config.json`, then
  `~/.ahoy/config.json`, then `./config.json`. `ahoy add` creates the per-user
  config when none exists, so it never writes a stray file into the cwd.

## Managing servers

The same config can be edited from the terminal:

```sh
# streamable HTTP — the name is derived from the host ("sentry")
ahoy add https://mcp.sentry.dev/mcp

# ...with a static token
ahoy add https://mcp.sentry.dev/mcp -header "Authorization: Bearer $TOKEN"

# ...or with a browser OAuth login (run this once to authorize)
ahoy add https://mcp.sentry.dev/mcp -auth oauth
ahoy login sentry

# stdio — NAME -- <command> [args]
ahoy add github -- npx -y @modelcontextprotocol/server-github
ahoy add github -env GITHUB_PERSONAL_ACCESS_TOKEN=$TOKEN -force \
  -- npx -y @modelcontextprotocol/server-github

ahoy list
ahoy remove github
```

`add` connects to the server and prints its tool count *before* saving, so a
broken entry is never committed. Pass `-no-verify` to save a server that is not
reachable at add time. `-name` overrides the derived name; every command accepts
`-config` (default: `$AHOY_HOME/config.json`, else `~/.ahoy/config.json`).

`login` runs the standard OAuth 2.1 flow — metadata discovery, dynamic client
registration, PKCE, and a localhost callback — then saves the token. Use
`-client-id`/`-client-secret` for a pre-registered client, `-scope` to request
scopes, and `-port` if the default callback port is taken. A server configured
with `auth: oauth` that has not been logged into reports
`run "ahoy login <name>"` when the gateway starts.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/AgiMaulana/Ahoy/main/install.sh | sh
```

The installer downloads the release binary for your platform, verifies its
[cosign](https://github.com/sigstore/cosign) keyless signature (when `cosign` is
on `PATH`), and installs `ahoy` to `/usr/local/bin`. Pin a version or choose a
different directory:

```sh
curl -fsSL https://raw.githubusercontent.com/AgiMaulana/Ahoy/main/install.sh | sh -s -- v0.1.0
AHOY_INSTALL_DIR="$HOME/.local/bin" sh install.sh
```

## Build & run

```sh
go build ./cmd/ahoy
./ahoy                          # uses ~/.ahoy/config.json (or $AHOY_HOME/config.json)
./ahoy -config config.json      # or an explicit path; speaks MCP over stdio
```

Wire it into an MCP client (e.g. Claude Code):

```sh
claude mcp add ahoy -- /absolute/path/to/ahoy -config /absolute/path/to/config.json
```

## Develop / test

```sh
go vet ./...
go test ./...          # includes an end-to-end stdio test
go test -short ./...   # unit tests only
```

`cmd/downstream-demo` is a tiny stdio MCP server (`echo`, `add`, plus `pid` and
`self_destruct` for reconnect testing) used by the end-to-end tests.
