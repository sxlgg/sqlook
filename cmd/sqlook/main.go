package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sxlgg/sqlook"
)

func main() {
	port := flag.Int("port", 0, "port to listen on (0 = random available port)")
	bind := flag.String("bind", "127.0.0.1", "bind address ('0.0.0.0' to expose to the network)")
	stmtTimeout := flag.String("timeout", "30s", "Postgres statement_timeout (e.g. '30s', '2min', '' to disable)")
	autoLimit := flag.Int("limit", 1000, "auto-append LIMIT N to ad-hoc SELECTs without an explicit LIMIT (0 disables)")
	authFlag := flag.String("auth", "", "require HTTP basic auth as 'user:pass'")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: sqlook [flags] <target>

<target> is one of:
  - a SQLite file path               (e.g. mydata.db)
  - a Postgres DSN                   (postgres://user:pass@host:port/db)
  - a profile name from ~/.sqlook/profiles  (e.g. 'katib')

Environment:
  SQLOOK_DSN       used when no target is given on the command line

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	target := ""
	if flag.NArg() >= 1 {
		target = flag.Arg(0)
	} else if env := os.Getenv("SQLOOK_DSN"); env != "" {
		target = env
	} else {
		flag.Usage()
		os.Exit(1)
	}

	// Resolve profile name → connection string if the target doesn't look
	// like a file path or a DSN.
	target = resolveProfile(target)

	// Warn when binding to all interfaces without auth.
	if (*bind == "0.0.0.0" || *bind == "") && *authFlag == "" {
		fmt.Fprintln(os.Stderr, "warning: serving on all interfaces with no auth — use --auth user:pass or --bind 127.0.0.1")
	}
	// Warn when pointing at remote Postgres without auth.
	if strings.HasPrefix(target, "postgres://") || strings.HasPrefix(target, "postgresql://") {
		if !isLocalDSN(target) && *authFlag == "" {
			fmt.Fprintln(os.Stderr, "warning: connecting to a remote Postgres host — anyone who can reach this port can browse your data. Consider --auth user:pass.")
		}
	}

	opts := sqlook.Options{
		StatementTimeout: *stmtTimeout,
		AutoLimit:        *autoLimit,
	}
	if *authFlag != "" {
		parts := strings.SplitN(*authFlag, ":", 2)
		if len(parts) != 2 {
			fmt.Fprintln(os.Stderr, "error: --auth must be 'user:pass'")
			os.Exit(1)
		}
		opts.BasicAuthUser, opts.BasicAuthPass = parts[0], parts[1]
	}

	e, err := sqlook.NewWithOptions(target, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer e.Close()

	if err := e.StartOn(*bind, *port); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// resolveProfile checks ~/.sqlook/profiles for a matching name. The file is a
// simple key=value format, one per line. Lines starting with # are comments.
//
//	katib = postgres://postgres:demo@localhost:5433/katib_demo?sslmode=disable
//	demo  = ./demo.db
func resolveProfile(target string) string {
	// If it looks like a path or DSN, don't try.
	if strings.ContainsAny(target, "/:\\.") {
		return target
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return target
	}
	path := filepath.Join(home, ".sqlook", "profiles")
	f, err := os.Open(path)
	if err != nil {
		return target
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		name := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if name == target {
			return val
		}
	}
	return target
}

// isLocalDSN returns true if the host in the DSN is localhost or 127.x.x.x / ::1.
func isLocalDSN(dsn string) bool {
	// Very loose parse: find "://host[:port]/"
	i := strings.Index(dsn, "://")
	if i < 0 {
		return false
	}
	rest := dsn[i+3:]
	// strip user:pass@
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	// host ends at : or /
	end := len(rest)
	if j := strings.IndexAny(rest, ":/?"); j >= 0 {
		end = j
	}
	host := rest[:end]
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.")
}
