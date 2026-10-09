package main

import (
	_ "embed"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed VERSION
var versionFile string

var version = strings.TrimSpace(versionFile)

func main() {
	os.Exit(cli(os.Args[1:], os.Stdout, os.Stderr))
}

func cli(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			return serve(args[1:], stdout, stderr)
		case "run":
			return runCommand(args[1:], stdout, stderr)
		case "check":
			return checkCommand(args[1:], stdout, stderr)
		}
	}
	fmt.Fprintln(stderr, "usage: surface serve --config <path>\n       surface run --runner <url> --target <url> [--timeout <duration>] <suites>\n       surface check <suites>")
	return 2
}

func serve(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("surface serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "the runner config file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" {
		fmt.Fprintln(stderr, "surface serve: --config is missing")
		return 2
	}
	config, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "surface serve: %v\n", err)
		return 1
	}
	server := &http.Server{Addr: ":8080", Handler: newRunner(stdout, config.targets), ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(stderr, "surface serve: %v\n", server.ListenAndServe())
	return 1
}
