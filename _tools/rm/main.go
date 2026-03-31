// _tools/rm/main.go
//
// Cross-platform equivalent of `rm -rf` — removes a file or directory.
// Replaces the `rm -rf` shell command in the Makefile so `make clean`
// works on Windows, macOS, and Linux without shell compatibility issues.
//
// Usage:
//
//	go run ./_tools/rm -path=bin
package main

import (
	"flag"
	"os"
)

func main() {
	path := flag.String("path", "", "Folder or file to remove")
	flag.Parse()

	if *path == "" {
		os.Exit(0)
	}

	_ = os.RemoveAll(*path)
}
