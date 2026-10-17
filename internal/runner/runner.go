package runner

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
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/rajnandan1/surface"
	"github.com/rajnandan1/surface/internal/api"
	"github.com/rajnandan1/surface/internal/suite"
)

const maxRunRequest = 16 << 20

var cleanupWindow = 10 * time.Minute

type runResult struct {
	api.RunResult

	stopState, stopReason string
	cleanupDeadline       time.Time
	rate                  *rateLimit
	secrets               map[string]string
	redact                *strings.Replacer
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
	writing  sync.Mutex
	pool     chan struct{}
	rates    map[target]*rateLimit
}

type rateLimit struct {
	mu    sync.Mutex
	every time.Duration
	next  time.Time
}

func newRunner(logTo io.Writer, c config) (*runner, error) {
	c.resultsDir = cmp.Or(c.resultsDir, "/var/lib/surface/results")
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
	if err := os.MkdirAll(r.config.resultsDir, 0o700); err != nil {
		r.log.Printf("results: %v", err)
	}
	r.markInterrupted()
	r.sweep()
	return r, nil
}

func (r *runner) start(w http.ResponseWriter, req *http.Request, c api.Caller) {
	var request api.RunRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxRunRequest))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			http.Error(w, "run: request body is over 16 MiB", http.StatusRequestEntityTooLarge)
		} else if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			http.Error(w, fmt.Sprintf("run: unknown field %s (runner %s)", field, surface.Version), http.StatusBadRequest)
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
	run := &runResult{RunResult: api.RunResult{ID: newRunID(time.Now()), State: "running", Caller: c, Target: request.Target}, cleanupDeadline: time.Now().Add(limit + cleanupWindow), rate: r.rateLimitFor(parsed)}
	for _, path := range slices.Sorted(maps.Keys(request.Suites)) {
		run.Suites = append(run.Suites, &api.SuiteResult{Path: path, State: "pending"})
	}
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		w.Header().Set("Retry-After", "5")
		http.Error(w, "run: runner is stopping", http.StatusServiceUnavailable)
		return
	}
	if maxRuns := cmp.Or(r.config.runs, 32); len(r.runs) >= maxRuns {
		r.mu.Unlock()
		w.Header().Set("Retry-After", "5")
		http.Error(w, fmt.Sprintf("run: the runner holds as many runs as its cap of %d allows", maxRuns), http.StatusServiceUnavailable)
		return
	}
	r.runs[run.ID] = run
	r.active.Add(1)
	r.mu.Unlock()
	r.save(run)
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

func (r *runner) get(w http.ResponseWriter, req *http.Request, c api.Caller) {
	r.mu.Lock()
	run := r.lookup(w, req.PathValue("id"), c, "read")
	body, _ := json.Marshal(run)
	r.mu.Unlock()
	if run != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

func (r *runner) cancel(w http.ResponseWriter, req *http.Request, c api.Caller) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run := r.lookup(w, req.PathValue("id"), c, "cancel"); run != nil {
		r.stop(run, "cancelled", "")
		w.WriteHeader(http.StatusAccepted)
	}
}

// lookup returns the run when caller c may read or cancel it, and answers the request itself when not. The caller holds r.mu.
func (r *runner) lookup(w http.ResponseWriter, id string, c api.Caller, action string) *runResult {
	run, ok := r.runs[id]
	if !ok {
		run = r.load(id)
	}
	if run == nil {
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
	if run.stopState != "" || api.IsFinal(run.State) {
		return
	}
	run.stopState, run.stopReason = state, reason
}

func testsRunning(run *runResult) bool {
	for _, suite := range run.Suites {
		if suite.State == "pending" || slices.ContainsFunc(suite.Tests, func(t *api.TestResult) bool { return t.State == "pending" || t.State == "running" }) {
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
	suites := make([]suite.Suite, len(run.Suites))
	run.secrets = map[string]string{}
	read := map[string]error{}
	for i, result := range run.Suites {
		s, err := suite.Load(result.Path, texts[result.Path])
		if err == nil {
			err = r.readGroups(run, result.Path, s, read)
		}
		if err != nil {
			r.mu.Lock()
			result.State, result.Failure = "failed", err.Error()
			r.mu.Unlock()
		}
		suites[i] = s
	}
	run.redact = newRedactor(run.secrets)
	var wg sync.WaitGroup
	for i, result := range run.Suites {
		if result.State == "pending" {
			wg.Go(func() { r.runSuite(run, result, suites[i]) })
		}
	}
	wg.Wait()
	run.secrets, run.redact = nil, nil

	r.mu.Lock()
	run.State = "passed"
	for _, suite := range run.Suites {
		if suite.State == "failed" {
			run.State = "failed"
		}
	}
	if run.stopState != "" {
		run.State, run.Reason = run.stopState, run.stopReason
	}
	line := cmp.Or(run.Reason, api.CountTests(run.Suites...).String())
	r.mu.Unlock()
	r.log.Printf("run %s %s: %s", run.ID, run.State, line)
	if r.save(run) == nil {
		r.mu.Lock()
		delete(r.runs, run.ID)
		r.mu.Unlock()
	}
}

var skipReason = map[string]string{"failed": "failed", "skipped": "was skipped"}

func (r *runner) runSuite(run *runResult, result *api.SuiteResult, s suite.Suite) {
	defer r.save(run)
	r.mu.Lock()
	result.State = "running"
	for _, t := range s.Tests {
		result.Tests = append(result.Tests, &api.TestResult{Name: t.Name, State: "pending"})
	}
	entries := result.Tests
	r.mu.Unlock()

	index := map[string]int{}
	ended := make([]chan struct{}, len(s.Tests))
	saved := make([]map[string]json.RawMessage, len(s.Tests))
	for i, t := range s.Tests {
		index[t.Name], ended[i] = i, make(chan struct{})
	}
	known := map[string]json.RawMessage{}
	known["run.id"], _ = json.Marshal(run.ID)
	for name, value := range run.secrets {
		known[name], _ = json.Marshal(value)
	}
	vars := func(names ...string) map[string]json.RawMessage {
		vars := maps.Clone(known)
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
		request, err := suite.NewRequest(run.Target, s.Defaults, *t.Cleanup, vars(append(slices.Clone(t.Needs), t.Name)...))
		if errors.Is(err, suite.ErrNoValue) {
			continue
		}
		cleanup := &api.CleanupResult{State: "passed"}
		switch {
		case time.Now().After(run.cleanupDeadline):
			cleanup.Failure = "not sent: run time limit passed"
		case err != nil:
			cleanup.Failure = "not sent: " + err.Error()
		default:
			release := r.turn(run)
			cleanup.Failure = "not sent: run time limit passed"
			if time.Now().Before(run.cleanupDeadline) {
				cleanup.Failure, _, _ = suite.SendAndCheck(run.ID, request, s.Defaults, suite.Test{Request: *t.Cleanup})
			}
			release()
		}
		if cleanup.Failure != "" {
			cleanup.State, cleanup.Failure = "failed", run.redact.Replace(cleanup.Failure)
		}
		r.mu.Lock()
		entries[i].Cleanup = cleanup
		r.mu.Unlock()
	}

	r.mu.Lock()
	result.State = "failed"
	if c := api.CountTests(result); c.Passed == len(result.Tests) && c.CleanupFailed == 0 {
		result.State = "passed"
	}
	r.mu.Unlock()
}

func (r *runner) runTest(run *runResult, d suite.Defaults, t suite.Test, vars map[string]json.RawMessage, result *api.TestResult) (map[string]json.RawMessage, bool) {
	failure, saved, statusPassed := "", map[string]json.RawMessage(nil), false
	if request, err := suite.NewRequest(run.Target, d, t.Request, vars); err != nil {
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
		failure, saved, statusPassed = suite.SendAndCheck(run.ID, request, d, t)
	}

	r.mu.Lock()
	result.State, result.Failure = "passed", run.redact.Replace(failure)
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
