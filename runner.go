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

type runRequest struct {
	Target string            `json:"target"`
	Suites map[string]string `json:"suites"`
}

type runResult struct {
	ID     string         `json:"id"`
	State  string         `json:"state"`
	Caller caller         `json:"caller"`
	Target string         `json:"target"`
	Suites []*suiteResult `json:"suites"`
}

type suiteResult struct {
	Path    string        `json:"path"`
	State   string        `json:"state"`
	Failure string        `json:"failure,omitempty"`
	Tests   []*testResult `json:"tests,omitempty"`
}

type testResult struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Failure string `json:"failure,omitempty"`
}

type runner struct {
	log      *log.Logger
	config   config
	verifier *oidc.IDTokenVerifier
	mu       sync.Mutex
	runs     map[string]*runResult
}

func newRunner(logTo io.Writer, c config) (http.Handler, error) {
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
		runs: map[string]*runResult{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runs", r.authorized(r.start))
	mux.HandleFunc("GET /v1/runs/{id}", r.authorized(r.get))
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	return mux, nil
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

	run := &runResult{ID: newRunID(time.Now()), State: "running", Caller: c, Target: request.Target}
	for _, path := range slices.Sorted(maps.Keys(request.Suites)) {
		run.Suites = append(run.Suites, &suiteResult{Path: path, State: "pending"})
	}
	r.mu.Lock()
	r.runs[run.ID] = run
	r.mu.Unlock()
	r.log.Printf("run %s accepted repo %s ref %s target %s", run.ID, c.Repo, c.Ref, run.Target)
	go r.execute(run, request.Suites)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"id": run.ID})
}

func (r *runner) get(w http.ResponseWriter, req *http.Request, c caller) {
	id := req.PathValue("id")
	r.mu.Lock()
	run, ok := r.runs[id]
	body, _ := json.Marshal(run)
	r.mu.Unlock()
	if !ok {
		http.Error(w, fmt.Sprintf("run: run %q is not known", id), http.StatusNotFound)
		return
	}
	if run.Caller.Repo != c.Repo {
		http.Error(w, fmt.Sprintf("run: repo %q may not read run %s", c.Repo, id), http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func (r *runner) execute(run *runResult, texts map[string]string) {
	var wg sync.WaitGroup
	for _, suite := range run.Suites {
		wg.Go(func() { r.runSuite(run.ID, run.Target, suite, texts[suite.Path]) })
	}
	wg.Wait()

	r.mu.Lock()
	run.State = "passed"
	for _, suite := range run.Suites {
		if suite.State == "failed" {
			run.State = "failed"
		}
	}
	total := countTests(run.Suites...)
	r.mu.Unlock()
	r.log.Printf("run %s %s: %s", run.ID, run.State, total)
}

func (r *runner) runSuite(runID, target string, result *suiteResult, text string) {
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

	var wg sync.WaitGroup
	for i, t := range s.Tests {
		wg.Go(func() { r.runTest(runID, target, s.Defaults, t, entries[i]) })
	}
	wg.Wait()

	r.mu.Lock()
	result.State = "passed"
	if countTests(result).failed > 0 {
		result.State = "failed"
	}
	r.mu.Unlock()
}

func (r *runner) runTest(runID, target string, d defaults, t test, result *testResult) {
	r.mu.Lock()
	result.State = "running"
	r.mu.Unlock()

	runIDJSON, _ := json.Marshal(runID)
	vars := map[string]json.RawMessage{"run.id": runIDJSON}
	state, failure := "passed", ""
	if request, err := newRequest(target, d, t.request, vars); err != nil {
		state, failure = "failed", "not sent: "+err.Error()
	} else if response, body, err := send(runID, request, cmp.Or(t.Timeout, d.Timeout, "30s")); err != nil {
		state, failure = "failed", "no response: "+err.Error()
	} else if checks, _ := checkResponse(t, response.StatusCode, response.Header, body, len(body) > maxResponseBody); checks != nil {
		state, failure = "failed", lineOne(checks)
	}

	r.mu.Lock()
	result.State, result.Failure = state, failure
	r.mu.Unlock()
}

type counts struct{ passed, failed int }

func countTests(suites ...*suiteResult) counts {
	var c counts
	for _, suite := range suites {
		for _, t := range suite.Tests {
			switch t.State {
			case "passed":
				c.passed++
			case "failed":
				c.failed++
			}
		}
	}
	return c
}

func (c counts) String() string {
	if c.failed == 0 {
		return fmt.Sprintf("%d passed", c.passed)
	}
	return fmt.Sprintf("%d passed, %d failed", c.passed, c.failed)
}
