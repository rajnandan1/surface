package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const fakeRunID = "01j9z3k4m5n6p7q8r9s0t1u2v3"

// fakeRunner accepts every run request as run fakeRunID and answers the n-th poll with polls[n], then the last one again.
func fakeRunner(t *testing.T, polls ...http.HandlerFunc) *httptest.Server {
	t.Helper()
	var n atomic.Int64
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintf(w, `{"id": %q}`, fakeRunID)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/runs/"+fakeRunID:
			polls[min(int(n.Add(1)), len(polls))-1](w, r)
		default:
			t.Errorf("the runner got %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(runner.Close)
	return runner
}

func reply(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

func hangUp(w http.ResponseWriter, _ *http.Request) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		conn.Close()
	}
}

func runAgainst(t *testing.T, runner, timeout, suiteText string) (code int, stdout, stderr string) {
	t.Helper()
	fakeGitHubJob(t, func(string) string { return "token" })
	defer func(interval time.Duration) { PollInterval = interval }(PollInterval)
	PollInterval = 10 * time.Millisecond
	t.Chdir(t.TempDir())
	writeSuite(t, "surface/api/users.yaml", suiteText)
	var out, errOut bytes.Buffer
	code = Run([]string{"--runner", runner, "--target", "https://pr-123.preview.crustdata.com", "--timeout", timeout, "surface/"}, &out, &errOut)
	return code, out.String(), errOut.String()
}

const runningRun = `{"id": "` + fakeRunID + `", "state": "running", "suites": []}`

const passedRun = `{"id": "` + fakeRunID + `", "state": "passed", "suites": [
  {"path": "surface/api/users.yaml", "state": "passed", "tests": [{"name": "health", "state": "passed"}, {"name": "list-users", "state": "passed"}]}
]}`

func TestRunPollsAgainAfterA5xxOrANetworkError(t *testing.T) {
	runner := fakeRunner(t, reply(http.StatusBadGateway, "bad gateway"), hangUp, reply(http.StatusOK, runningRun), reply(http.StatusServiceUnavailable, ""), reply(http.StatusOK, passedRun))

	code, stdout, stderr := runAgainst(t, runner.URL, "1m", usersSuite)

	if code != 0 {
		t.Fatalf("exit code is %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
}

func TestRunExits2WhenThePollAnswers404(t *testing.T) {
	runner := fakeRunner(t, reply(http.StatusOK, runningRun), reply(http.StatusNotFound, `run: run "`+fakeRunID+`" is not known`+"\n"))

	code, stdout, stderr := runAgainst(t, runner.URL, "1m", usersSuite)

	if code != 2 {
		t.Fatalf("exit code is %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if want := `run: run "` + fakeRunID + `" is not known` + "\n"; stderr != want {
		t.Fatalf("stderr is %q, want %q", stderr, want)
	}
}

func TestRunExits2WhenTheRunnerHasNoRunsRoute(t *testing.T) {
	runner := httptest.NewServer(http.NotFoundHandler())
	defer runner.Close()

	code, stdout, stderr := runAgainst(t, runner.URL+"/", "1m", usersSuite)

	if code != 2 {
		t.Fatalf("exit code is %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if want := "surface run: runner at " + runner.URL + " has no /v1/runs: it may be a different major version\n"; stderr != want {
		t.Fatalf("stderr is %q, want %q", stderr, want)
	}
}

func TestRunExits2WhenTheWaitLimitPassesAndLeavesTheRunRunning(t *testing.T) {
	runner := fakeRunner(t, reply(http.StatusOK, runningRun))

	code, stdout, stderr := runAgainst(t, runner.URL, "100ms", usersSuite)

	if code != 2 {
		t.Fatalf("exit code is %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if want := "surface run: run " + fakeRunID + " did not finish within 100ms\n"; stderr != want {
		t.Fatalf("stderr is %q, want %q", stderr, want)
	}
}

func TestRunIgnoresUnknownFieldsInTheRunResult(t *testing.T) {
	runner := fakeRunner(t, reply(http.StatusOK, `{"id": "`+fakeRunID+`", "state": "passed", "shard": {"zone": "b"}, "suites": [
  {"path": "surface/api/users.yaml", "state": "passed", "duration_ms": 41, "tests": [
    {"name": "health", "state": "passed", "attempts": 1},
    {"name": "list-users", "state": "passed", "cleanup": null}
  ]}
]}`))

	code, stdout, stderr := runAgainst(t, runner.URL, "1m", usersSuite)

	if code != 0 {
		t.Fatalf("exit code is %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	want := `surface 1.0.1 run ` + fakeRunID + ` target https://pr-123.preview.crustdata.com
surface/api/users.yaml: 2 passed

run ` + fakeRunID + `: 2 passed
`
	if stdout != want {
		t.Fatalf("CI log is\n%s\nwant\n%s", stdout, want)
	}
}
