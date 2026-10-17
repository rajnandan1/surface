package runner

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rajnandan1/surface/internal/api"
	"github.com/rajnandan1/surface/internal/client"
)

const createThingSuite = `tests:
  - name: create-thing
    method: POST
    path: /things
    expect:
      status: 201
    cleanup:
      method: DELETE
      path: /things/1
      expect:
        status: 204
`

const slowSuite = createThingSuite + `  - name: slow-thing
    needs: [create-thing]
    method: GET
    path: /slow
    expect:
      status: 200
  - name: after-slow
    needs: [slow-thing]
    method: GET
    path: /after
    expect:
      status: 200
`

type slowTarget struct {
	*httptest.Server
	arrived chan struct{}
	release func()
	mu      sync.Mutex
	sent    []string
}

func newSlowTarget(t *testing.T, slow, cleanup time.Duration) *slowTarget {
	t.Helper()
	gate := make(chan struct{})
	target := &slowTarget{arrived: make(chan struct{}, 1), release: sync.OnceFunc(func() { close(gate) })}
	if slow > 0 {
		time.AfterFunc(slow, target.release)
	}
	target.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target.mu.Lock()
		target.sent = append(target.sent, r.Method+" "+r.URL.Path)
		target.mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "POST /things":
			w.WriteHeader(http.StatusCreated)
		case "GET /slow":
			target.arrived <- struct{}{}
			<-gate
		case "DELETE /things/1":
			time.Sleep(cleanup)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(target.Close)
	t.Cleanup(target.release)
	return target
}

func (target *slowTarget) requests() []string {
	target.mu.Lock()
	defer target.mu.Unlock()
	return slices.Clone(target.sent)
}

func limitedRunner(t *testing.T, issuer *fakeIssuer, url, runTime string, targets ...string) *runner {
	t.Helper()
	runner, err := newRunner(io.Discard, config{url: url, targets: targetList(t, targets...), issuer: issuer.url, ownerIDs: []string{crustdataOwnerID}, runTime: runTime, resultsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.active.Wait)
	return runner
}

func inRepoWith(t *testing.T, suite string) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("surface/api", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("surface/api", "users.yaml"), []byte(suite), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSIGINTToTheClientCancelsTheRunAndItsCleanupRuns(t *testing.T) {
	target := newSlowTarget(t, 0, 0)
	issuer := newFakeIssuer(t)
	fakeGitHubJob(t, func(audience string) string { return issuer.sign(issuer.claims(audience)) })
	server := startRunner(t, issuer, target.URL)
	defer func(interval time.Duration) { client.PollInterval = interval }(client.PollInterval)
	client.PollInterval = 10 * time.Millisecond
	inRepoWith(t, header+slowSuite)

	var stdout, stderr bytes.Buffer
	exited := make(chan int)
	go func() {
		exited <- client.Run([]string{"--runner", server.URL, "--target", target.URL, "surface/"}, &stdout, &stderr)
	}()
	<-target.arrived
	syscall.Kill(os.Getpid(), syscall.SIGINT)
	code := <-exited
	target.release()

	if code != 2 {
		t.Fatalf("exit code is %d, want 2\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
	id := strings.Fields(stdout.String())[3]
	if want := "surface run: interrupt signal received: cancelled run " + id + "\n"; stderr.String() != want {
		t.Fatalf("stderr is %q, want %q", stderr.String(), want)
	}
	result := waitForRun(t, server.Config.Handler, issuer.sign(issuer.claims(server.URL)), id)
	tests := result.Suites[0].Tests
	if result.State != "cancelled" || tests[0].Cleanup == nil || tests[0].Cleanup.State != "passed" || tests[2].State != "pending" {
		t.Fatalf("run is %s with tests %+v, want cancelled with the cleanup of create-thing passed and after-slow pending", result.State, tests)
	}
	if want := []string{"POST /things", "GET /slow", "DELETE /things/1"}; !slices.Equal(target.requests(), want) {
		t.Fatalf("the target got %q, want %q", target.requests(), want)
	}
}

func TestOnlyTheRunsRepoMayCancelIt(t *testing.T) {
	issuer := newFakeIssuer(t)
	runner := newTestRunner(t, issuer, io.Discard, runnerURL, "http://127.0.0.1:1")
	token := issuer.sign(issuer.claims(runnerURL))
	id := startRun(t, runner, token, "http://127.0.0.1:1", header+"tests: []\n")
	other := issuer.sign(with(issuer.claims(runnerURL), "repository", "crustdata/web"))

	cases := []struct {
		path, token string
		code        int
		body        string
	}{
		{"/v1/runs/" + id + "/cancel", other, 403, `run: repo "crustdata/web" may not cancel run ` + id + "\n"},
		{"/v1/runs/01j9z3k4m5n6p7q8r9s0t1u2v3/cancel", token, 404, `run: run "01j9z3k4m5n6p7q8r9s0t1u2v3" is not known` + "\n"},
		{"/v1/runs/" + id + "/cancel", token, 202, ""},
	}
	for _, c := range cases {
		if response := call(runner, http.MethodPost, c.path, c.token, nil); response.Code != c.code || response.Body.String() != c.body {
			t.Errorf("POST %s answers %d %q, want %d %q", c.path, response.Code, response.Body.String(), c.code, c.body)
		}
	}
}

func TestARunPastItsRunTimeLimitEndsInErrorAndItsCleanupRuns(t *testing.T) {
	target := newSlowTarget(t, 400*time.Millisecond, 0)
	issuer := newFakeIssuer(t)
	fakeGitHubJob(t, func(audience string) string { return issuer.sign(issuer.claims(audience)) })
	var runner *runner
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { runner.ServeHTTP(w, r) }))
	defer server.Close()
	runner = limitedRunner(t, issuer, server.URL, "100ms", target.URL)
	defer func(interval time.Duration) { client.PollInterval = interval }(client.PollInterval)
	client.PollInterval = 10 * time.Millisecond
	inRepoWith(t, header+slowSuite)

	var stdout, stderr bytes.Buffer
	code := client.Run([]string{"--runner", server.URL, "--target", target.URL, "surface/"}, &stdout, &stderr)

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	id := strings.Fields(lines[0])[3]
	if want := "run " + id + ": error: run time limit of 100ms passed"; code != 2 || lines[len(lines)-1] != want {
		t.Fatalf("exit code is %d with last line %q, want 2 and %q\n%s%s", code, lines[len(lines)-1], want, &stdout, &stderr)
	}
	if want := []string{"POST /things", "GET /slow", "DELETE /things/1"}; !slices.Equal(target.requests(), want) {
		t.Fatalf("the target got %q, want %q", target.requests(), want)
	}
}

func TestACleanupThatCannotStartWithinTheWindowFails(t *testing.T) {
	defer func(window time.Duration) { cleanupWindow = window }(cleanupWindow)
	cleanupWindow = 50 * time.Millisecond
	target := newSlowTarget(t, 400*time.Millisecond, 0)
	issuer := newFakeIssuer(t)
	runner := limitedRunner(t, issuer, runnerURL, "50ms", target.URL)
	token := issuer.sign(issuer.claims(runnerURL))

	result := waitForRun(t, runner, token, startRun(t, runner, token, target.URL, header+slowSuite))

	cleanup := result.Suites[0].Tests[0].Cleanup
	if result.State != "error" || result.Reason != "run time limit of 50ms passed" || cleanup == nil || *cleanup != (api.CleanupResult{State: "failed", Failure: "not sent: run time limit passed"}) {
		t.Fatalf("run is %s (%s) with cleanup %+v, want error with a failed cleanup", result.State, result.Reason, cleanup)
	}
	if want := []string{"POST /things", "GET /slow"}; !slices.Equal(target.requests(), want) {
		t.Fatalf("the target got %q, want %q", target.requests(), want)
	}
}

func TestTheRunTimeLimitStopsNothingWhenEveryTestHasEnded(t *testing.T) {
	target := newSlowTarget(t, 0, 300*time.Millisecond)
	issuer := newFakeIssuer(t)
	runner := limitedRunner(t, issuer, runnerURL, "100ms", target.URL)
	token := issuer.sign(issuer.claims(runnerURL))

	result := waitForRun(t, runner, token, startRun(t, runner, token, target.URL, header+createThingSuite))

	if result.State != "passed" || result.Reason != "" || result.Suites[0].Tests[0].Cleanup == nil {
		t.Fatalf("run is %s (%s) with tests %+v, want passed with the cleanup run", result.State, result.Reason, result.Suites[0].Tests)
	}
}

func TestSIGTERMStopsTheRunnerAfterItsRunsEndWithTheirCleanups(t *testing.T) {
	target := newSlowTarget(t, 0, 0)
	issuer := newFakeIssuer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	runner := limitedRunner(t, issuer, url, "", target.URL)
	token := issuer.sign(issuer.claims(url))
	signals := make(chan os.Signal, 1)
	exited := make(chan int)
	go func() { exited <- serveUntil(runner, listener, signals, io.Discard) }()

	id := startRun(t, runner, token, target.URL, header+slowSuite)
	<-target.arrived
	signals <- syscall.SIGTERM
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		response, err := http.Get(url + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("/healthz answers %d while the runner stops, want 503", response.StatusCode)
		}
	}
	if response := call(runner, http.MethodPost, "/v1/runs", token, strings.NewReader(`{"target": "`+target.URL+`", "suites": {}}`)); response.Code != 503 || response.Header().Get("Retry-After") != "5" {
		t.Fatalf("a run request while the runner stops answers %d with Retry-After %q, want 503 and 5", response.Code, response.Header().Get("Retry-After"))
	}
	target.release()

	if code := <-exited; code != 0 {
		t.Fatalf("serve exits %d, want 0", code)
	}
	result := waitForRun(t, runner, token, id)
	if result.State != "error" || result.Reason != "runner stopped" || result.Suites[0].Tests[0].Cleanup == nil {
		t.Fatalf("run is %s (%s) with tests %+v, want error, runner stopped, with the cleanup run", result.State, result.Reason, result.Suites[0].Tests)
	}
	if want := []string{"POST /things", "GET /slow", "DELETE /things/1"}; !slices.Equal(target.requests(), want) {
		t.Fatalf("the target got %q, want %q", target.requests(), want)
	}
}
