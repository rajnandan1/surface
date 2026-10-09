package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTheDataSafetyExampleSkipsUpdateUserWhenCreateUserGets500(t *testing.T) {
	log, code, sent := runDataSafetyExample(t, map[string]answer{"POST /orgs/41/users": {500, `{"error": "database is down"}`}})

	if code != 1 {
		t.Fatalf("exit code is %d, want 1\n%s", code, log)
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
	if want := []string{"POST /orgs", "POST /orgs/41/users", "DELETE /orgs/41"}; !slices.Equal(sent, want) {
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
		{"create-user", "failed", "status: expected 201, got 500", nil},
		{"update-user", "skipped", "skipped: needs create-user, which failed", nil},
		{"delete-user", "skipped", "skipped: needs update-user, which was skipped", nil},
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
