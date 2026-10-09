package main

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const groupsConfig = `groups:
  production:
    callers:
      - repo: crustdata/api
        ref: refs/heads/main
      - repo: crustdata/*
        ref: refs/pull/*/merge
    targets:
      - <target>
  preview:
    secret: preview-api
    callers:
      - repo: crustdata/*
        ref: refs/pull/*/merge
    targets:
      - <target>
  partner:
    callers:
      - repo: crustdata/*
        ref: refs/pull/*/merge
    targets:
      - https://api.partner.example.com
`

// fakeKubernetes serves secrets the way the Kubernetes API does, over TLS with its own CA, and returns the Secret names it was asked for.
func fakeKubernetes(t *testing.T, secrets map[string]map[string]string) (reads func() []string) {
	t.Helper()
	var mu sync.Mutex
	var names []string
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer service-account-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		name, inNamespace := strings.CutPrefix(r.URL.Path, "/api/v1/namespaces/surface/secrets/")
		mu.Lock()
		names = append(names, name)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		data, found := secrets[name]
		if !inNamespace || !found {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"kind": "Status", "message": fmt.Sprintf("secrets %q not found", name)})
			return
		}
		encoded := map[string][]byte{}
		for key, value := range data {
			encoded[key] = []byte(value)
		}
		json.NewEncoder(w).Encode(map[string]any{"kind": "Secret", "data": encoded})
	}))
	t.Cleanup(api.Close)

	dir := t.TempDir()
	files := map[string][]byte{
		"token":     []byte("service-account-token\n"),
		"namespace": []byte("surface"),
		"ca.crt":    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw}),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	defaultDir := serviceAccountDir
	serviceAccountDir = dir
	t.Cleanup(func() { serviceAccountDir = defaultDir })
	host, port, _ := net.SplitHostPort(api.Listener.Addr().String())
	t.Setenv("KUBERNETES_SERVICE_HOST", host)
	t.Setenv("KUBERNETES_SERVICE_PORT", port)
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(names)
	}
}

// runWithGroups runs suites against target on a runner with groupsConfig, as the default caller with claims changed, and returns the run result once the run ends.
func runWithGroups(t *testing.T, target string, claims map[string]any, suites map[string]string) runResult {
	t.Helper()
	issuer := newFakeIssuer(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := fmt.Sprintf("url: %s\ntargets:\n  - %s\ngithub:\n  issuer: %s\n  owner_ids:\n    - %s\nresults:\n  dir: %s\n", runnerURL, target, issuer.url, crustdataOwnerID, t.TempDir())
	if err := os.WriteFile(path, []byte(config+strings.ReplaceAll(groupsConfig, "<target>", target)), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := newRunner(io.Discard, c)
	if err != nil {
		t.Fatal(err)
	}
	full := issuer.claims(runnerURL)
	maps.Copy(full, claims)
	token := issuer.sign(full)
	body, _ := json.Marshal(runRequest{Target: target, Suites: suites})
	accepted := call(runner, http.MethodPost, "/v1/runs", token, bytes.NewReader(body))
	var started struct{ ID string }
	if err := json.Unmarshal(accepted.Body.Bytes(), &started); err != nil {
		t.Fatalf("run request got %d %q", accepted.Code, accepted.Body.String())
	}
	runner.active.Wait()
	var result runResult
	if err := json.Unmarshal(call(runner, http.MethodGet, "/v1/runs/"+started.ID, token, nil).Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func usesSecret(name string) string {
	return header + "defaults:\n  headers:\n    Authorization: Bearer ${{ secrets." + name + " }}\n" + `tests:
  - name: list-users
    method: GET
    path: /users
    expect:
      status: 200
`
}

func TestASuiteWhoseSecretItCannotUseFailsAtLoadAndTheOtherSuitesStillRun(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	health := header + "tests:\n  - name: health\n    method: GET\n    path: /health\n    expect:\n      status: 200\n"
	cases := []struct {
		name, secret string
		claims       map[string]any
		line         string
		reads        []string
	}{
		{"the group is not in the config", "staging.api_token", nil, `secret group "staging" is not in the runner config`, nil},
		{"the Secret has no such key", "production.admin_token", nil, `secret group "production" has no key "admin_token"`, []string{"production"}},
		{"the value is empty", "production.empty", nil, `secret group "production" key "empty" is empty`, []string{"production"}},
		{"the caller is not bound", "production.api_token", map[string]any{"repository": "crustdata/web", "ref": "refs/heads/feature-x"}, `secret group "production" is not available to crustdata/web on refs/heads/feature-x`, nil},
		{"the target is not bound", "partner.api_token", nil, `secret group "partner" is not available to target ` + target.URL, nil},
		{"the Secret is not there", "preview.api_token", nil, `secret group "preview" was not read: secrets "preview-api" not found`, []string{"preview-api"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reads := fakeKubernetes(t, map[string]map[string]string{
				"production": {"api_token": "tok-9f8e7d6c", "empty": ""},
				"partner":    {"api_token": "partner-5a4b"},
			})

			result := runWithGroups(t, target.URL, c.claims, map[string]string{"api/health.yaml": health, "api/users.yaml": usesSecret(c.secret)})

			health, users := result.Suites[0], result.Suites[1]
			if want := "suite api/users.yaml: " + c.line; users.State != "failed" || users.Failure != want || users.Tests != nil {
				t.Errorf("api/users.yaml is %s with %q and %d tests, want failed at load with %q", users.State, users.Failure, len(users.Tests), want)
			}
			if health.State != "passed" {
				t.Errorf("api/health.yaml is %s %q, want passed", health.State, health.Failure)
			}
			if !slices.Equal(reads(), c.reads) {
				t.Errorf("the runner read the Secrets %q, want %q", reads(), c.reads)
			}
		})
	}
}

func TestATokenTheTargetEchoesIsRedactedInTheRequestAndTheResponseOfTheTestAndItsCleanup(t *testing.T) {
	fakeKubernetes(t, map[string]map[string]string{"production": {"api_token": "tok-9f8e7d6c"}})
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"authorization": r.Header.Get("Authorization")})
	}))
	defer target.Close()
	suite := header + `defaults:
  headers:
    Authorization: Bearer ${{ secrets.production.api_token }}
tests:
  - name: whoami
    method: GET
    path: /whoami
    expect:
      status: 200
      equal:
        body.authorization: nobody
    cleanup:
      method: DELETE
      path: /whoami
      expect:
        status: 204
`

	result := runWithGroups(t, target.URL, nil, map[string]string{"api/users.yaml": suite})

	test := result.Suites[0].Tests[0]
	if test.Cleanup == nil {
		t.Fatalf("whoami has no cleanup result; failure:\n%s", test.Failure)
	}
	for name, failure := range map[string]string{"test": test.Failure, "cleanup": test.Cleanup.Failure} {
		for _, want := range []string{"> Authorization: Bearer ${{ secrets.production.api_token }}", `<   "authorization": "Bearer ${{ secrets.production.api_token }}"`} {
			if !strings.Contains(failure, want) {
				t.Errorf("the %s failure text has no line %q:\n%s", name, want, failure)
			}
		}
		if strings.Contains(failure, "tok-9f8e7d6c") {
			t.Errorf("the %s failure text holds the token:\n%s", name, failure)
		}
	}
}

func TestAValueWithASlashIsRedactedInEachFormTheRunnerEscapesItTo(t *testing.T) {
	const value = `crust/data "api"`
	fakeKubernetes(t, map[string]map[string]string{"production": {"team": value}})
	var mu sync.Mutex
	var sentPath, sentQuery string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sentPath, sentQuery = r.URL.Path, r.URL.Query().Get("q")
		mu.Unlock()
	}))
	defer target.Close()
	suite := header + `tests:
  - name: find-team
    method: POST
    path: /teams/${{ secrets.production.team }}
    query:
      q: ${{ secrets.production.team }}
    json:
      team: ${{ secrets.production.team }}
    expect:
      status: 201
`

	result := runWithGroups(t, target.URL, nil, map[string]string{"api/teams.yaml": suite})

	mu.Lock()
	defer mu.Unlock()
	if sentPath != "/teams/"+value || sentQuery != value {
		t.Fatalf("the target got path %q and q %q, want the value in both", sentPath, sentQuery)
	}
	failure := result.Suites[0].Tests[0].Failure
	for _, want := range []string{"> POST " + target.URL + "/teams/${{ secrets.production.team }}?q=${{ secrets.production.team }}", `>   "team": "${{ secrets.production.team }}"`} {
		if !strings.Contains(failure, want) {
			t.Errorf("the failure text has no line %q:\n%s", want, failure)
		}
	}
	if strings.Contains(failure, "crust") {
		t.Errorf("the failure text holds the value:\n%s", failure)
	}
}

func TestAValueThatIsAPrefixOfAnotherValueLeavesBothRedactedWhole(t *testing.T) {
	fakeKubernetes(t, map[string]map[string]string{"production": {"short": "s3cr3t", "long": "s3cr3t-and-more"}})
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()
	suite := header + `tests:
  - name: both
    method: GET
    path: /users
    headers:
      X-Short: ${{ secrets.production.short }}
      X-Long: ${{ secrets.production.long }}
    expect:
      status: 201
`

	result := runWithGroups(t, target.URL, nil, map[string]string{"api/users.yaml": suite})

	failure := result.Suites[0].Tests[0].Failure
	for _, want := range []string{"> X-Long: ${{ secrets.production.long }}\n", "> X-Short: ${{ secrets.production.short }}\n"} {
		if !strings.Contains(failure, want) {
			t.Errorf("the failure text has no line %q:\n%s", want, failure)
		}
	}
}

func TestServeRefusesABadGroup(t *testing.T) {
	cases := []struct{ group, line string }{
		{"    callers: []\n    targets: [https://api.crustdata.com]\n", "line 7: groups.production.callers is missing or empty"},
		{"    targets: [https://api.crustdata.com]\n", "line 7: groups.production.callers is missing or empty"},
		{"    callers: [{repo: crustdata/api, ref: refs/heads/main}]\n    targets: []\n", "line 7: groups.production.targets is missing or empty"},
		{"    callers: [{repo: crustdata/api}]\n    targets: [https://api.crustdata.com]\n", "line 8: a caller must be a mapping with repo and ref"},
		{"    callers: [{repo: crustdata/api, ref: refs/heads/main, sha: 4f2a9c1e}]\n    targets: [https://api.crustdata.com]\n", `line 8: unknown key "sha"`},
		{"    callers: [{repo: crustdata/api, ref: refs/heads/main}]\n    targets: [https://api.crustdata.com/v1]\n", `line 9: target "https://api.crustdata.com/v1" must not have a path`},
		{"    callers: [{repo: crustdata/api, ref: refs/heads/main}]\n    targets: [https://api.crustdata.com]\n    secrets: production\n", `line 10: unknown key "groups.production.secrets"`},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "config.yaml")
		config := "url: https://surface.example.com\ntargets:\n  - https://api.crustdata.com\ngithub:\n  owner_ids: [63745551]\ngroups:\n  production:\n" + c.group
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		code := cli([]string{"serve", "--config", path}, &stdout, &stderr)

		if want := "surface serve: " + path + ": " + c.line + "\n"; code != 1 || stderr.String() != want {
			t.Errorf("%q: exit code %d, stderr %q, want 1 and %q", c.group, code, stderr.String(), want)
		}
	}
}

func TestServeRefusesAGroupNameThatIsNotASlug(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := "url: https://surface.example.com\ntargets:\n  - https://api.crustdata.com\ngithub:\n  owner_ids: [63745551]\ngroups:\n  Production:\n    callers: [{repo: crustdata/api, ref: refs/heads/main}]\n    targets: [https://api.crustdata.com]\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cli([]string{"serve", "--config", path}, &stdout, &stderr)

	if want := "surface serve: " + path + ": line 7: group name \"Production\" is not a slug of [a-z0-9-]\n"; code != 1 || stderr.String() != want {
		t.Fatalf("exit code %d, stderr %q, want 1 and %q", code, stderr.String(), want)
	}
}
