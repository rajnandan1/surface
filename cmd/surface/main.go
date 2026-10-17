package main

import (
	"fmt"
	"io"
	"os"

	"github.com/rajnandan1/surface/internal/client"
	"github.com/rajnandan1/surface/internal/runner"
)

func main() {
	os.Exit(cli(os.Args[1:], os.Stdout, os.Stderr))
}

func cli(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return runner.Serve(args[1:], stdout, stderr)
		case "run":
			return client.Run(args[1:], stdout, stderr)
		case "check":
			return client.Check(args[1:], stdout, stderr)
		}
	}
	fmt.Fprintln(stderr, "usage: surface serve --config <path>\n       surface run --runner <url> --target <url> [--timeout <duration>] <suites>\n       surface check <suites>")
	return 2
}
