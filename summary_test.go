package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

const createUserSuite = `apiVersion: surface/v1
kind: API
tests:
  - name: create-user
    method: POST
    path: /users
    expect:
      status: 201
    cleanup:
      method: DELETE
      path: /users/8812
      expect:
        status: 204
  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/8812
    expect:
      status: 200
  - name: read-user
    needs: [update-user]
    method: GET
    path: /users/8812
    expect:
      status: 200
`

const failedRun = `{"id": "` + fakeRunID + `", "state": "failed", "suites": [
  {"path": "surface/api/broken.yaml", "state": "failed", "failure": "suite surface/api/broken.yaml: line 8: unknown field \"staus\" (runner 1.0.1)"},
  {"path": "surface/api/search.yaml", "state": "passed", "tests": [{"name": "search-person", "state": "passed"}, {"name": "search-company", "state": "passed"}]},
  {"path": "surface/api/users.yaml", "state": "failed", "tests": [
    {"name": "create-user", "state": "passed", "cleanup": {"state": "failed", "failure": "status: expected 204, got 500\n> DELETE https://pr-123.preview.crustdata.com/users/8812"}},
    {"name": "update-user", "state": "failed", "failure": "body.discount: expected \"100%\", got \"50%\"\n> PATCH https://pr-123.preview.crustdata.com/users/8812"},
    {"name": "read-user", "state": "skipped", "failure": "skipped: needs update-user, which failed"}
  ]}
]}`

func runTheFailedRun(t *testing.T) (stdout, summary string) {
	t.Helper()
	runner := fakeRunner(t, reply(http.StatusOK, failedRun))
	code, stdout, stderr := runAgainst(t, runner.URL, "1m", createUserSuite)
	if code != 1 {
		t.Fatalf("exit code is %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	text, err := os.ReadFile(os.Getenv("GITHUB_STEP_SUMMARY"))
	if err != nil {
		t.Fatal(err)
	}
	return stdout, string(text)
}

func TestRunWritesTheJobSummary(t *testing.T) {
	_, summary := runTheFailedRun(t)

	want := "### surface run " + fakeRunID + ": failed\n" + `
| Suite | Passed | Failed | Skipped | Cleanup failed |
| --- | ---: | ---: | ---: | ---: |
| ` + "`surface/api/broken.yaml`" + ` (load failed) | 0 | 0 | 0 | 0 |
| ` + "`surface/api/search.yaml`" + ` | 2 | 0 | 0 | 0 |
| ` + "`surface/api/users.yaml`" + ` | 1 | 1 | 1 | 1 |
| Total | 3 | 1 | 1 | 1 |
`
	if summary != want {
		t.Fatalf("job summary is\n%s\nwant\n%s", summary, want)
	}
}

func TestRunAnnotatesEachFailureOnItsLine(t *testing.T) {
	stdout, _ := runTheFailedRun(t)

	var annotations []string
	for line := range strings.Lines(stdout) {
		if strings.HasPrefix(line, "::") {
			annotations = append(annotations, line)
		}
	}
	want := []string{
		`::error file=surface/api/broken.yaml,line=1::suite surface/api/broken.yaml: line 8: unknown field "staus" (runner 1.0.1)` + "\n",
		`::error file=surface/api/users.yaml,line=4::create-user cleanup: status: expected 204, got 500` + "\n",
		`::error file=surface/api/users.yaml,line=14::update-user: body.discount: expected "100%25", got "50%25"` + "\n",
	}
	if strings.Join(annotations, "") != strings.Join(want, "") {
		t.Fatalf("annotations are\n%s\nwant\n%s", strings.Join(annotations, ""), strings.Join(want, ""))
	}
}

func TestTheJobSummaryIsCutAt1MiB(t *testing.T) {
	result := runResult{ID: fakeRunID, State: "passed"}
	for i := range 30000 {
		result.Suites = append(result.Suites, &suiteResult{Path: fmt.Sprintf("surface/api/suite-%05d.yaml", i), State: "passed", Tests: []*testResult{{Name: "health", State: "passed"}}})
	}

	summary := jobSummary(result)

	if len(summary) > 1<<20 {
		t.Fatalf("the job summary has %d bytes, want at most %d", len(summary), 1<<20)
	}
	if want := "| 1 | 0 | 0 | 0 |\n\nThe job summary stops here, because GitHub takes at most 1 MiB per step. The CI log shows every suite.\n"; !strings.HasSuffix(summary, want) {
		t.Fatalf("the job summary ends with %q, want %q", summary[len(summary)-200:], want)
	}
}
