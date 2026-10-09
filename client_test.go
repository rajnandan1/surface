package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const usersSuite = `apiVersion: surface/v1
kind: API
tests:
  - name: health
    method: GET
    path: /health
    expect:
      status: 200
  - name: list-users
    method: GET
    path: /users
    expect:
      status: 200
`

func TestRunPrintsTheResultAndExits1WhenATestFails(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer target.Close()
	runner := httptest.NewServer(newRunner(io.Discard, targetList(t, target.URL)))
	defer runner.Close()
	defer func(interval time.Duration) { pollInterval = interval }(pollInterval)
	pollInterval = 10 * time.Millisecond

	t.Chdir(t.TempDir())
	if err := os.MkdirAll("surface/api", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("surface/api/users.yaml", []byte(usersSuite), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", runner.URL, "--target", target.URL, "--timeout", "1m", "surface/"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code is %d, want 1\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
	log := regexp.MustCompile(`\b[0-9a-z]{26}\b`).ReplaceAllString(stdout.String(), "<id>")
	log = strings.ReplaceAll(log, target.URL, "<target>")
	want := `surface 1.0.0 run <id> target <target>
surface/api/users.yaml: 1 passed, 1 failed

surface/api/users.yaml list-users
  status: expected 200, got 500

run <id>: 1 passed, 1 failed
`
	if log != want {
		t.Fatalf("CI log is\n%s\nwant\n%s", log, want)
	}
}

func TestRunWithABrokenSuiteStillRunsTheValidSuite(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	runner := httptest.NewServer(newRunner(io.Discard, targetList(t, target.URL)))
	defer runner.Close()
	defer func(interval time.Duration) { pollInterval = interval }(pollInterval)
	pollInterval = 10 * time.Millisecond

	t.Chdir(t.TempDir())
	writeSuite(t, "surface/api/users.yaml", usersSuite)
	writeSuite(t, "surface/api/broken.yaml", strings.Replace(usersSuite, "status: 200", "staus: 200", 1))

	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", runner.URL, "--target", target.URL, "--timeout", "1m", "surface/"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code is %d, want 1\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
	log := regexp.MustCompile(`\b[0-9a-z]{26}\b`).ReplaceAllString(stdout.String(), "<id>")
	log = strings.ReplaceAll(log, target.URL, "<target>")
	want := `surface 1.0.0 run <id> target <target>
suite surface/api/broken.yaml: line 8: unknown field "staus" (runner 1.0.0)
surface/api/users.yaml: 2 passed

run <id>: 2 passed
`
	if log != want {
		t.Fatalf("CI log is\n%s\nwant\n%s", log, want)
	}
}

func writeSuite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithATargetNotInTheTargetListSendsNoRequest(t *testing.T) {
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer target.Close()
	runner := httptest.NewServer(newRunner(io.Discard, targetList(t, "https://api.crustdata.com")))
	defer runner.Close()
	defer func(interval time.Duration) { pollInterval = interval }(pollInterval)
	pollInterval = 10 * time.Millisecond

	t.Chdir(t.TempDir())
	writeSuite(t, "surface/api/users.yaml", usersSuite)

	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", runner.URL, "--target", target.URL, "--timeout", "1m", "surface/"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code is %d, want 2\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
	if want := `run: target "` + target.URL + `" is not in the target list` + "\n"; stderr.String() != want {
		t.Fatalf("stderr is %q, want %q", stderr.String(), want)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("the target got %d requests, want 0", n)
	}
}

func TestRunWithNoSuitesExits2(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("surface", 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", "http://127.0.0.1:1", "--target", "http://127.0.0.1:1", "surface/"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code is %d, want 2", code)
	}
	if want := "surface run: no suites under surface/\n"; stderr.String() != want {
		t.Fatalf("stderr is %q, want %q", stderr.String(), want)
	}
}
