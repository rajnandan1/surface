package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func stepScript(t *testing.T, file, name string) string {
	t.Helper()
	text, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(text, &doc); err != nil {
		t.Fatal(err)
	}
	var find func(n *yaml.Node) string
	find = func(n *yaml.Node) string {
		if n.Kind == yaml.MappingNode {
			var stepName, run string
			for i := 0; i < len(n.Content); i += 2 {
				switch n.Content[i].Value {
				case "name":
					stepName = n.Content[i+1].Value
				case "run":
					run = n.Content[i+1].Value
				}
			}
			if stepName == name && run != "" {
				return run
			}
		}
		for _, child := range n.Content {
			if run := find(child); run != "" {
				return run
			}
		}
		return ""
	}
	run := find(&doc)
	if run == "" {
		t.Fatalf("%s has no step %q with a run script", file, name)
	}
	return run
}

// runStep runs a step's script the way GitHub runs shell: bash.
func runStep(t *testing.T, script, dir string, env ...string) (string, int) {
	t.Helper()
	command := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", script)
	command.Dir = dir
	command.Env = append(os.Environ(), env...)
	out, err := command.CombinedOutput()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return string(out), exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func TestReleaseRefusesATagThatIsNotVPlusTheVERSIONFile(t *testing.T) {
	script := stepScript(t, ".github/workflows/release.yml", "Check the tag against VERSION")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runStep(t, script, dir, "GITHUB_REF_NAME=v1.0.1")
	if want := "release: tag v1.0.1 is not v1.0.0, the text of VERSION\n"; code == 0 || out != want {
		t.Errorf("tag v1.0.1: exit code %d, output %q, want a failure and %q", code, out, want)
	}
	if out, code := runStep(t, script, dir, "GITHUB_REF_NAME=v1.0.0"); code != 0 {
		t.Errorf("tag v1.0.0: exit code %d, output %q, want 0", code, out)
	}
}

func TestActionPicksTheBinaryForTheRunnerOSAndArchitecture(t *testing.T) {
	script := stepScript(t, "action.yml", "Pick the binary")
	actionPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(actionPath, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ os, arch, binary string }{
		{"Linux", "X64", "surface_1.0.0_linux_amd64"},
		{"Linux", "ARM64", "surface_1.0.0_linux_arm64"},
		{"macOS", "X64", "surface_1.0.0_darwin_amd64"},
		{"macOS", "ARM64", "surface_1.0.0_darwin_arm64"},
		{"Windows", "X64", "surface_1.0.0_windows_amd64.exe"},
		{"Windows", "ARM64", "surface_1.0.0_windows_arm64.exe"},
	}
	for _, c := range cases {
		output := filepath.Join(t.TempDir(), "output")
		out, code := runStep(t, script, t.TempDir(), "RUNNER_OS="+c.os, "RUNNER_ARCH="+c.arch, "GITHUB_ACTION_PATH="+actionPath, "GITHUB_OUTPUT="+output)
		written, _ := os.ReadFile(output)
		if want := "version=1.0.0\nbinary=" + c.binary + "\n"; code != 0 || string(written) != want {
			t.Errorf("%s/%s: exit code %d, output %q, step outputs %q, want 0 and %q", c.os, c.arch, code, out, written, want)
		}
	}

	out, code := runStep(t, script, t.TempDir(), "RUNNER_OS=Linux", "RUNNER_ARCH=ARM", "GITHUB_ACTION_PATH="+actionPath, "GITHUB_OUTPUT="+filepath.Join(t.TempDir(), "output"))
	if want := "surface: no binary for Linux/ARM\n"; code != 2 || out != want {
		t.Errorf("Linux/ARM: exit code %d, output %q, want 2 and %q", code, out, want)
	}
}

func TestActionRefusesABinaryWhoseChecksumDoesNotMatch(t *testing.T) {
	script := stepScript(t, "action.yml", "Check the checksum")
	binary := "surface_1.0.0_linux_amd64"
	for _, c := range []struct {
		name string
		sum  [32]byte
		code int
		out  string
	}{
		{"a matching checksum", sha256.Sum256([]byte("the binary")), 0, ""},
		{"a broken download", sha256.Sum256([]byte("another binary")), 2, "surface: the SHA-256 of " + binary + " does not match checksums.txt\n"},
	} {
		dir := t.TempDir()
		checksums := fmt.Sprintf("%x  surface_1.0.0_darwin_arm64\n%x  %s\n", sha256.Sum256([]byte("darwin")), c.sum, binary)
		if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(checksums), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, binary), []byte("the binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		out, code := runStep(t, script, dir, "BINARY="+binary)
		if code != c.code || !strings.HasSuffix(out, c.out) {
			t.Errorf("%s: exit code %d, output %q, want %d and %q", c.name, code, out, c.code, c.out)
		}
	}
}

func TestEveryStepScriptIsBashWithNoExpressionInIt(t *testing.T) {
	for _, file := range []string{"action.yml", ".github/workflows/release.yml"} {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(text, &doc); err != nil {
			t.Fatal(err)
		}
		var scripts []*yaml.Node
		var collect func(n *yaml.Node)
		collect = func(n *yaml.Node) {
			for i := 0; n.Kind == yaml.MappingNode && i < len(n.Content); i += 2 {
				if n.Content[i].Value == "run" {
					scripts = append(scripts, n.Content[i+1])
				}
			}
			for _, child := range n.Content {
				collect(child)
			}
		}
		collect(&doc)
		for _, script := range scripts {
			if strings.Contains(script.Value, "${{") {
				t.Errorf("%s: line %d: a run script holds a ${{ }} expression; pass the value through env", file, script.Line)
			}
			if out, err := exec.Command("bash", "-n", "-c", script.Value).CombinedOutput(); err != nil {
				t.Errorf("%s: line %d: the run script is not valid bash: %s", file, script.Line, out)
			}
		}
	}
}
