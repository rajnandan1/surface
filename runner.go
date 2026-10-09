package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const maxRunRequest = 16 << 20

var cleanupWindow = 10 * time.Minute

type runRequest struct {
	Target string            `json:"target"`
	Suites map[string]string `json:"suites"`
}

type runResult struct {
	ID     string         `json:"id"`
	State  string         `json:"state"`
	Reason string         `json:"reason,omitempty"`
	Caller caller         `json:"caller"`
	Target string         `json:"target"`
	Suites []*suiteResult `json:"suites"`

	stopState, stopReason string
	cleanupDeadline       time.Time
	rate                  *rateLimit
}

type suiteResult struct {
	Path    string        `json:"path"`
	State   string        `json:"state"`
	Failure string        `json:"failure,omitempty"`
	Tests   []*testResult `json:"tests,omitempty"`
}

type testResult struct {
	Name    string         `json:"name"`
	State   string         `json:"state"`
	Failure string         `json:"failure,omitempty"`
	Cleanup *cleanupResult `json:"cleanup,omitempty"`
}

type cleanupResult struct {
	State   string `json:"state"`
	Failure string `json:"failure,omitempty"`
}

type runner struct {
	http.Handler
	log      *log.Logger
	config   config
	verifier *oidc.IDTokenVerifier
	mu       sync.Mutex
	runs     map[string]*runResult
	stopping bool
	active   sync.WaitGroup
	running  int
	pool     chan struct{}
	rates    map[target]*rateLimit
}

type rateLimit struct {
	mu    sync.Mutex
	every time.Duration
	next  time.Time
}

func newRunner(logTo io.Writer, c config) (*runner, error) {
	provider, err := oidc.NewProvider(oidc.ClientContext(context.Background(), &http.Client{Timeout: 30 * time.Second}), c.issuer)
	if err != nil {
		return nil, fmt.Errorf("github.issuer: %v", err)
	}
	r := &runner{
		log:    log.New(logTo, "", 0),
		config: c,
		verifier: provider.Verifier(&oidc.Config{
			// go-oidc's issuer and audience errors carry no type, so identify checks both to name them in the 401 line.
			SkipIssuerCheck:   true,
			SkipClientIDCheck: true,
			// go-oidc has no clock skew for exp, so its clock runs behind by the skew.
			Now: func() time.Time { return time.Now().Add(-clockSkew) },
		}),
		runs:  map[string]*runResult{},
		pool:  make(chan struct{}, cmp.Or(c.requests, 128)),
		rates: map[target]*rateLimit{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runs", r.authorized(r.start))
	mux.HandleFunc("GET /v1/runs/{id}", r.authorized(r.get))
	mux.HandleFunc("POST /v1/runs/{id}/cancel", r.authorized(r.cancel))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.stopping {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	r.Handler = mux
	return r, nil
}

func (r *runner) start(w http.ResponseWriter, req *http.Request, c caller) {
	var request runRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxRunRequest))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "run: request body is over 16 MiB", http.StatusRequestEntityTooLarge)
		} else if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			http.Error(w, fmt.Sprintf("run: unknown field %s (runner %s)", field, version), http.StatusBadRequest)
		} else {
			http.Error(w, "run: body is not a run request: "+strings.TrimPrefix(err.Error(), "json: "), http.StatusBadRequest)
		}
		return
	}
	if request.Target == "" {
		http.Error(w, "run: target is missing", http.StatusBadRequest)
		return
	}
	parsed, err := parseTarget(request.Target)
	if err == nil && strings.Contains(parsed.host, "*") {
		err = fmt.Errorf("target %q must not have a *", request.Target)
	}
	if err != nil {
		http.Error(w, "run: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !slices.ContainsFunc(r.config.targets, func(entry target) bool { return entry.matches(parsed) }) {
		http.Error(w, fmt.Sprintf("run: target %q is not in the target list", request.Target), http.StatusForbidden)
		return
	}

	runTime := cmp.Or(r.config.runTime, "30m")
	limit, _ := time.ParseDuration(runTime)
	run := &runResult{ID: newRunID(time.Now()), State: "running", Caller: c, Target: request.Target, cleanupDeadline: time.Now().Add(limit + cleanupWindow), rate: r.rateLimitFor(parsed)}
	for _, path := range slices.Sorted(maps.Keys(request.Suites)) {
		run.Suites = append(run.Suites, &suiteResult{Path: path, State: "pending"})
	}
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		w.Header().Set("Retry-After", "5")
		http.Error(w, "run: runner is stopping", http.StatusServiceUnavailable)
		return
	}
	if maxRuns := cmp.Or(r.config.runs, 32); r.running >= maxRuns {
		r.mu.Unlock()
		w.Header().Set("Retry-After", "5")
		http.Error(w, fmt.Sprintf("run: the runner holds as many runs as its cap of %d allows", maxRuns), http.StatusServiceUnavailable)
		return
	}
	r.runs[run.ID] = run
	r.running++
	r.active.Add(1)
	r.mu.Unlock()
	time.AfterFunc(limit, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if testsRunning(run) {
			r.stop(run, "error", "run time limit of "+runTime+" passed")
		}
	})
	r.log.Printf("run %s accepted repo %s ref %s target %s", run.ID, c.Repo, c.Ref, run.Target)
	go r.execute(run, request.Suites)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"id": run.ID})
}

func (r *runner) get(w http.ResponseWriter, req *http.Request, c caller) {
	r.mu.Lock()
	run := r.lookup(w, req.PathValue("id"), c, "read")
	body, _ := json.Marshal(run)
	r.mu.Unlock()
	if run != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

func (r *runner) cancel(w http.ResponseWriter, req *http.Request, c caller) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run := r.lookup(w, req.PathValue("id"), c, "cancel"); run != nil {
		r.stop(run, "cancelled", "")
		w.WriteHeader(http.StatusAccepted)
	}
}

// lookup returns the run when caller c may read or cancel it, and answers the request itself when not. The caller holds r.mu.
func (r *runner) lookup(w http.ResponseWriter, id string, c caller, action string) *runResult {
	run, ok := r.runs[id]
	if !ok {
		http.Error(w, fmt.Sprintf("run: run %q is not known", id), http.StatusNotFound)
		return nil
	}
	if run.Caller.Repo != c.Repo {
		http.Error(w, fmt.Sprintf("run: repo %q may not %s run %s", c.Repo, action, id), http.StatusForbidden)
		return nil
	}
	return run
}

// stop makes run start no new test and sets the state it ends in. The caller holds r.mu.
func (r *runner) stop(run *runResult, state, reason string) {
	if run.stopState != "" || isFinal(run.State) {
		return
	}
	run.stopState, run.stopReason = state, reason
}

func testsRunning(run *runResult) bool {
	for _, suite := range run.Suites {
		if suite.State == "pending" || slices.ContainsFunc(suite.Tests, func(t *testResult) bool { return t.State == "pending" || t.State == "running" }) {
			return true
		}
	}
	return false
}

func (r *runner) rateLimitFor(t target) *rateLimit {
	lowest := 0
	for _, entry := range r.config.targets {
		if entry.rate > 0 && entry.matches(t) && (lowest == 0 || entry.rate < lowest) {
			lowest = entry.rate
		}
	}
	if lowest == 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rates[t] == nil {
		r.rates[t] = &rateLimit{every: time.Second / time.Duration(lowest)}
	}
	return r.rates[t]
}

func (l *rateLimit) wait() {
	if l == nil {
		return
	}
	l.mu.Lock()
	turn := time.Now()
	if turn.Before(l.next) {
		turn = l.next
	}
	l.next = turn.Add(l.every)
	l.mu.Unlock()
	time.Sleep(time.Until(turn))
}

func (r *runner) shutdown() {
	r.mu.Lock()
	r.stopping = true
	for _, run := range r.runs {
		r.stop(run, "error", "runner stopped")
	}
	r.mu.Unlock()
	r.active.Wait()
}

func (r *runner) execute(run *runResult, texts map[string]string) {
	defer r.active.Done()
	var wg sync.WaitGroup
	for _, suite := range run.Suites {
		wg.Go(func() { r.runSuite(run, suite, texts[suite.Path]) })
	}
	wg.Wait()

	r.mu.Lock()
	r.running--
	run.State = "passed"
	for _, suite := range run.Suites {
		if suite.State == "failed" {
			run.State = "failed"
		}
	}
	if run.stopState != "" {
		run.State, run.Reason = run.stopState, run.stopReason
	}
	line := cmp.Or(run.Reason, countTests(run.Suites...).String())
	r.mu.Unlock()
	r.log.Printf("run %s %s: %s", run.ID, run.State, line)
}

var skipReason = map[string]string{"failed": "failed", "skipped": "was skipped"}

func (r *runner) runSuite(run *runResult, result *suiteResult, text string) {
	s, err := loadSuite(result.Path, text)
	r.mu.Lock()
	if err != nil {
		result.State, result.Failure = "failed", err.Error()
		r.mu.Unlock()
		return
	}
	result.State = "running"
	for _, t := range s.Tests {
		result.Tests = append(result.Tests, &testResult{Name: t.Name, State: "pending"})
	}
	entries := result.Tests
	r.mu.Unlock()

	index := map[string]int{}
	ended := make([]chan struct{}, len(s.Tests))
	saved := make([]map[string]json.RawMessage, len(s.Tests))
	for i, t := range s.Tests {
		index[t.Name], ended[i] = i, make(chan struct{})
	}
	runIDJSON, _ := json.Marshal(run.ID)
	vars := func(names ...string) map[string]json.RawMessage {
		vars := map[string]json.RawMessage{"run.id": runIDJSON}
		for _, name := range names {
			for key, value := range saved[index[name]] {
				vars["needs."+name+"."+key] = value
			}
		}
		return vars
	}
	var created []int
	var wg sync.WaitGroup
	for i, t := range s.Tests {
		wg.Go(func() {
			defer close(ended[i])
			for _, name := range t.Needs {
				<-ended[index[name]]
			}
			r.mu.Lock()
			for _, name := range t.Needs {
				if reason, ok := skipReason[entries[index[name]].State]; ok {
					entries[i].State, entries[i].Failure = "skipped", fmt.Sprintf("skipped: needs %s, which %s", name, reason)
					break
				}
			}
			start := entries[i].State == "pending" && run.stopState == ""
			if start {
				entries[i].State = "running"
			}
			r.mu.Unlock()
			if !start {
				return
			}
			var statusPassed bool
			saved[i], statusPassed = r.runTest(run, s.Defaults, t, vars(t.Needs...), entries[i])
			if statusPassed && t.Cleanup != nil {
				r.mu.Lock()
				created = append(created, i)
				r.mu.Unlock()
			}
		})
	}
	wg.Wait()

	for _, i := range slices.Backward(created) {
		t := s.Tests[i]
		request, err := newRequest(run.Target, s.Defaults, *t.Cleanup, vars(append(slices.Clone(t.Needs), t.Name)...))
		if errors.Is(err, errNoValue) {
			continue
		}
		cleanup := &cleanupResult{State: "passed"}
		switch {
		case time.Now().After(run.cleanupDeadline):
			cleanup.Failure = "not sent: run time limit passed"
		case err != nil:
			cleanup.Failure = "not sent: " + err.Error()
		default:
			release := r.turn(run)
			cleanup.Failure = "not sent: run time limit passed"
			if time.Now().Before(run.cleanupDeadline) {
				cleanup.Failure, _, _ = sendAndCheck(run.ID, request, s.Defaults, test{request: *t.Cleanup})
			}
			release()
		}
		if cleanup.Failure != "" {
			cleanup.State = "failed"
		}
		r.mu.Lock()
		entries[i].Cleanup = cleanup
		r.mu.Unlock()
	}

	r.mu.Lock()
	result.State = "failed"
	if c := countTests(result); c.passed == len(result.Tests) && c.cleanupFailed == 0 {
		result.State = "passed"
	}
	r.mu.Unlock()
}

func (r *runner) runTest(run *runResult, d defaults, t test, vars map[string]json.RawMessage, result *testResult) (map[string]json.RawMessage, bool) {
	failure, saved, statusPassed := "", map[string]json.RawMessage(nil), false
	if request, err := newRequest(run.Target, d, t.request, vars); err != nil {
		failure = "not sent: " + err.Error()
	} else {
		release := r.turn(run)
		defer release()
		r.mu.Lock()
		if run.stopState != "" {
			result.State = "pending"
			r.mu.Unlock()
			return nil, false
		}
		r.mu.Unlock()
		failure, saved, statusPassed = sendAndCheck(run.ID, request, d, t)
	}

	r.mu.Lock()
	result.State, result.Failure = "passed", failure
	if failure != "" {
		result.State = "failed"
	}
	r.mu.Unlock()
	return saved, statusPassed
}

func (r *runner) turn(run *runResult) (release func()) {
	run.rate.wait()
	// The Go runtime wakes blocked channel senders in the order they blocked, so pool slots go out in the order asked.
	r.pool <- struct{}{}
	return func() { <-r.pool }
}

func sendAndCheck(runID string, request *http.Request, d defaults, t test) (failure string, saved map[string]json.RawMessage, statusPassed bool) {
	response, body, elapsed, err := send(runID, request, cmp.Or(t.Timeout, d.Timeout, "30s"))
	if err != nil {
		return failureText([]failedCheck{{text: "no response: " + err.Error()}}, request, t.JSON.Kind != 0, nil, nil, 0), nil, false
	}
	checks, saved := checkResponse(t, response.StatusCode, response.Header, body, len(body) > maxResponseBody)
	statusPassed = response.StatusCode == t.Expect.Status
	if checks == nil {
		return "", saved, statusPassed
	}
	return failureText(checks, request, t.JSON.Kind != 0, response, body[:min(len(body), maxResponseBody)], elapsed), saved, statusPassed
}

type counts struct{ passed, failed, skipped, cleanupFailed int }

func countTests(suites ...*suiteResult) counts {
	var c counts
	for _, suite := range suites {
		for _, t := range suite.Tests {
			switch t.State {
			case "passed":
				c.passed++
			case "failed":
				c.failed++
			case "skipped":
				c.skipped++
			}
			if t.Cleanup != nil && t.Cleanup.State == "failed" {
				c.cleanupFailed++
			}
		}
	}
	return c
}

func (c counts) String() string {
	text := fmt.Sprintf("%d passed", c.passed)
	if c.failed > 0 {
		text += fmt.Sprintf(", %d failed", c.failed)
	}
	if c.skipped > 0 {
		text += fmt.Sprintf(", %d skipped", c.skipped)
	}
	if c.cleanupFailed > 0 {
		text += fmt.Sprintf(", %d cleanup failed", c.cleanupFailed)
	}
	return text
}
