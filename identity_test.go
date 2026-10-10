package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
)

const (
	runnerURL        = "https://surface.example.com"
	crustdataOwnerID = "63745551"
	emptyRunRequest  = `{"target": "http://127.0.0.1:1", "suites": {}}`
)

type fakeIssuer struct {
	url string
	key *rsa.PrivateKey
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key := newKey(t)
	server := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{PublicKey: key.Public(), KeyID: "github", Algorithm: oidc.RS256}}}
	issuer := httptest.NewServer(server)
	t.Cleanup(issuer.Close)
	server.SetIssuer(issuer.URL)
	return &fakeIssuer{url: issuer.URL, key: key}
}

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func (f *fakeIssuer) claims(audience string) map[string]any {
	return map[string]any{
		"iss":                 f.url,
		"aud":                 audience,
		"exp":                 time.Now().Add(5 * time.Minute).Unix(),
		"repository":          "crustdata/api",
		"repository_owner_id": crustdataOwnerID,
		"ref":                 "refs/pull/123/merge",
		"sha":                 "4f2a9c1e",
		"run_id":              "11223344",
		"workflow_ref":        "crustdata/api/.github/workflows/e2e.yml@refs/pull/123/merge",
	}
}

func (f *fakeIssuer) sign(claims map[string]any) string {
	return signWith(f.key, claims)
}

func signWith(key *rsa.PrivateKey, claims map[string]any) string {
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	return oidctest.SignIDToken(key, "github", oidc.RS256, string(payload))
}

func with(claims map[string]any, name string, value any) map[string]any {
	claims[name] = value
	return claims
}

func newTestRunner(t *testing.T, issuer *fakeIssuer, logTo io.Writer, url string, targets ...string) http.Handler {
	t.Helper()
	runner, err := newRunner(logTo, config{url: url, targets: targetList(t, targets...), issuer: issuer.url, ownerIDs: []string{crustdataOwnerID}, resultsDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runner.active.Wait)
	return runner
}

// startRunner serves a runner whose url is its own address, the audience the client asks for.
func startRunner(t *testing.T, issuer *fakeIssuer, targets ...string) *httptest.Server {
	t.Helper()
	var runner http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { runner.ServeHTTP(w, r) }))
	t.Cleanup(server.Close)
	runner = newTestRunner(t, issuer, io.Discard, server.URL, targets...)
	return server
}

// fakeGitHubJob sets the variables GitHub gives a job with id-token: write, pointed at a token endpoint that answers with sign(audience), and a step summary file of its own.
func fakeGitHubJob(t *testing.T, sign func(audience string) string) {
	t.Helper()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "bearer job-request-token" {
			http.Error(w, "bad request token", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"count": 1, "value": sign(r.URL.Query().Get("audience"))})
	}))
	t.Cleanup(endpoint.Close)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", endpoint.URL+"/_apis/token?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "job-request-token")
	t.Setenv("GITHUB_STEP_SUMMARY", filepath.Join(t.TempDir(), "step_summary.md"))
}

func call(runner http.Handler, method, path, token string, body io.Reader) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	runner.ServeHTTP(response, request)
	return response
}

func TestTheRunnerChecksTheToken(t *testing.T) {
	issuer := newFakeIssuer(t)
	runner := newTestRunner(t, issuer, io.Discard, runnerURL, "http://127.0.0.1:1")
	expiredAt := time.Now().Add(-90 * time.Second).Truncate(time.Second)

	tests := []struct {
		name   string
		token  string
		status int
		line   string
	}{
		{"a valid token", issuer.sign(issuer.claims(runnerURL)), http.StatusAccepted, ""},
		{"no token", "", http.StatusUnauthorized, "run: token is missing"},
		{"a token expired 90 seconds ago", issuer.sign(with(issuer.claims(runnerURL), "exp", expiredAt.Unix())), http.StatusUnauthorized, "run: token expired at " + expiredAt.UTC().Format(time.RFC3339)},
		{"a token for another audience", issuer.sign(issuer.claims("https://other.example.com")), http.StatusUnauthorized, `run: token audience "https://other.example.com" is not this runner`},
		{"a token for the runner url with a trailing /", issuer.sign(issuer.claims(runnerURL + "/")), http.StatusUnauthorized, `run: token audience "https://surface.example.com/" is not this runner`},
		{"a token from another issuer", issuer.sign(with(issuer.claims(runnerURL), "iss", "https://token.example.com")), http.StatusUnauthorized, `run: token issuer "https://token.example.com" is not "` + issuer.url + `"`},
		{"a token signed by another key", signWith(newKey(t), issuer.claims(runnerURL)), http.StatusUnauthorized, "run: token signature is not valid"},
		{"a token expired 30 seconds ago, inside the clock skew", issuer.sign(with(issuer.claims(runnerURL), "exp", time.Now().Add(-30*time.Second).Unix())), http.StatusAccepted, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := call(runner, http.MethodPost, "/v1/runs", test.token, strings.NewReader(emptyRunRequest))
			if response.Code != test.status {
				t.Fatalf("status is %d, want %d\nbody: %s", response.Code, test.status, response.Body)
			}
			if test.line != "" && response.Body.String() != test.line+"\n" {
				t.Fatalf("body is %q, want %q", response.Body.String(), test.line+"\n")
			}
		})
	}
}

type readRecorder struct{ read bool }

func (b *readRecorder) Read([]byte) (int, error) {
	b.read = true
	return 0, io.EOF
}

func TestARefusedOwnerGets403AndTheRunnerNeverReadsTheBody(t *testing.T) {
	issuer := newFakeIssuer(t)
	var log bytes.Buffer
	runner := newTestRunner(t, issuer, &log, runnerURL, "http://127.0.0.1:1")
	stranger := with(with(issuer.claims(runnerURL), "repository", "someone/api"), "repository_owner_id", "98765")
	body := &readRecorder{}

	response := call(runner, http.MethodPost, "/v1/runs", issuer.sign(stranger), body)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status is %d, want 403\nbody: %s", response.Code, response.Body)
	}
	if want := `run: repo "someone/api" (owner id 98765) may not use this runner` + "\n"; response.Body.String() != want {
		t.Fatalf("body is %q, want %q", response.Body.String(), want)
	}
	if body.read {
		t.Fatal("the runner read the body of a refused owner")
	}
	if log.Len() != 0 {
		t.Fatalf("the runner logged a refusal: %q", log.String())
	}
}

func TestOnlyTheCallersRepoMayReadARun(t *testing.T) {
	issuer := newFakeIssuer(t)
	runner := newTestRunner(t, issuer, io.Discard, runnerURL, "http://127.0.0.1:1")
	accepted := call(runner, http.MethodPost, "/v1/runs", issuer.sign(issuer.claims(runnerURL)), strings.NewReader(emptyRunRequest))
	var run struct{ ID string }
	if err := json.Unmarshal(accepted.Body.Bytes(), &run); err != nil {
		t.Fatalf("status %d, body %q: %v", accepted.Code, accepted.Body, err)
	}

	otherRepo := with(issuer.claims(runnerURL), "repository", "crustdata/web")
	response := call(runner, http.MethodGet, "/v1/runs/"+run.ID, issuer.sign(otherRepo), nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status for another repo is %d, want 403\nbody: %s", response.Code, response.Body)
	}
	if want := `run: repo "crustdata/web" may not read run ` + run.ID + "\n"; response.Body.String() != want {
		t.Fatalf("body is %q, want %q", response.Body.String(), want)
	}

	mainBranch := with(issuer.claims(runnerURL), "ref", "refs/heads/main")
	response = call(runner, http.MethodGet, "/v1/runs/"+run.ID, issuer.sign(mainBranch), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status for another ref of the same repo is %d, want 200\nbody: %s", response.Code, response.Body)
	}
	var result runResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	want := caller{Repo: "crustdata/api", Ref: "refs/pull/123/merge", SHA: "4f2a9c1e", RunID: "11223344", WorkflowRef: "crustdata/api/.github/workflows/e2e.yml@refs/pull/123/merge"}
	if result.Caller != want {
		t.Fatalf("caller is %+v, want %+v", result.Caller, want)
	}
}
