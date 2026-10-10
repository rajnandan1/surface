package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const installDoc = "docs/install.md"

func yamlBlocks(t *testing.T, doc string) []string {
	t.Helper()
	text, err := os.ReadFile(doc)
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	for _, part := range strings.Split(string(text), "```yaml\n")[1:] {
		block, _, _ := strings.Cut(part, "```")
		blocks = append(blocks, block)
	}
	return blocks
}

func blockStarting(t *testing.T, blocks []string, prefix string) string {
	t.Helper()
	i := slices.IndexFunc(blocks, func(block string) bool { return strings.HasPrefix(block, prefix) })
	if i < 0 {
		t.Fatalf("%s has no yaml block that starts with %q", installDoc, prefix)
	}
	return blocks[i]
}

func TestTheInstallDocValuesRenderAndTheRunnerLoadsTheirConfig(t *testing.T) {
	blocks := yamlBlocks(t, installDoc)
	values := blockStarting(t, blocks, "config:")
	for _, route := range []struct{ kind, values string }{
		{"", ""},
		{"Ingress", blockStarting(t, blocks, "ingress:")},
		{"HTTPRoute", blockStarting(t, blocks, "httpRoute:")},
	} {
		objects := helmTemplate(t, values+route.values)
		if _, ok := objects[route.kind]; route.kind != "" && !ok {
			t.Fatalf("the chart rendered no %s from the values in %s", route.kind, installDoc)
		}
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(dig(t, objects["ConfigMap"], "data", "config.yaml").(string)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(path); err != nil {
			t.Fatalf("the runner does not load the config in %s: %v", installDoc, err)
		}
	}
}

func TestEachRelativeLinkInTheInstallDocResolves(t *testing.T) {
	text, err := os.ReadFile(installDoc)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`\]\(([^)]+)\)`).FindAllStringSubmatch(string(text), -1) {
		if strings.HasPrefix(m[1], "https://") {
			continue
		}
		file, anchor, _ := strings.Cut(m[1], "#")
		target, err := os.ReadFile(filepath.Join(filepath.Dir(installDoc), file))
		if err != nil {
			t.Errorf("link %s: %v", m[1], err)
			continue
		}
		if anchor != "" && !slices.Contains(headingAnchors(string(target)), anchor) {
			t.Errorf("link %s: %s has no heading %q", m[1], file, anchor)
		}
	}
}

// headingAnchors returns the anchors GitHub gives the headings of a Markdown file.
func headingAnchors(text string) []string {
	var anchors []string
	for _, m := range regexp.MustCompile(`(?m)^#+ (.+)$`).FindAllStringSubmatch(text, -1) {
		anchor := regexp.MustCompile(`[^\p{L}\p{N}_ -]`).ReplaceAllString(strings.ToLower(m[1]), "")
		anchors = append(anchors, strings.ReplaceAll(anchor, " ", "-"))
	}
	return anchors
}
