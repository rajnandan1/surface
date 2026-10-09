package main

import (
	"bytes"
	"os"
	"path/filepath"
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
