package main

import (
	"bytes"
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

func inRepoWithASuite(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("surface/api", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("surface/api/users.yaml", []byte(usersSuite), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunPrintsTheResultAndExits1WhenATestFails(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer target.Close()
	issuer := newFakeIssuer(t)
	fakeGitHubJob(t, func(audience string) string { return issuer.sign(issuer.claims(audience)) })
	runner := startRunner(t, issuer, target.URL)
	defer func(interval time.Duration) { pollInterval = interval }(pollInterval)
	pollInterval = 10 * time.Millisecond

	inRepoWithASuite(t)

	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", runner.URL, "--target", target.URL, "--timeout", "1m", "surface/"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code is %d, want 1\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
	log := regexp.MustCompile(`\b[0-9a-z]{26}\b`).ReplaceAllString(stdout.String(), "<id>")
	log = strings.ReplaceAll(log, target.URL, "<target>")
	log = regexp.MustCompile(`in \d+ms +`).ReplaceAllString(log, "in <N>ms ")
	log = regexp.MustCompile(`Date: .*`).ReplaceAllString(log, "Date: <date>")
	want := `surface 1.0.0 run <id> target <target>
surface/api/users.yaml: 1 passed, 1 failed

surface/api/users.yaml list-users
  status: expected 200, got 500
  > GET <target>/users
  > User-Agent: surface/1.0.0 (run <id>)
  < 500 Internal Server Error in <N>ms ✗ expected 200
  < Content-Length: 0
  < Date: <date>

run <id>: 1 passed, 1 failed
::error file=surface/api/users.yaml,line=9::list-users: status: expected 200, got 500
`
	if log != want {
		t.Fatalf("CI log is\n%s\nwant\n%s", log, want)
	}
}

func TestRunWithABrokenSuiteStillRunsTheValidSuite(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	issuer := newFakeIssuer(t)
	fakeGitHubJob(t, func(audience string) string { return issuer.sign(issuer.claims(audience)) })
	runner := startRunner(t, issuer, target.URL)
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
::error file=surface/api/broken.yaml,line=1::suite surface/api/broken.yaml: line 8: unknown field "staus" (runner 1.0.0)
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
	issuer := newFakeIssuer(t)
	fakeGitHubJob(t, func(audience string) string { return issuer.sign(issuer.claims(audience)) })
	runner := startRunner(t, issuer, "https://api.crustdata.com")
	defer func(interval time.Duration) { pollInterval = interval }(pollInterval)
	pollInterval = 10 * time.Millisecond
	inRepoWithASuite(t)

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

func TestRunOutsideGitHubActionsExits2(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	inRepoWithASuite(t)

	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", "http://127.0.0.1:1", "--target", "http://127.0.0.1:1", "surface/"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code is %d, want 2", code)
	}
	if want := "surface run: no OIDC token: not inside GitHub Actions, or the job is missing permissions: id-token: write\n"; stderr.String() != want {
		t.Fatalf("stderr is %q, want %q", stderr.String(), want)
	}
}

func TestRunSendsTheRunnerURLWithoutItsTrailingSlashAsTheAudience(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	issuer := newFakeIssuer(t)
	fakeGitHubJob(t, func(audience string) string { return issuer.sign(issuer.claims(audience)) })
	runner := startRunner(t, issuer, target.URL)
	defer func(interval time.Duration) { pollInterval = interval }(pollInterval)
	pollInterval = 10 * time.Millisecond
	inRepoWithASuite(t)

	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", runner.URL + "/", "--target", target.URL, "surface/"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code is %d, want 0\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
}

func TestRunFetchesAFreshTokenOnceAfterA401(t *testing.T) {
	tests := []struct {
		name    string
		expired func(fetch int32) bool
		code    int
	}{
		{"the first token expired", func(fetch int32) bool { return fetch == 1 }, 0},
		{"every token expired", func(int32) bool { return true }, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			defer target.Close()
			issuer := newFakeIssuer(t)
			var fetches atomic.Int32
			fakeGitHubJob(t, func(audience string) string {
				claims := issuer.claims(audience)
				if test.expired(fetches.Add(1)) {
					claims["exp"] = time.Now().Add(-5 * time.Minute).Unix()
				}
				return issuer.sign(claims)
			})
			runner := startRunner(t, issuer, target.URL)
			defer func(interval time.Duration) { pollInterval = interval }(pollInterval)
			pollInterval = 10 * time.Millisecond
			inRepoWithASuite(t)

			var stdout, stderr bytes.Buffer
			code := cli([]string{"run", "--runner", runner.URL, "--target", target.URL, "surface/"}, &stdout, &stderr)

			if code != test.code {
				t.Fatalf("exit code is %d, want %d\nstdout:\n%s\nstderr:\n%s", code, test.code, &stdout, &stderr)
			}
			if fetches.Load() != 2 {
				t.Fatalf("the client fetched %d tokens, want 2", fetches.Load())
			}
		})
	}
}
