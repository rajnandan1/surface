package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		if err := os.WriteFile(path, []byte("url: https://surface.example.com\n"+c.targets+"github:\n  owner_ids:\n    - 63745551\n"), 0o600); err != nil {
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
	config := "url: https://surface.example.com\ntargets:\n  - https://*.preview.crustdata.com\n  - url: https://staging.crustdata.com\n    rate: 20\n" + "github:\n  owner_ids:\n    - 63745551\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []target{targetList(t, "https://*.preview.crustdata.com")[0], rated(t, "https://staging.crustdata.com:443", 20)}
	if !slices.Equal(c.targets, want) {
		t.Fatalf("targets are %v, want %v", c.targets, want)
	}
}

func TestServeRefusesABadOwnerList(t *testing.T) {
	tests := []struct {
		name   string
		github string
		error  string
	}{
		{"no github block", "", "github.owner_ids is missing or empty"},
		{"an empty list", "github:\n  owner_ids: []\n", "github.owner_ids is missing or empty"},
		{"an id of 0", "github:\n  owner_ids:\n    - 0\n", "line 6: an owner id must be a whole number of 1 or more"},
		{"an id written as a string", "github:\n  owner_ids:\n    - \"63745551\"\n", "line 6: an owner id must be a whole number of 1 or more"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			config := "url: https://surface.example.com\ntargets:\n  - https://api.example.com\n" + test.github
			if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			code := cli([]string{"serve", "--config", path}, &stdout, &stderr)

			if code == 0 {
				t.Fatalf("exit code is 0, want the runner to refuse to start")
			}
			if want := "surface serve: " + path + ": " + test.error + "\n"; stderr.String() != want {
				t.Fatalf("stderr is %q, want %q", stderr.String(), want)
			}
		})
	}
}

func TestConfigReadsTheRunTimeLimit(t *testing.T) {
	base := "url: https://surface.example.com\ntargets:\n  - https://api.example.com\ngithub:\n  owner_ids:\n    - 63745551\n"
	cases := []struct{ limits, runTime, error string }{
		{"", "", ""},
		{"limits:\n  run_time: 45m\n", "45m", ""},
		{"limits:\n  run_time: 0s\n", "", "line 8: limits.run_time must be a duration above zero, such as 30m"},
		{"limits:\n  run_time: -5m\n", "", "line 8: limits.run_time must be a duration above zero, such as 30m"},
		{"limits:\n  run_time: soon\n", "", "line 8: limits.run_time must be a duration above zero, such as 30m"},
		{"limits:\n  runtime: 30m\n", "", `line 8: unknown key "limits.runtime"`},
		{"limits: 30m\n", "", "line 7: limits must be a mapping"},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(base+c.limits), 0o600); err != nil {
			t.Fatal(err)
		}

		config, err := loadConfig(path)

		got := ""
		if err != nil {
			got = strings.TrimPrefix(err.Error(), path+": ")
		}
		if got != c.error || config.runTime != c.runTime && c.error == "" {
			t.Errorf("%q: run time %q, error %q, want %q and %q", c.limits, config.runTime, got, c.runTime, c.error)
		}
	}
}

func TestConfigReadsThePoolAndTheCapOnRuns(t *testing.T) {
	base := "url: https://surface.example.com\ntargets:\n  - https://api.example.com\ngithub:\n  owner_ids:\n    - 63745551\n"
	cases := []struct {
		limits         string
		requests, runs int
		error          string
	}{
		{"", 0, 0, ""},
		{"limits:\n  requests: 2\n  runs: 1\n", 2, 1, ""},
		{"limits:\n  requests: 0\n", 0, 0, "line 8: limits.requests must be a whole number of 1 or more"},
		{"limits:\n  requests: -4\n", 0, 0, "line 8: limits.requests must be a whole number of 1 or more"},
		{"limits:\n  runs: 2.5\n", 0, 0, "line 8: limits.runs must be a whole number of 1 or more"},
		{"limits:\n  runs: many\n", 0, 0, "line 8: limits.runs must be a whole number of 1 or more"},
		{"limits:\n  runs: \"4\"\n", 0, 0, "line 8: limits.runs must be a whole number of 1 or more"},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(base+c.limits), 0o600); err != nil {
			t.Fatal(err)
		}

		config, err := loadConfig(path)

		got := ""
		if err != nil {
			got = strings.TrimPrefix(err.Error(), path+": ")
		}
		if got != c.error || c.error == "" && (config.requests != c.requests || config.runs != c.runs) {
			t.Errorf("%q: requests %d, runs %d, error %q, want %d, %d, and %q", c.limits, config.requests, config.runs, got, c.requests, c.runs, c.error)
		}
	}
}

func TestConfigReadsTheResultsFolderAndRetention(t *testing.T) {
	base := "url: https://surface.example.com\ntargets:\n  - https://api.example.com\ngithub:\n  owner_ids:\n    - 63745551\n"
	cases := []struct{ results, dir, retention, error string }{
		{"", "", "", ""},
		{"results:\n  dir: /data/results\n  retention: 24h\n", "/data/results", "24h", ""},
		{"results:\n  dir: 5\n", "", "", "line 8: results.dir must be a path"},
		{"results:\n  dir: \"\"\n", "", "", "line 8: results.dir must be a path"},
		{"results:\n  retention: 0s\n", "", "", "line 8: results.retention must be a duration above zero, such as 168h"},
		{"results:\n  retention: 7d\n", "", "", "line 8: results.retention must be a duration above zero, such as 168h"},
		{"results:\n  keep: 7\n", "", "", `line 8: unknown key "results.keep"`},
		{"results: /data\n", "", "", "line 7: results must be a mapping"},
	}
	for _, c := range cases {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(base+c.results), 0o600); err != nil {
			t.Fatal(err)
		}

		config, err := loadConfig(path)

		got := ""
		if err != nil {
			got = strings.TrimPrefix(err.Error(), path+": ")
		}
		if got != c.error || c.error == "" && (config.resultsDir != c.dir || config.retention != c.retention) {
			t.Errorf("%q: dir %q, retention %q, error %q, want %q, %q, and %q", c.results, config.resultsDir, config.retention, got, c.dir, c.retention, c.error)
		}
	}
}
