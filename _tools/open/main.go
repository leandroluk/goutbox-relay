// _tools/open/main.go
//
// Cross-platform equivalent of `open` / `xdg-open` — opens a file in the
// default system application. No-ops silently when -enabled is not "1".
// Replaces the bash if/case block in the Makefile so `make coverage OPEN=1`
// works on Windows, macOS, and Linux without shell compatibility issues.
//
// Usage:
//
//	go run ./_tools/open -path=.public/coverage.html -enabled=1
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func main() {
	path := flag.String("path", "", "file to open")
	enabled := flag.String("enabled", "", `set to "1" to actually open the file`)
	flag.Parse()

	if *enabled != "1" {
		// Not requested — exit silently.
		return
	}

	if *path == "" {
		fmt.Fprintln(os.Stderr, "open: -path is required")
		os.Exit(1)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", *path)
	case "windows":
		cmd = exec.Command("cmd", "/C", "start", *path)
	default:
		cmd = exec.Command("xdg-open", *path)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Non-fatal: the file was generated successfully; opening it is
		// just a convenience. Log and exit cleanly.
		fmt.Fprintf(os.Stderr, "open: %v\n", err)
	}
}
