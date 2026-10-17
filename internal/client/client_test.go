package client

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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

const usersSuite = `apiVersion: surface/v1
kind: API
tests:
  - name: health
    method: GET
    path: /health
    expect:
      status: 200
  - name: list-users
    method: GET
    path: /users
    expect:
      status: 200
`

func writeSuite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithNoSuitesExits2(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("surface", 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--runner", "http://127.0.0.1:1", "--target", "http://127.0.0.1:1", "surface/"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code is %d, want 2", code)
	}
	if want := "surface run: no suites under surface/\n"; stderr.String() != want {
		t.Fatalf("stderr is %q, want %q", stderr.String(), want)
	}
}

func TestRunOutsideGitHubActionsExits2(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	t.Chdir(t.TempDir())
	writeSuite(t, "surface/api/users.yaml", usersSuite)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--runner", "http://127.0.0.1:1", "--target", "http://127.0.0.1:1", "surface/"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code is %d, want 2", code)
	}
	if want := "surface run: no OIDC token: not inside GitHub Actions, or the job is missing permissions: id-token: write\n"; stderr.String() != want {
		t.Fatalf("stderr is %q, want %q", stderr.String(), want)
	}
}
