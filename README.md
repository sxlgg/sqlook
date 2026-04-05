# sqlook

A minimal SQL database explorer. Point it at a SQLite file or a Postgres
connection string and browse your data in a clean web UI powered by HTMX.

## Features

- SQLite and Postgres (local or remote) — auto-detected from the input
- Browse tables, views, and schemas (multi-schema aware on Postgres)
- Run arbitrary SQL queries (read-only, with timing and auto-LIMIT)
- Per-column filters, full-text search, sortable/resizable columns
- Foreign-key navigation — click to jump to the referenced row
- Row detail drawer — click any row for a vertical view with copy buttons
- EXPLAIN button for query plans
- Streaming CSV / JSON export (safe for large tables)
- Query history (client-side, ↑/↓ arrows in editor)
- Keyboard shortcuts: `Cmd+Enter` to run, `Esc` to close drawer/history
- Single binary, no frontend build step
- Importable as a Go library

## Install

### Homebrew (macOS / Linux)

```bash
brew tap sxlgg/sqlook
brew install sqlook
```

### Go

```bash
go install github.com/sxlgg/sqlook/cmd/sqlook@latest
```

### Shell script (macOS / Linux)

```bash
curl -sfL https://raw.githubusercontent.com/sxlgg/sqlook/main/install.sh | sh
```

### Scoop (Windows)

```powershell
scoop bucket add sqlook https://github.com/sxlgg/sqlook
scoop install sqlook
```

### Manual download

Grab the binary for your platform from the [releases page](https://github.com/sxlgg/sqlook/releases).

## Usage

### CLI

```bash
# SQLite file
sqlook mydata.db

# Specific port
sqlook --port 8080 mydata.db

# Postgres — local or remote, read-only session
sqlook 'postgres://user:pass@localhost:5432/mydb?sslmode=disable'
sqlook 'postgresql://user:pass@db.example.com:5432/prod?sslmode=require'

# Profile shortcut (see Profiles below)
sqlook katib

# Bind to all interfaces with basic auth (safe-ish remote serving)
sqlook --bind 0.0.0.0 --port 8080 --auth admin:secret mydata.db
```

Anything starting with `postgres://` or `postgresql://` is treated as a
Postgres DSN; everything else is treated as a SQLite file path or a profile
name.

### Flags

| Flag | Default | Purpose |
|---|---|---|
| `--port N` | `0` (random) | TCP port |
| `--bind ADDR` | `127.0.0.1` | bind address; use `0.0.0.0` to expose to the network |
| `--timeout DUR` | `30s` | Postgres `statement_timeout`; `''` disables |
| `--limit N` | `1000` | auto-append `LIMIT N` to ad-hoc SELECTs without one; `0` disables |
| `--auth USER:PASS` | *(none)* | require HTTP basic auth |

### Safety & read-only

- SQLite is opened with `mode=ro`
- Postgres sessions run with `default_transaction_read_only = on`
  — DDL / DML from the editor is rejected by the server
- Postgres sessions also set `statement_timeout` (default `30s`)
- Ad-hoc SELECTs in the query editor get `LIMIT 1000` appended if they
  don't specify their own LIMIT (see `--limit`)
- Binds to `127.0.0.1` by default; warns if you bind to all interfaces
  without `--auth`, and if you connect to a remote Postgres host without
  `--auth`

### Profiles

sqlook reads `~/.sqlook/profiles` (key=value lines) so you don't have to
paste connection strings every time:

```
# ~/.sqlook/profiles
katib = postgres://postgres:demo@localhost:5433/katib_demo?sslmode=disable
demo  = ./demo.db
```

Then:

```bash
sqlook katib
```

You can also set `SQLOOK_DSN` and run `sqlook` with no arguments.

### As a library

```go
package main

import (
	"log"

	"github.com/sxlgg/sqlook"
)

func main() {
	e, err := sqlook.New("mydata.db")
	if err != nil {
		log.Fatal(err)
	}
	defer e.Close()
	log.Fatal(e.Start(8080))
}
```

With custom options:

```go
e, _ := sqlook.NewWithOptions("postgres://...", sqlook.Options{
	StatementTimeout: "10s",
	AutoLimit:        500,
	BasicAuthUser:    "admin",
	BasicAuthPass:    "secret",
})
log.Fatal(e.StartOn("0.0.0.0", 8080))
```

Or mount inside an existing server:

```go
e, _ := sqlook.New("mydata.db")
http.Handle("/db/", http.StripPrefix("/db", e.Handler()))
http.ListenAndServe(":9000", nil)
```

## API

| Function | Description |
|---|---|
| `sqlook.New(connStr)` | Open a SQLite file or Postgres DSN (read-only) with `DefaultOptions` |
| `sqlook.NewWithOptions(connStr, opts)` | Same, with custom `Options` |
| `e.Start(port)` | Start the web server on `127.0.0.1` (pass `0` for a random port) |
| `e.StartOn(bind, port)` | Start with a custom bind address |
| `e.Handler()` | Get the `http.Handler` (with auth middleware applied) |
| `e.Close()` | Close the database connection |

## Releasing

Tag and push — GitHub Actions builds binaries and publishes to Homebrew and Scoop automatically:

```bash
git tag v0.1.0
git push origin v0.1.0
```

## License

MIT
