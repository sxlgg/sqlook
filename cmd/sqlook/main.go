package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sxlgg/sqlook"
)

func main() {
	port := flag.Int("port", 0, "port to listen on (0 = random available port)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: sqlook [flags] <database.db>\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	e, err := sqlook.New(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer e.Close()

	if err := e.Start(*port); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
