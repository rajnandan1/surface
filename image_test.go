package main

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestImageRunsSurfaceServeAsUser65532AndAnswersHealthz(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("docker", args...).Output()
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("docker %v: %v\n%s", args, err, exit.Stderr)
		} else if err != nil {
			t.Fatalf("docker %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	image := docker("build", "-q", ".")
	config := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(config, []byte("url: https://surface.example.com\ntargets:\n  - https://api.example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	container := docker("run", "-d", "-p", "127.0.0.1::8080", "-v", config+":/etc/surface/config.yaml:ro", image)
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", container).Run() })
	address := docker("port", container, "8080/tcp")

	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		response, err := http.Get("http://" + address + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /healthz did not answer 200 in 30s: %v\n%s", err, docker("logs", container))
		}
	}

	if cmdline := docker("exec", container, "cat", "/proc/1/cmdline"); cmdline != "/usr/local/bin/surface\x00serve\x00--config\x00/etc/surface/config.yaml\x00" {
		t.Errorf("process 1 is %q, want surface serve --config /etc/surface/config.yaml", cmdline)
	}
	if status := docker("exec", container, "cat", "/proc/1/status"); !regexp.MustCompile(`(?m)^Uid:\s+65532\s`).MatchString(status) {
		t.Errorf("process 1 does not run as user 65532:\n%s", status)
	}
}
