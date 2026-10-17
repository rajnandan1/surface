package client

import (
	"bytes"
	"strings"
	"testing"
)

func TestCheckPrintsEveryBrokenSuiteAndExits1(t *testing.T) {
	t.Chdir(t.TempDir())
	writeSuite(t, "surface/api/users.yaml", usersSuite)
	writeSuite(t, "surface/api/orgs.yaml", strings.Replace(usersSuite, "status: 200", "staus: 200", 1))
	writeSuite(t, "surface/search.yaml", strings.Replace(usersSuite, "kind: API", "kind: UI", 1))

	var stdout, stderr bytes.Buffer
	code := Check([]string{"surface/"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("exit code is %d, want 1\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
	want := `suite surface/api/orgs.yaml: line 8: unknown field "staus" (runner 1.0.1)
suite surface/search.yaml: line 2: unknown kind "UI" (runner 1.0.1)
`
	if stdout.String() != want {
		t.Fatalf("stdout is\n%s\nwant\n%s", &stdout, want)
	}
}

func TestCheckOnValidSuitesPrintsNothingAndExits0(t *testing.T) {
	t.Chdir(t.TempDir())
	writeSuite(t, "surface/api/users.yaml", usersSuite)
	writeSuite(t, "surface/search.yml", usersSuite)

	var stdout, stderr bytes.Buffer
	code := Check([]string{"surface/"}, &stdout, &stderr)

	if code != 0 || stdout.Len()+stderr.Len() != 0 {
		t.Fatalf("exit code is %d, want 0 and no output\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
}
