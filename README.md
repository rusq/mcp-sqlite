# mcp-sqlite

A [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server that exposes SQLite database operations as tools. Connect any MCP-compatible AI client (e.g., Claude Desktop) to inspect schemas, run read-only queries, and execute write operations against a local SQLite database.

## Features

- Four MCP tools: `open_database`, `get_schema`, `query`, `execute`
- STDIO and HTTP transport modes
- Concurrent read operations with serialised database opens
- Row cap on query results (configurable, default 10) to stay within LLM token budgets
- Human-readable plain-text responses

## Requirements

- Go 1.25 or later

## Installation

```
go install github.com/rusq/mcp-sqlite/cmd/mcp-sqlite@latest
```

Install from GitHub Releases (prebuilt binaries):

1. Download the archive for your platform from:
   `https://github.com/rusq/mcp-sqlite/releases/latest`
2. Extract and place `mcp-sqlite` (or `mcp-sqlite.exe`) in your `PATH`.

Archive naming pattern:

```
mcp-sqlite_<version>_<os>_<arch>.tar.gz
```

On Windows, use:

```
mcp-sqlite_<version>_windows_<arch>.zip
```

Linux/macOS quick install (replace `<version>`, `<os>`, `<arch>`):

```bash
VERSION=v0.0.1
OS=linux   # linux or darwin
ARCH=amd64 # amd64 or arm64
curl -fL -o /tmp/mcp-sqlite.tar.gz \
  "https://github.com/rusq/mcp-sqlite/releases/download/${VERSION}/mcp-sqlite_${VERSION#v}_${OS}_${ARCH}.tar.gz"
tar -xzf /tmp/mcp-sqlite.tar.gz -C /tmp
install /tmp/mcp-sqlite /usr/local/bin/mcp-sqlite
```

Windows PowerShell quick install (replace `<version>`, `<arch>`):

```powershell
$Version = "v0.0.1"
$Arch = "amd64" # amd64 or arm64
$Url = "https://github.com/rusq/mcp-sqlite/releases/download/$Version/mcp-sqlite_$($Version.TrimStart('v'))_windows_$Arch.zip"
$Zip = "$env:TEMP\mcp-sqlite.zip"
Invoke-WebRequest -Uri $Url -OutFile $Zip
Expand-Archive -Path $Zip -DestinationPath "$env:TEMP\mcp-sqlite" -Force
Copy-Item "$env:TEMP\mcp-sqlite\mcp-sqlite.exe" "$env:USERPROFILE\AppData\Local\Microsoft\WindowsApps\mcp-sqlite.exe" -Force
```

Or build from source:

```
git clone https://github.com/rusq/mcp-sqlite
cd mcp-sqlite
go build ./cmd/mcp-sqlite
```

## Usage

```
mcp-sqlite [flags] [database-file]
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `-v` | false | Enable verbose/debug logging (to stderr) |
| `-http` | false | Start in HTTP mode instead of STDIO |
| `-listen` | `127.0.0.1:8483` | Listen address for HTTP mode |
| `-max-rows` | `10` | Maximum rows returned by `query` (must be ≥ 1) |

### Arguments

| Position | Description |
|---|---|
| 0 | Path to an existing SQLite database file (optional) |

If no database file is provided at startup, the server starts in a "no database open" state. Call the `open_database` tool before using any other tool.

### Examples

Start in STDIO mode with a pre-opened database:

```
mcp-sqlite /path/to/mydb.sqlite
```

Start in HTTP mode, returning up to 50 rows per query:

```
mcp-sqlite -http -listen 127.0.0.1:9000 -max-rows 50 /path/to/mydb.sqlite
```

Start without a database (client calls `open_database` later):

```
mcp-sqlite
```

## MCP Tools

### `open_database`

Open a SQLite database file. If a database is already open it is closed and replaced. On success, returns the schema of the newly opened database.

**Parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `path` | string | yes | Path to an existing `.db` or `.sqlite` file |

### `get_schema`

Return the schema of the currently open database: tables, views, columns, indexes, and foreign keys.

**Parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `mask` | string | no | SQL `LIKE` pattern to filter table/view names (e.g. `USER_%`) |

### `query`

Execute a read-only SQL statement (`SELECT`, `WITH`, or `EXPLAIN`). Returns up to `-max-rows` rows.

**Parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `sql` | string | yes | A read-only SQL statement |

### `execute`

Execute a write SQL statement (`INSERT`, `UPDATE`, `DELETE`, DDL, etc.). Returns rows affected and last insert ID.

**Parameters:**

| Name | Type | Required | Description |
|---|---|---|---|
| `sql` | string | yes | A SQL statement that modifies the database |

## Client Configuration

### Claude Desktop

Add to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "sqlite": {
      "command": "mcp-sqlite",
      "args": ["/path/to/mydb.sqlite"]
    }
  }
}
```

### OpenCode

Add to your `opencode.jsonc`:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "sqlite": {
      "type": "local",
      "command": ["mcp-sqlite", "/path/to/mydb.sqlite"],
      "enabled": true
    }
  }
}
```

To start without a pre-opened database (and call `open_database` from within the session), omit the path argument:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "sqlite": {
      "type": "local",
      "command": ["mcp-sqlite"],
      "enabled": true
    }
  }
}
```

### HTTP mode

For HTTP mode, start the server manually:

```
mcp-sqlite -http /path/to/mydb.sqlite
```

Then configure any MCP client that supports the streamable HTTP transport to connect to `http://127.0.0.1:8483/mcp`. In OpenCode:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "sqlite": {
      "type": "remote",
      "url": "http://127.0.0.1:8483/mcp",
      "enabled": true
    }
  }
}
```

## Transport

- **STDIO** (default): reads from stdin, writes to stdout. All log output goes to stderr.
- **HTTP**: exposes a streamable HTTP endpoint at `http://<listen>/mcp`. Graceful shutdown on `SIGINT` or `SIGTERM`.

## Development

Run tests:

```
go test ./...
```

Run tests with the race detector:

```
go test -race ./...
```

## License

BSD-2-Clause
