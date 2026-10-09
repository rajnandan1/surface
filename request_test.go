package main

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// runOneSuite starts a run of one suite against target and waits for its run result.
func runOneSuite(t *testing.T, target, suite string) runResult {
	t.Helper()
	issuer := newFakeIssuer(t)
	runner := newTestRunner(t, issuer, io.Discard, runnerURL, target)
	token := issuer.sign(issuer.claims(runnerURL))
	return waitForRun(t, runner, token, startRun(t, runner, token, target, suite))
}

func startRun(t *testing.T, runner http.Handler, token, target, suite string) string {
	t.Helper()
	request, _ := json.Marshal(runRequest{Target: target, Suites: map[string]string{"surface/api/users.yaml": suite}})
	accepted := call(runner, http.MethodPost, "/v1/runs", token, bytes.NewReader(request))
	var run struct{ ID string }
	if err := json.Unmarshal(accepted.Body.Bytes(), &run); err != nil {
		t.Fatalf("status %d, body %q: %v", accepted.Code, accepted.Body, err)
	}
	return run.ID
}

func waitForRun(t *testing.T, runner http.Handler, token, id string) runResult {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var result runResult
		if err := json.Unmarshal(call(runner, http.MethodGet, "/v1/runs/"+id, token, nil).Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if isFinal(result.State) {
			return result
		}
	}
	t.Fatalf("run %s did not end within 10s", id)
	return runResult{}
}

func oneTest(t *testing.T, result runResult) testResult {
	t.Helper()
	if len(result.Suites) != 1 || len(result.Suites[0].Tests) != 1 {
		t.Fatalf("run result is not one suite with one test: %+v", result.Suites)
	}
	return *result.Suites[0].Tests[0]
}

func TestATargetThatAnswers302GivesTheTestA302(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer target.Close()

	result := runOneSuite(t, target.URL, header+`tests:
  - name: old-users
    method: GET
    path: /old-users
    expect:
      status: 302
`)

	if got := oneTest(t, result); got.State != "passed" {
		t.Fatalf("test is %s: %s", got.State, got.Failure)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("the target counts %d requests, want 1", n)
	}
}

type sentRequest struct {
	host, uri string
	header    http.Header
	body      string
}

// recordingTarget answers 200 and records each request it gets.
func recordingTarget(t *testing.T) (*httptest.Server, chan sentRequest) {
	t.Helper()
	requests := make(chan sentRequest, 10)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- sentRequest{r.Host, r.RequestURI, r.Header, string(body)}
	}))
	t.Cleanup(target.Close)
	return target, requests
}

func TestAPathThatLooksLikeAHostStaysOnTheTarget(t *testing.T) {
	target, requests := recordingTarget(t)

	result := runOneSuite(t, target.URL, header+`tests:
  - name: evil
    method: GET
    path: //evil.com/x
    expect:
      status: 200
`)

	if test := oneTest(t, result); test.State != "passed" {
		t.Fatalf("test is %s: %s", test.State, test.Failure)
	}
	got := <-requests
	if got.host != target.Listener.Addr().String() || got.uri != "//evil.com/x" {
		t.Fatalf("the target got host %q and path %q, want %q and //evil.com/x", got.host, got.uri, target.Listener.Addr())
	}
}

func TestTheRunnerAddsItsTextToTheUserAgent(t *testing.T) {
	for _, c := range []struct{ name, headers, want string }{
		{"no suite User-Agent", "", "surface/1.0.0 (run <id>)"},
		{"a suite User-Agent", "    headers:\n      User-Agent: my-tests/2.0\n", "my-tests/2.0 surface/1.0.0 (run <id>)"},
		{"an empty suite User-Agent", "    headers:\n      User-Agent: \"\"\n", "surface/1.0.0 (run <id>)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			target, requests := recordingTarget(t)

			result := runOneSuite(t, target.URL, header+`tests:
  - name: health
    method: GET
    path: /health
`+c.headers+`    expect:
      status: 200
`)

			if test := oneTest(t, result); test.State != "passed" {
				t.Fatalf("test is %s: %s", test.State, test.Failure)
			}
			if got, want := (<-requests).header.Get("User-Agent"), strings.Replace(c.want, "<id>", result.ID, 1); got != want {
				t.Fatalf("User-Agent is %q, want %q", got, want)
			}
		})
	}
}

func TestTestHeadersMergeWithTheDefaultHeaders(t *testing.T) {
	target, requests := recordingTarget(t)

	result := runOneSuite(t, target.URL, header+`defaults:
  headers:
    Authorization: Bearer default-token
    Accept: application/json
    X-Trace: "1"
tests:
  - name: health
    method: GET
    path: /health
    headers:
      authorization: Bearer test-token
      X-Trace: null
      X-Run: ${{ run.id }}
    expect:
      status: 200
`)

	if test := oneTest(t, result); test.State != "passed" {
		t.Fatalf("test is %s: %s", test.State, test.Failure)
	}
	got := (<-requests).header
	if got.Get("Authorization") != "Bearer test-token" || got.Get("Accept") != "application/json" || got.Values("X-Trace") != nil || got.Get("X-Run") != result.ID {
		t.Fatalf("headers are %v, want Authorization: Bearer test-token, Accept: application/json, X-Run: %s, and no X-Trace", got, result.ID)
	}
}

func TestTheRunnerReadsAtMost10MiBOfAResponseBody(t *testing.T) {
	for _, c := range []struct {
		name    string
		size    int
		status  int
		failure string
	}{
		{"a body of 10 MiB", 10 << 20, 200, ""},
		{"a body of 10 MiB plus 1 byte", 10<<20 + 1, 200, "body: response is over 10 MiB"},
		{"a body over 10 MiB and the wrong status", 10<<20 + 1, 201, "status: expected 201, got 200 (+1 more)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(bytes.Repeat([]byte("a"), c.size))
			}))
			defer target.Close()

			result := runOneSuite(t, target.URL, header+`tests:
  - name: export
    method: GET
    path: /export
    expect:
      status: `+strconv.Itoa(c.status)+`
`)

			if got := oneTest(t, result); lineOneOf(got.Failure) != c.failure {
				t.Fatalf("test is %s with failure %q, want line one %q", got.State, got.Failure, c.failure)
			}
		})
	}
}

func TestASlowTargetGivesNoResponseAfterTheTimeout(t *testing.T) {
	for _, c := range []struct {
		name, defaults, timeout, failure string
		stopInTheBody                    bool
	}{
		{"the test's timeout", "defaults:\n  timeout: 1m\n", "    timeout: 100ms\n", "no response: timeout after 100ms", false},
		{"defaults.timeout", "defaults:\n  timeout: 200ms\n", "", "no response: timeout after 200ms", false},
		{"a target that stops in the body", "", "    timeout: 100ms\n", "no response: timeout after 100ms", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.stopInTheBody {
					w.Write([]byte("{"))
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}))
			defer target.Close()

			result := runOneSuite(t, target.URL, header+c.defaults+`tests:
  - name: slow
    method: GET
    path: /slow
`+c.timeout+`    expect:
      status: 200
`)

			if got := oneTest(t, result); lineOneOf(got.Failure) != c.failure {
				t.Fatalf("test is %s with failure %q, want line one %q", got.State, got.Failure, c.failure)
			}
		})
	}
}

func TestATargetThatRefusesTheConnectionGivesNoResponse(t *testing.T) {
	target := httptest.NewServer(http.NotFoundHandler())
	target.Close()

	result := runOneSuite(t, target.URL, header+`tests:
  - name: health
    method: GET
    path: /health
    expect:
      status: 200
`)

	want := "no response: connection refused\n> GET " + target.URL + "/health\n> User-Agent: surface/" + version + " (run " + result.ID + ")"
	if got := oneTest(t, result).Failure; got != want {
		t.Fatalf("failure is %q, want %q", got, want)
	}
}

func TestAJSONBodySendsTheRunIDAsAString(t *testing.T) {
	target, requests := recordingTarget(t)

	result := runOneSuite(t, target.URL, header+`tests:
  - name: create-user
    method: POST
    path: /users
    json: {
      "user_id": "${{ run.id }}",
      "note": "created by ${{ run.id }}",
      "age": 36,
      "admin": false,
      "tags": ["a", "b"],
      "manager": null
    }
    expect:
      status: 200
`)

	if test := oneTest(t, result); test.State != "passed" {
		t.Fatalf("test is %s: %s", test.State, test.Failure)
	}
	got := <-requests
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type is %q, want application/json", ct)
	}
	want := `{"admin":false,"age":36,"manager":null,"note":"created by ` + result.ID + `","tags":["a","b"],"user_id":"` + result.ID + `"}`
	var body any
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", got.body, err)
	}
	if sorted, _ := json.Marshal(body); string(sorted) != want {
		t.Fatalf("body is %s, want %s", got.body, want)
	}
}

const updateUserSuite = `apiVersion: surface/v1
kind: API
tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    save:
      id: body.id
      age: body.age
      user: body.user
      manager: body.manager
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/${{ needs.create-user.id }}
    json:
      id: ${{ needs.create-user.id }}
      age: ${{ needs.create-user.age }}
      user: ${{ needs.create-user.user }}
      note: age ${{ needs.create-user.age }}, manager ${{ needs.create-user.manager }}
    expect:
      status: 200
`

var createUserValues = map[string]json.RawMessage{
	"needs.create-user.id":      json.RawMessage(`"a/b"`),
	"needs.create-user.age":     json.RawMessage(`36`),
	"needs.create-user.user":    json.RawMessage(`{"name":"Ada"}`),
	"needs.create-user.manager": json.RawMessage(`null`),
}

func TestNewRequestPutsVariableValuesIntoTheRequest(t *testing.T) {
	s, err := loadSuite("surface/api/users.yaml", updateUserSuite)
	if err != nil {
		t.Fatal(err)
	}
	target, requests := recordingTarget(t)

	request, err := newRequest(target.URL, s.Defaults, s.Tests[1].request, createUserValues)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := send("01j9z3k4m5n6p7q8r9s0t1u2v3", request, "10s"); err != nil {
		t.Fatal(err)
	}

	got := <-requests
	if got.uri != "/users/a%2Fb" {
		t.Fatalf("the target got the path %q, want /users/a%%2Fb", got.uri)
	}
	if want := `{"id":"a/b","age":36,"user":{"name":"Ada"},"note":"age 36, manager null"}`; got.body != want {
		t.Fatalf("the target got the body %s, want %s", got.body, want)
	}
}

func TestAnObjectOrAnArrayInsideTextFailsTheTest(t *testing.T) {
	for _, c := range []struct{ value, want string }{
		{`{"name":"Ada"}`, "${{ needs.create-user.user }} is an object inside text"},
		{`["Ada"]`, "${{ needs.create-user.user }} is an array inside text"},
	} {
		s, err := loadSuite("surface/api/users.yaml", strings.Replace(updateUserSuite, "note: age ${{ needs.create-user.age }}", "note: by ${{ needs.create-user.user }}", 1))
		if err != nil {
			t.Fatal(err)
		}
		values := maps.Clone(createUserValues)
		values["needs.create-user.user"] = json.RawMessage(c.value)

		_, err = newRequest("http://127.0.0.1:1", s.Defaults, s.Tests[1].request, values)

		if err == nil || err.Error() != c.want {
			t.Errorf("%s inside text: error is %v, want %s", c.value, err, c.want)
		}
	}
}

func TestATestSendsItsQueryAndBody(t *testing.T) {
	for _, c := range []struct{ name, fields, uri, contentType, body string }{
		{"query", `    path: /search
    query:
      limit: 10
      active: true
      tag: [a, b]
      q: u-${{ run.id }}@example.com
`, "/search?active=true&limit=10&q=u-<id>%40example.com&tag=a&tag=b", "", ""},
		{"form", `    path: /users
    form:
      email: u-${{ run.id }}@example.com
      plan: free
      seats: 3
`, "/users", "application/x-www-form-urlencoded", "email=u-<id>%40example.com&plan=free&seats=3"},
		{"body", `    path: /notes
    body: "note by ${{ run.id }}"
`, "/notes", "", "note by <id>"},
		{"json with its own Content-Type", `    path: /users
    headers:
      Content-Type: application/vnd.api+json
    json: {"plan": "free"}
`, "/users", "application/vnd.api+json", `{"plan":"free"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			target, requests := recordingTarget(t)

			result := runOneSuite(t, target.URL, header+`tests:
  - name: send
    method: POST
`+c.fields+`    expect:
      status: 200
`)

			if test := oneTest(t, result); test.State != "passed" {
				t.Fatalf("test is %s: %s", test.State, test.Failure)
			}
			got := <-requests
			uri, body := strings.ReplaceAll(c.uri, "<id>", result.ID), strings.ReplaceAll(c.body, "<id>", result.ID)
			if got.uri != uri || got.header.Get("Content-Type") != c.contentType || got.body != body {
				t.Fatalf("the target got %s with Content-Type %q and body %q\nwant %s with Content-Type %q and body %q", got.uri, got.header.Get("Content-Type"), got.body, uri, c.contentType, body)
			}
		})
	}
}

func TestAMapInQueryFailsTheTest(t *testing.T) {
	target, _ := recordingTarget(t)

	result := runOneSuite(t, target.URL, header+`tests:
  - name: search
    method: GET
    path: /search
    query:
      filter: {plan: free}
    expect:
      status: 200
`)

	if got, want := oneTest(t, result).Failure, "not sent: line 8: query.filter is not text, a number, a bool, or a list of them"; got != want {
		t.Fatalf("failure is %q, want %q", got, want)
	}
}
