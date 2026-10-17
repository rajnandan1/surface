package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rajnandan1/surface/internal/api"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func writeRunFile(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readRunFile(t *testing.T, dir, id string) api.RunResult {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var run api.RunResult
	if err := json.Unmarshal(text, &run); err != nil {
		t.Fatal(err)
	}
	return run
}

func runFile(id, state string) string {
	return fmt.Sprintf(`{"id": %q, "state": %q, "caller": {"repo": "crustdata/api"}, "target": "https://api.crustdata.com", "suites": [{"path": "surface/api/users.yaml", "state": "running"}]}`, id, state)
}

func TestAtStartAFileOfARunNotEndedBecomesErrorRunnerRestarted(t *testing.T) {
	dir := t.TempDir()
	interrupted, finished := newRunID(time.Now()), newRunID(time.Now())
	writeRunFile(t, dir, interrupted+".json", runFile(interrupted, "running"))
	writeRunFile(t, dir, finished+".json", runFile(finished, "passed"))
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))

	runner := boundedRunner(t, issuer, runnerURL, config{targets: targetList(t, "https://api.crustdata.com"), resultsDir: dir})

	run := readRunFile(t, dir, interrupted)
	if run.State != "error" || run.Reason != "runner restarted" || len(run.Suites) != 1 || run.Suites[0].Path != "surface/api/users.yaml" {
		t.Fatalf("the interrupted run's file is %+v, want error, runner restarted, with its suite kept", run)
	}
	if text, _ := os.ReadFile(filepath.Join(dir, finished+".json")); string(text) != runFile(finished, "passed") {
		t.Fatalf("the finished run's file is %s, want it unchanged", text)
	}
	if response := call(runner, http.MethodGet, "/v1/runs/"+interrupted, token, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"reason":"runner restarted"`) {
		t.Fatalf("GET answers %d %s, want 200 with the reason runner restarted", response.Code, response.Body)
	}
}

func TestWithAReadOnlyFolderARunStaysInMemoryAndCountsAgainstTheCap(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only folder")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer api.Close()
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	var log lockedBuffer
	runner, err := newRunner(&log, config{url: runnerURL, targets: targetList(t, api.URL), issuer: issuer.url, ownerIDs: []string{crustdataOwnerID}, runs: 1, resultsDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.active.Wait)

	id := startRun(t, runner, token, api.URL, suiteOf(1))
	if !strings.Contains(log.String(), "permission denied") {
		t.Fatalf("the log is %q, want the OS error of the failed write", log.String())
	}
	waitForPass(t, runner, token, id)
	time.Sleep(50 * time.Millisecond)

	if response := call(runner, http.MethodGet, "/v1/runs/"+id, token, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"state":"passed"`) {
		t.Fatalf("GET answers %d %s, want 200 with the run from memory", response.Code, response.Body)
	}
	response := call(runner, http.MethodPost, "/v1/runs", token, strings.NewReader(fmt.Sprintf(`{"target": %q, "suites": {}}`, api.URL)))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("a second run request gets %d %s, want 503 while the first run is still in memory", response.Code, response.Body)
	}
}

func TestAtStartTheRunnerCreatesAMissingResultsFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "var", "lib", "surface", "results")
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer api.Close()
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	runner := boundedRunner(t, issuer, runnerURL, config{targets: targetList(t, api.URL), resultsDir: dir})

	id := startRun(t, runner, token, api.URL, suiteOf(1))
	if _, err := os.Stat(filepath.Join(dir, id+".json")); err != nil {
		t.Fatalf("no file after the run was accepted: %v", err)
	}
	waitForPass(t, runner, token, id)
}

func TestTheSweepDeletesFilesOfOldRunsAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	old, fresh := newRunID(time.Now().Add(-48*time.Hour)), newRunID(time.Now().Add(-time.Hour))
	writeRunFile(t, dir, old+".json", runFile(old, "passed"))
	writeRunFile(t, dir, old+".123456.tmp", "{")
	writeRunFile(t, dir, fresh+".json", runFile(fresh, "passed"))
	writeRunFile(t, dir, "notes.txt", "kept by the operator")
	if err := os.Mkdir(filepath.Join(dir, "lost+found"), 0o700); err != nil {
		t.Fatal(err)
	}

	boundedRunner(t, newFakeIssuer(t), runnerURL, config{targets: targetList(t, "https://api.crustdata.com"), resultsDir: dir, retention: "24h"})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if want := []string{fresh + ".json", "lost+found", "notes.txt"}; strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("the folder holds %q, want %q", names, want)
	}
}

func TestAFinishedRunIsReadableAfterItLeavesMemory(t *testing.T) {
	outside := t.TempDir()
	dir := filepath.Join(outside, "results")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRunFile(t, outside, "elsewhere.json", runFile(newRunID(time.Now()), "passed"))
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer api.Close()
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	runner := boundedRunner(t, issuer, runnerURL, config{targets: targetList(t, api.URL), resultsDir: dir})

	id := startRun(t, runner, token, api.URL, suiteOf(2))
	if _, err := os.Stat(filepath.Join(dir, id+".json")); err != nil {
		t.Fatalf("no file after the run was accepted: %v", err)
	}
	waitForPass(t, runner, token, id)
	for deadline := time.Now().Add(5 * time.Second); inMemory(runner, id); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("run %s is still in memory 5s after it ended", id)
		}
	}

	response := call(runner, http.MethodGet, "/v1/runs/"+id, token, nil)
	if file, _ := os.ReadFile(filepath.Join(dir, id+".json")); response.Code != http.StatusOK || response.Body.String() != string(file) {
		t.Fatalf("GET answers %d %s, want 200 with the file %s", response.Code, response.Body, file)
	}
	if run := readRunFile(t, dir, id); run.State != "passed" || run.Caller.Repo != "crustdata/api" || len(run.Suites[0].Tests) != 2 {
		t.Fatalf("the file holds %+v, want the passed run with its caller and 2 tests", run)
	}
	stranger := issuer.sign(with(issuer.claims(runnerURL), "repository", "crustdata/web"))
	cases := []struct {
		path, token string
		code        int
		body        string
	}{
		{"/v1/runs/" + id, stranger, http.StatusForbidden, fmt.Sprintf("run: repo \"crustdata/web\" may not read run %s\n", id)},
		{"/v1/runs/" + newRunID(time.Now()), token, http.StatusNotFound, ""},
		{"/v1/runs/..%2Felsewhere", token, http.StatusNotFound, "run: run \"../elsewhere\" is not known\n"},
	}
	for _, c := range cases {
		response := call(runner, http.MethodGet, c.path, c.token, nil)
		if response.Code != c.code || c.body != "" && response.Body.String() != c.body {
			t.Errorf("GET %s answers %d %q, want %d %q", c.path, response.Code, response.Body, c.code, c.body)
		}
	}
}

func inMemory(runner *runner, id string) bool {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	_, ok := runner.runs[id]
	return ok
}

func TestTheFileHoldsEachSuiteThatEndedWhileTheRunGoesOn(t *testing.T) {
	dir := t.TempDir()
	release := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-release
		}
	}))
	defer target.Close()
	defer close(release)
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	runner := boundedRunner(t, issuer, runnerURL, config{targets: targetList(t, target.URL), resultsDir: dir})
	slow := strings.Replace(suiteOf(1), "path: /", "path: /slow", 1)
	body, _ := json.Marshal(api.RunRequest{Target: target.URL, Suites: map[string]string{"surface/api/fast.yaml": suiteOf(1), "surface/api/slow.yaml": slow}})
	var accepted struct{ ID string }
	json.Unmarshal(call(runner, http.MethodPost, "/v1/runs", token, bytes.NewReader(body)).Body.Bytes(), &accepted)

	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		run := readRunFile(t, dir, accepted.ID)
		if run.Suites[0].State == "passed" {
			if run.State != "running" || run.Suites[1].State != "running" {
				t.Fatalf("the file holds the run %s with the slow suite %s, want both running", run.State, run.Suites[1].State)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the file still holds the fast suite as %s 5s after the run started", run.Suites[0].State)
		}
	}
}
