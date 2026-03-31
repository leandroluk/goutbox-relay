// _tools/mkdir/main.go
//
// Cross-platform equivalent of `mkdir -p` — creates a directory and all
// parents, silently succeeding if it already exists.
// Replaces the Unix-only `mkdir -p` call in the Makefile so `make coverage`
// works on Windows without shell compatibility issues.
//
// Usage:
//
//	go run ./_tools/mkdir -path=.public
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	path := flag.String("path", "", "directory path to create (required)")
	flag.Parse()

	if *path == "" {
		fmt.Fprintln(os.Stderr, "mkdir: -path is required")
		os.Exit(1)
	}

	if err := os.MkdirAll(*path, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
}
