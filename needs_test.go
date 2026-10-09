package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTheDataSafetyExampleSkipsUpdateUserWhenCreateUserGets500(t *testing.T) {
	doc, err := os.ReadFile("docs/design/data-safety.md")
	if err != nil {
		t.Fatal(err)
	}
	_, example, _ := strings.Cut(string(doc), "```yaml\n")
	example, _, _ = strings.Cut(example, "```")

	var mu sync.Mutex
	var sent []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent = append(sent, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /orgs":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id": 41}`))
		case "POST /orgs/41/users":
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error": "database is down"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer target.Close()
	issuer := newFakeIssuer(t)
	fakeGitHubJob(t, func(audience string) string { return issuer.sign(issuer.claims(audience)) })
	runner := startRunner(t, issuer, target.URL)
	defer func(interval time.Duration) { pollInterval = interval }(pollInterval)
	pollInterval = 10 * time.Millisecond
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("surface/api", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("surface/api/users.yaml", []byte(header+example), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cli([]string{"run", "--runner", runner.URL, "--target", target.URL, "--timeout", "1m", "surface/"}, &stdout, &stderr)

	log := regexp.MustCompile(`\b[0-9a-z]{26}\b`).ReplaceAllString(stdout.String(), "<id>")
	log = strings.ReplaceAll(log, target.URL, "<target>")
	if code != 1 {
		t.Fatalf("exit code is %d, want 1\nstdout:\n%s\nstderr:\n%s", code, log, &stderr)
	}
	for _, want := range []string{
		"\nsurface/api/users.yaml: 1 passed, 1 failed, 1 skipped\n",
		"\nsurface/api/users.yaml create-user\n  status: expected 201, got 500 (+1 more)\n  > POST <target>/orgs/41/users\n",
		"\nsurface/api/users.yaml update-user\n  skipped: needs create-user, which failed\n",
		"\nrun <id>: 1 passed, 1 failed, 1 skipped\n",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("CI log has no %q:\n%s", want, log)
		}
	}
	if want := []string{"POST /orgs", "POST /orgs/41/users"}; !slices.Equal(sent, want) {
		t.Errorf("the target got %q, want %q", sent, want)
	}
}

func TestASavedNumberStaysANumberInJSONAndBecomesTextInPath(t *testing.T) {
	updates := make(chan sentRequest, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Write([]byte(`{"id": 8812}`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		updates <- sentRequest{uri: r.RequestURI, body: string(body)}
	}))
	defer target.Close()

	result := runOneSuite(t, target.URL, header+`tests:
  - name: get-me
    method: GET
    path: /users/me
    expect:
      status: 200
    save:
      id: body.id
  - name: update-me
    needs: [get-me]
    method: PATCH
    path: /users/${{ needs.get-me.id }}
    json: {"user_id": "${{ needs.get-me.id }}", "note": "user ${{ needs.get-me.id }}"}
    expect:
      status: 200
`)

	for _, test := range result.Suites[0].Tests {
		if test.State != "passed" {
			t.Fatalf("test %s is %s: %s", test.Name, test.State, test.Failure)
		}
	}
	got := <-updates
	if want := (sentRequest{uri: "/users/8812", body: `{"user_id":8812,"note":"user 8812"}`}); got.uri != want.uri || got.body != want.body {
		t.Fatalf("the target got %s with body %s, want %s with body %s", got.uri, got.body, want.uri, want.body)
	}
}

func TestAFailureSkipsTheWholeChainAfterIt(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent = append(sent, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer target.Close()

	result := runOneSuite(t, target.URL, header+`tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/1
    expect:
      status: 200
  - name: delete-user
    needs: [update-user]
    method: DELETE
    path: /users/1
    expect:
      status: 204
`)

	suite := result.Suites[0]
	var got []testResult
	for _, test := range suite.Tests {
		got = append(got, testResult{Name: test.Name, State: test.State, Failure: lineOneOf(test.Failure)})
	}
	want := []testResult{
		{"create-user", "failed", "status: expected 201, got 500"},
		{"update-user", "skipped", "skipped: needs create-user, which failed"},
		{"delete-user", "skipped", "skipped: needs update-user, which was skipped"},
	}
	if !slices.Equal(got, want) || suite.State != "failed" {
		t.Fatalf("suite is %s with tests %+v, want failed with %+v", suite.State, got, want)
	}
	if want := []string{"/users"}; !slices.Equal(sent, want) {
		t.Fatalf("the target got %q, want %q", sent, want)
	}
}

func TestTwoTestsWithoutNeedsRunAtTheSameTime(t *testing.T) {
	var inFlight sync.WaitGroup
	inFlight.Add(2)
	both := make(chan struct{})
	go func() { inFlight.Wait(); close(both) }()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Done()
		select {
		case <-both:
		case <-time.After(5 * time.Second):
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	}))
	defer target.Close()

	result := runOneSuite(t, target.URL, header+`tests:
  - name: list-users
    method: GET
    path: /users
    expect:
      status: 200
  - name: list-orgs
    method: GET
    path: /orgs
    expect:
      status: 200
`)

	for _, test := range result.Suites[0].Tests {
		if test.State != "passed" {
			t.Fatalf("test %s is %s: %s", test.Name, test.State, lineOneOf(test.Failure))
		}
	}
}
