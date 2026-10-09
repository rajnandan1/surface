package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestServeRefusesAnUnknownConfigKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := "url: https://surface.example.com\ntargets:\n  - https://api.example.com\ntarget_list: []\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cli([]string{"serve", "--config", path}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code is 0, want the runner to refuse to start")
	}
	want := "surface serve: " + path + ": line 4: unknown key \"target_list\"\n"
	if stderr.String() != want {
		t.Fatalf("stderr is %q, want %q", stderr.String(), want)
	}
}

func TestServeRefusesABadTargetList(t *testing.T) {
	cases := []struct{ targets, line string }{
		{"targets: []\n", "targets is missing or empty"},
		{"", "targets is missing or empty"},
		{"targets:\n  - https://api.crustdata.com/v1\n", `line 3: target "https://api.crustdata.com/v1" must not have a path`},
		{"targets:\n  - 8080\n", "line 3: a target must be a string, or a mapping with url and rate"},
		{"targets:\n  - url: https://api.crustdata.com\n    rate: 0\n", "line 4: rate must be a whole number of 1 or more"},
		{"targets:\n  - url: https://api.crustdata.com\n    rate: 2.5\n", "line 4: rate must be a whole number of 1 or more"},
		{"targets:\n  - url: https://api.crustdata.com\n    rate: fast\n", "line 4: rate must be a whole number of 1 or more"},
		{"targets:\n  - rate: 20\n", "line 3: a target entry has no url"},
		{"targets:\n  - url: https://api.crustdata.com\n    callers: [crustdata/api]\n", `line 4: unknown key "callers"`},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte("url: https://surface.example.com\n"+c.targets), 0o600); err != nil {
			t.Fatal(err)
		}

		var stdout, stderr bytes.Buffer
		code := cli([]string{"serve", "--config", path}, &stdout, &stderr)

		want := "surface serve: " + path + ": " + c.line + "\n"
		if code != 1 || stderr.String() != want {
			t.Errorf("%q: exit code %d, stderr %q, want 1 and %q", c.targets, code, stderr.String(), want)
		}
	}
}

func TestConfigReadsTargetStringsAndMappings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	config := "url: https://surface.example.com\ntargets:\n  - https://*.preview.crustdata.com\n  - url: https://staging.crustdata.com\n    rate: 20\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := targetList(t, "https://*.preview.crustdata.com", "https://staging.crustdata.com:443"); !slices.Equal(c.targets, want) {
		t.Fatalf("targets are %v, want %v", c.targets, want)
	}
}
