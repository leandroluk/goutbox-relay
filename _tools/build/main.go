// _tools/build/main.go
//
// Cross-platform wrapper for `go build` that sets CGO_ENABLED=0 and
// GOOS=linux without relying on shell inline-env syntax (which breaks
// on Windows cmd/PowerShell), and applies ldflags without passing them
// through the shell (which breaks quoting on Windows).
//
// Called by `make build`:
//
//	go run ./_tools/build -out=bin/goutbox-relay -pkg=./cmd/relay
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	out := flag.String("out", "bin/goutbox-relay", "output binary path (-o)")
	pkg := flag.String("pkg", "./cmd/relay", "package to build")
	flag.Parse()

	args := []string{
		"build",
		// Strip symbol table and DWARF debug info — reduces binary size ~30%.
		// Hardcoded here instead of passed via -flags to avoid shell quoting
		// issues with the inner quotes on Windows.
		"-ldflags=-s -w",
		// Remove local filesystem paths from the binary for reproducibility.
		"-trimpath",
		"-o", *out,
		*pkg,
	}

	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set env vars programmatically — works identically on all platforms.
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS=linux",
	)

	fmt.Printf("go %s\n", strings.Join(args, " "))

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n", err)
		os.Exit(1)
	}
}
