package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func rated(t *testing.T, url string, rate int) target {
	t.Helper()
	entry := targetList(t, url)[0]
	entry.rate = rate
	return entry
}

func suiteOf(n int) string {
	var b strings.Builder
	b.WriteString("apiVersion: surface/v1\nkind: API\ntests:\n")
	for i := range n {
		fmt.Fprintf(&b, "  - name: test-%d\n    method: GET\n    path: /\n    expect:\n      status: 200\n", i)
	}
	return b.String()
}

func startRunOn(t *testing.T, runner, target, suite string) string {
	t.Helper()
	id, err := startRun(t.Context(), runner, runRequest{Target: target, Suites: map[string]string{"surface/api/load.yaml": suite}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func waitForPass(t *testing.T, runner, id string) {
	t.Helper()
	result, err := waitForRun(t.Context(), runner, id)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "passed" {
		t.Fatalf("run %s is %s, want passed: %+v", id, result.State, result.Suites[0])
	}
}

func fastPolls(t *testing.T) {
	interval := pollInterval
	pollInterval = 10 * time.Millisecond
	t.Cleanup(func() { pollInterval = interval })
}

func TestThePoolCapsTheRequestsInFlight(t *testing.T) {
	var mu sync.Mutex
	inFlight, most := 0, 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		inFlight++
		most = max(most, inFlight)
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
	}))
	defer target.Close()
	runner := httptest.NewServer(newRunner(io.Discard, targetList(t, target.URL), limits{requests: 2, runs: 32}))
	defer runner.Close()
	fastPolls(t)

	waitForPass(t, runner.URL, startRunOn(t, runner.URL, target.URL, suiteOf(10)))

	if most != 2 {
		t.Fatalf("the target saw at most %d requests in flight, want 2", most)
	}
}

func TestARunAboveTheCapGets503AndTheClientTriesAgain(t *testing.T) {
	release := make(chan struct{})
	open := sync.OnceFunc(func() { close(release) })
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer target.Close()
	defer open()
	handler := newRunner(io.Discard, targetList(t, target.URL), limits{requests: 128, runs: 1})
	var posts atomic.Int64
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		if r.Method == http.MethodPost && posts.Add(1) == 3 {
			open()
		}
	}))
	defer runner.Close()
	fastPolls(t)

	startRunOn(t, runner.URL, target.URL, usersSuite)
	body, err := json.Marshal(runRequest{Target: target.URL, Suites: map[string]string{"surface/api/users.yaml": usersSuite}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(runner.URL+"/v1/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	line, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Retry-After") != "5" {
		t.Fatalf("the second run request gets %d with Retry-After %q, want 503 with 5", response.StatusCode, response.Header.Get("Retry-After"))
	}
	if want := "run: the runner holds as many runs as its cap of 1 allows\n"; string(line) != want {
		t.Fatalf("the 503 body is %q, want %q", line, want)
	}

	t.Chdir(t.TempDir())
	writeSuite(t, "surface/api/users.yaml", usersSuite)
	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", runner.URL, "--target", target.URL, "--timeout", "10s", "surface/"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code is %d, want 0\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
	if n := posts.Load(); n < 4 {
		t.Fatalf("the runner got %d run requests, want the client's 503 and its next try on top of the first 2", n)
	}
}

func TestTwoRunsOnOneTargetShareItsRate(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer api.Close()
	runner := httptest.NewServer(newRunner(io.Discard, []target{rated(t, api.URL, 10)}, defaultLimits))
	defer runner.Close()
	fastPolls(t)

	began := time.Now()
	first := startRunOn(t, runner.URL, api.URL, suiteOf(10))
	second := startRunOn(t, runner.URL, api.URL, suiteOf(10))
	waitForPass(t, runner.URL, first)
	waitForPass(t, runner.URL, second)

	if took := time.Since(began); took < 1900*time.Millisecond {
		t.Fatalf("20 requests at rate 10 took %v, want at least 1.9s", took)
	}
}

func TestEachTargetGetsOneRateAfterTheDefaultPortIsFilledIn(t *testing.T) {
	r := &runner{
		targets: []target{
			rated(t, "https://*.preview.crustdata.com", 50),
			rated(t, "https://api.crustdata.com", 20),
			rated(t, "https://api.crustdata.com:443", 5),
			targetList(t, "https://staging.crustdata.com")[0],
		},
		rates: map[target]*rateLimit{},
	}
	rateOf := func(text string) *rateLimit { return r.rateLimitFor(targetList(t, text)[0]) }

	if rateOf("https://pr-1.preview.crustdata.com") == rateOf("https://pr-2.preview.crustdata.com") {
		t.Errorf("two hosts under one wildcard entry share one rate, want one each")
	}
	if rateOf("https://pr-1.preview.crustdata.com").every != time.Second/50 {
		t.Errorf("pr-1 gets one request every %v, want every %v", rateOf("https://pr-1.preview.crustdata.com").every, time.Second/50)
	}
	if rateOf("https://api.crustdata.com") != rateOf("https://API.crustdata.com:443") {
		t.Errorf("https://api.crustdata.com and https://API.crustdata.com:443 get two rates, want one")
	}
	if every := rateOf("https://api.crustdata.com").every; every != time.Second/5 {
		t.Errorf("api gets one request every %v, want every %v from the lowest rate", every, time.Second/5)
	}
	if rateOf("https://staging.crustdata.com") != nil {
		t.Errorf("a target of a string entry gets a rate, want none")
	}
}

func TestARequestWaitingForItsRateHoldsNoPoolSlot(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer slow.Close()
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer other.Close()
	runner := httptest.NewServer(newRunner(io.Discard, []target{rated(t, slow.URL, 1), targetList(t, other.URL)[0]}, limits{requests: 1, runs: 32}))
	defer runner.Close()
	fastPolls(t)

	slowRun := startRunOn(t, runner.URL, slow.URL, suiteOf(2))
	began := time.Now()
	waitForPass(t, runner.URL, startRunOn(t, runner.URL, other.URL, suiteOf(1)))

	if took := time.Since(began); took > 500*time.Millisecond {
		t.Fatalf("the run on the other target took %v, want under 500ms while the slow target's second request waits 1s for its rate", took)
	}
	waitForPass(t, runner.URL, slowRun)
}
