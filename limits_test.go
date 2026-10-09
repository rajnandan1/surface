package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
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

func boundedRunner(t *testing.T, issuer *fakeIssuer, url string, c config) *runner {
	t.Helper()
	c.url, c.issuer, c.ownerIDs = url, issuer.url, []string{crustdataOwnerID}
	runner, err := newRunner(io.Discard, c)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func waitForPass(t *testing.T, runner http.Handler, token, id string) {
	t.Helper()
	if result := waitForRun(t, runner, token, id); result.State != "passed" {
		t.Fatalf("run %s is %s, want passed: %+v", id, result.State, result.Suites[0])
	}
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
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	runner := boundedRunner(t, issuer, runnerURL, config{targets: targetList(t, target.URL), requests: 2})

	waitForPass(t, runner, token, startRun(t, runner, token, target.URL, suiteOf(10)))

	if most != 2 {
		t.Fatalf("the target saw at most %d requests in flight, want 2", most)
	}
}

func TestTheRunnerDefaultsToAPoolOf128(t *testing.T) {
	runner := boundedRunner(t, newFakeIssuer(t), runnerURL, config{targets: targetList(t, "https://api.crustdata.com")})

	if size := cap(runner.pool); size != 128 {
		t.Fatalf("the pool has %d slots, want 128", size)
	}
}

func TestARunAboveTheCapGets503WithRetryAfter5(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(time.Second) }))
	defer target.Close()
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	runner := boundedRunner(t, issuer, runnerURL, config{targets: targetList(t, target.URL), runs: 1})

	first := startRun(t, runner, token, target.URL, suiteOf(1))
	response := call(runner, http.MethodPost, "/v1/runs", token, strings.NewReader(fmt.Sprintf(`{"target": %q, "suites": {}}`, target.URL)))

	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "5" {
		t.Fatalf("the second run request gets %d with Retry-After %q, want 503 with 5", response.Code, response.Header().Get("Retry-After"))
	}
	if want := "run: the runner holds as many runs as its cap of 1 allows\n"; response.Body.String() != want {
		t.Fatalf("the 503 body is %q, want %q", response.Body, want)
	}
	waitForPass(t, runner, token, first)
}

func TestTheClientSendsTheRunRequestAgainAfterA503(t *testing.T) {
	release := make(chan struct{})
	open := sync.OnceFunc(func() { close(release) })
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer target.Close()
	defer open()
	issuer := newFakeIssuer(t)
	fakeGitHubJob(t, func(audience string) string { return issuer.sign(issuer.claims(audience)) })
	var runner *runner
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runner.ServeHTTP(w, r)
		if r.Method == http.MethodPost && posts.Add(1) == 2 {
			open()
		}
	}))
	defer server.Close()
	runner = boundedRunner(t, issuer, server.URL, config{targets: targetList(t, target.URL), runs: 1})
	defer func(interval time.Duration) { pollInterval = interval }(pollInterval)
	pollInterval = 10 * time.Millisecond

	body, _ := json.Marshal(runRequest{Target: target.URL, Suites: map[string]string{"surface/api/users.yaml": usersSuite}})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/runs", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+issuer.sign(issuer.claims(server.URL)))
	first, err := http.DefaultClient.Do(request)
	if err != nil || first.StatusCode != http.StatusAccepted {
		t.Fatalf("the first run request gets %v, %v, want 202", first, err)
	}
	inRepoWithASuite(t)

	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", server.URL, "--target", target.URL, "--timeout", "10s", "surface/"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code is %d, want 0\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
	if n := posts.Load(); n < 3 {
		t.Fatalf("the runner got %d run requests, want the first run's, the client's 503, and the client's next try", n)
	}
}

func TestTwoRunsOnOneTargetShareItsRate(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer api.Close()
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	runner := boundedRunner(t, issuer, runnerURL, config{targets: []target{rated(t, api.URL, 10)}})

	began := time.Now()
	first := startRun(t, runner, token, api.URL, suiteOf(10))
	second := startRun(t, runner, token, api.URL, suiteOf(10))
	waitForPass(t, runner, token, first)
	waitForPass(t, runner, token, second)

	if took := time.Since(began); took < 1900*time.Millisecond {
		t.Fatalf("20 requests at rate 10 took %v, want at least 1.9s", took)
	}
}

func TestEachTargetGetsOneRateAfterTheDefaultPortIsFilledIn(t *testing.T) {
	runner := boundedRunner(t, newFakeIssuer(t), runnerURL, config{targets: []target{
		rated(t, "https://*.preview.crustdata.com", 50),
		rated(t, "https://api.crustdata.com", 20),
		rated(t, "https://api.crustdata.com:443", 5),
		targetList(t, "https://staging.crustdata.com")[0],
	}})
	rateOf := func(text string) *rateLimit { return runner.rateLimitFor(targetList(t, text)[0]) }

	if rateOf("https://pr-1.preview.crustdata.com") == rateOf("https://pr-2.preview.crustdata.com") {
		t.Errorf("two hosts under one wildcard entry share one rate, want one each")
	}
	if every := rateOf("https://pr-1.preview.crustdata.com").every; every != time.Second/50 {
		t.Errorf("pr-1 gets one request every %v, want every %v", every, time.Second/50)
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
	seen := make(chan struct{}, 2)
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { seen <- struct{}{} }))
	defer slow.Close()
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer other.Close()
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	runner := boundedRunner(t, issuer, runnerURL, config{targets: []target{rated(t, slow.URL, 1), targetList(t, other.URL)[0]}, requests: 1})

	slowRun := startRun(t, runner, token, slow.URL, suiteOf(2))
	<-seen
	began := time.Now()
	waitForPass(t, runner, token, startRun(t, runner, token, other.URL, suiteOf(1)))

	if took := time.Since(began); took > 500*time.Millisecond {
		t.Fatalf("the run on the other target took %v, want under 500ms while the slow target's second request waits 1s for its rate", took)
	}
	waitForPass(t, runner, token, slowRun)
}

func TestACancelledRunSendsNoTestStillWaitingForItsTurn(t *testing.T) {
	seen := make(chan struct{}, 3)
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { seen <- struct{}{} }))
	defer api.Close()
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	runner := boundedRunner(t, issuer, runnerURL, config{targets: []target{rated(t, api.URL, 4)}})

	id := startRun(t, runner, token, api.URL, suiteOf(3))
	<-seen
	call(runner, http.MethodPost, "/v1/runs/"+id+"/cancel", token, nil)
	result := waitForRun(t, runner, token, id)

	pending := 0
	for _, test := range result.Suites[0].Tests {
		if test.State == "pending" {
			pending++
		}
	}
	if sent := 1 + len(seen); result.State != "cancelled" || sent != 1 || pending != 2 {
		t.Fatalf("run is %s, the target got %d requests, and %d tests are pending, want cancelled, 1, and 2", result.State, sent, pending)
	}
}

func TestACleanupWhoseTurnComesAfterTheWindowIsNotSent(t *testing.T) {
	defer func(window time.Duration) { cleanupWindow = window }(cleanupWindow)
	cleanupWindow = 50 * time.Millisecond
	api := newSlowTarget(t, 0, 0)
	issuer := newFakeIssuer(t)
	token := issuer.sign(issuer.claims(runnerURL))
	runner := boundedRunner(t, issuer, runnerURL, config{targets: []target{rated(t, api.URL, 1)}, runTime: "50ms"})

	result := waitForRun(t, runner, token, startRun(t, runner, token, api.URL, header+createThingSuite))

	if cleanup := result.Suites[0].Tests[0].Cleanup; cleanup == nil || *cleanup != (cleanupResult{"failed", "not sent: run time limit passed"}) {
		t.Fatalf("the cleanup is %+v, want failed with not sent: run time limit passed", cleanup)
	}
	if want := []string{"POST /things"}; !slices.Equal(api.requests(), want) {
		t.Fatalf("the target got %q, want %q", api.requests(), want)
	}
}
