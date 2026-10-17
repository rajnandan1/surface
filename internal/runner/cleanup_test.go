package runner

import (
	"bytes"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rajnandan1/surface"
	"github.com/rajnandan1/surface/internal/api"
	"github.com/rajnandan1/surface/internal/client"
)

const (
	docRunID  = "01j9z3k4m5n6p7q8r9s0t1u2v3"
	docTarget = "https://pr-123.preview.crustdata.com"
)

func docExample(t *testing.T, doc, testLine string) string {
	t.Helper()
	text, err := os.ReadFile("../../docs/design/" + doc)
	if err != nil {
		t.Fatal(err)
	}
	block, found := failureUnder(string(text), testLine)
	if !found {
		t.Fatalf("%s has no example under %q", doc, testLine)
	}
	return block
}

func failureUnder(log, testLine string) (string, bool) {
	_, after, found := strings.Cut(log, "\n"+testLine+"\n")
	var lines []string
	for line := range strings.SplitSeq(after, "\n") {
		line, indented := strings.CutPrefix(line, "  ")
		if !indented {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), found
}

type answer struct {
	status int
	body   string
}

func runDataSafetyExample(t *testing.T, changed map[string]answer) (log string, code int, sent []string) {
	t.Helper()
	doc, err := os.ReadFile("../../docs/design/data-safety.md")
	if err != nil {
		t.Fatal(err)
	}
	_, example, _ := strings.Cut(string(doc), "```yaml\n")
	example, _, _ = strings.Cut(example, "```")
	answers := map[string]answer{
		"POST /orgs":                 {201, `{"id": 41}`},
		"POST /orgs/41/users":        {201, `{"data": {"id": 8812}}`},
		"PATCH /users/8812":          {200, `{}`},
		"DELETE /orgs/41/users/8812": {204, ``},
		"DELETE /orgs/41":            {204, ``},
	}
	maps.Copy(answers, changed)

	var mu sync.Mutex
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent = append(sent, r.Method+" "+r.URL.Path)
		mu.Unlock()
		a, ok := answers[r.Method+" "+r.URL.Path]
		if !ok {
			a.status = http.StatusNotFound
		}
		if a.body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(a.status)
		w.Write([]byte(a.body))
	}))
	defer target.Close()
	issuer := newFakeIssuer(t)
	fakeGitHubJob(t, func(audience string) string { return issuer.sign(issuer.claims(audience)) })
	runner := startRunner(t, issuer, target.URL)
	defer func(interval time.Duration) { client.PollInterval = interval }(client.PollInterval)
	client.PollInterval = 10 * time.Millisecond
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("surface/api", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("surface/api/users.yaml", []byte(header+example), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code = client.Run([]string{"--runner", runner.URL, "--target", target.URL, "--timeout", "1m", "surface/"}, &stdout, &stderr)

	log = regexp.MustCompile(`\b[0-9a-z]{26}\b`).ReplaceAllString(stdout.String()+stderr.String(), "<id>")
	mu.Lock()
	defer mu.Unlock()
	return strings.ReplaceAll(log, target.URL, "<target>"), code, slices.Clone(sent)
}

func TestWhenEveryTestPassesTheCleanupsDeleteTheUserBeforeTheOrg(t *testing.T) {
	log, code, sent := runDataSafetyExample(t, nil)

	if code != 0 || !strings.Contains(log, "\nsurface/api/users.yaml: 3 passed\n") {
		t.Fatalf("exit code is %d, want 0 and 3 passed\n%s", code, log)
	}
	want := []string{"POST /orgs", "POST /orgs/41/users", "PATCH /users/8812", "DELETE /orgs/41/users/8812", "DELETE /orgs/41"}
	if !slices.Equal(sent, want) {
		t.Fatalf("the target got %q, want %q", sent, want)
	}
}

func TestAFailedCleanupFailsTheRunAndPrintsItsFailureText(t *testing.T) {
	want := docExample(t, "data-safety.md", "surface/api/users.yaml create-org cleanup")
	log, code, _ := runDataSafetyExample(t, map[string]answer{"DELETE /orgs/41": {500, `{"error": "org has members"}`}})

	if code != 1 {
		t.Fatalf("exit code is %d, want 1\n%s", code, log)
	}
	for _, want := range []string{"\nsurface/api/users.yaml: 3 passed, 1 cleanup failed\n", "\nrun <id>: 3 passed, 1 cleanup failed\n"} {
		if !strings.Contains(log, want) {
			t.Errorf("CI log has no %q:\n%s", want, log)
		}
	}
	failure, found := failureUnder(log, "surface/api/users.yaml create-org cleanup")
	if !found {
		t.Fatalf("CI log has no failed cleanup of create-org:\n%s", log)
	}
	failure = regexp.MustCompile(`\n< (Content-Length|Date): [^\n]*`).ReplaceAllString(failure, "")
	failure = regexp.MustCompile(`in \d+ms +`).ReplaceAllString(failure, "in 12ms     ")
	failure = strings.NewReplacer("<target>", docTarget, "<id>", docRunID, "surface/"+surface.Version, "surface/1.2.0").Replace(failure)
	if failure != want {
		t.Fatalf("cleanup failure text is\n%s\nwant\n%s", failure, want)
	}
}

func TestACleanupRunsOnlyWhenItsTestsStatusCheckPassed(t *testing.T) {
	cases := []struct {
		name     string
		save     string
		answer   answer
		cleanup  *api.CleanupResult
		requests []string
	}{
		{"a 201 with a wrong body value", "", answer{201, `{"id": 1, "name": "b"}`}, &api.CleanupResult{State: "passed"}, []string{"POST /things", "DELETE /things/1"}},
		{"a 500", "", answer{500, `{"error": "database is down"}`}, nil, []string{"POST /things"}},
		{"a saved value the cleanup reads is not found", "    save: {thing_id: body.data.id}\n", answer{201, `{"id": 1, "name": "a"}`}, nil, []string{"POST /things"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			var sent []string
			deletes := make(chan http.Header, 1)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				sent = append(sent, r.Method+" "+r.URL.Path)
				mu.Unlock()
				if r.Method == http.MethodDelete {
					deletes <- r.Header
					w.WriteHeader(http.StatusNoContent)
					return
				}
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(c.answer.status)
				w.Write([]byte(c.answer.body))
			}))
			defer target.Close()
			path := "/things/1"
			if c.save != "" {
				path = "/things/${{ needs.create-thing.thing_id }}"
			}

			result := runOneSuite(t, target.URL, header+`defaults:
  headers: {X-Team: core}
tests:
  - name: create-thing
    method: POST
    path: /things
    expect:
      status: 201
      body: {"name": "a"}
`+c.save+`    cleanup:
      method: DELETE
      path: `+path+`
      expect:
        status: 204
`)

			got := oneTest(t, result)
			if got.State != "failed" || (got.Cleanup == nil) != (c.cleanup == nil) || got.Cleanup != nil && *got.Cleanup != *c.cleanup {
				t.Fatalf("test is %s with cleanup %+v, want failed with cleanup %+v", got.State, got.Cleanup, c.cleanup)
			}
			if result.Suites[0].State != "failed" {
				t.Fatalf("suite is %s, want failed", result.Suites[0].State)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(sent, c.requests) {
				t.Fatalf("the target got %q, want %q", sent, c.requests)
			}
			if c.cleanup != nil {
				sentHeader := <-deletes
				if sentHeader.Get("X-Team") != "core" || !strings.HasPrefix(sentHeader.Get("User-Agent"), "surface/") {
					t.Fatalf("the cleanup sent X-Team %q and User-Agent %q, want core and surface/...", sentHeader.Get("X-Team"), sentHeader.Get("User-Agent"))
				}
			}
		})
	}
}
