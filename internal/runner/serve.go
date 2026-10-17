package runner

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func Serve(args []string, stdout, stderr io.Writer) int {
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
	c, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "surface serve: %v\n", err)
		return 1
	}
	runner, err := newRunner(stdout, c)
	if err != nil {
		fmt.Fprintf(stderr, "surface serve: %v\n", err)
		return 1
	}
	go func() {
		for range time.Tick(time.Hour) {
			runner.sweep()
		}
	}()
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		fmt.Fprintf(stderr, "surface serve: %v\n", err)
		return 1
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	return serveUntil(runner, listener, signals, stderr)
}

func serveUntil(runner *runner, listener net.Listener, signals <-chan os.Signal, stderr io.Writer) int {
	server := &http.Server{Handler: runner, ReadHeaderTimeout: 10 * time.Second}
	stopped := make(chan struct{})
	go func() {
		<-signals
		runner.shutdown()
		server.Shutdown(context.Background())
		close(stopped)
	}()
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "surface serve: %v\n", err)
		return 1
	}
	<-stopped
	return 0
}
