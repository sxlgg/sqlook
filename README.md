# sqlook

A minimal SQLite database explorer. Point it at a `.db` file and browse your data in a clean web UI powered by HTMX.

## Features

- Browse tables, views, and schemas
- Run arbitrary SQL queries (read-only)
- Paginated results
- Keyboard shortcut: `Cmd+Enter` / `Ctrl+Enter` to run queries
- Single binary, no frontend build step
- Importable as a Go library

## Install

### Homebrew (macOS / Linux)

```bash
brew install sxlgg/sqlook/sqlook
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
scoop bucket add sqlook https://github.com/sxlgg/scoop-sqlook
scoop install sqlook
```

### Manual download

Grab the binary for your platform from the [releases page](https://github.com/sxlgg/sqlook/releases).

## Usage

### CLI

```bash
# Random available port
sqlook mydata.db

# Specific port
sqlook --port 8080 mydata.db
```

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

Or mount it inside an existing server:

```go
e, _ := sqlook.New("mydata.db")
http.Handle("/db/", http.StripPrefix("/db", e.Handler()))
http.ListenAndServe(":9000", nil)
```

## API

| Function | Description |
|---|---|
| `sqlook.New(path)` | Open a database (read-only) and return an `*Explorer` |
| `e.Start(port)` | Start the web server. Pass `0` for a random port |
| `e.Handler()` | Get the `http.Handler` for embedding |
| `e.Close()` | Close the database connection |

## Releasing

Tag and push — GitHub Actions builds binaries and publishes to Homebrew and Scoop automatically:

```bash
git tag v0.1.0
git push origin v0.1.0
```

## License

MIT
