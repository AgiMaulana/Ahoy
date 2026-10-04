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

JSON file (`-config`, default `config.json`):

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
    }
  }
}
```

- `transport`: `stdio` (needs `command`, optional `args`/`env`) or `http`
  (needs `url`, optional `headers`).
- `description` is shown by `discover()`; keep it to one line.
- `env` is appended to the inherited environment, so `PATH` etc. still work.

## Build & run

```sh
go build ./cmd/ahoy
./ahoy -config config.json      # speaks MCP over stdio; logs go to stderr
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
