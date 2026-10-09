package main

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type suite struct {
	APIVersion yaml.Node `yaml:"apiVersion"`
	Kind       yaml.Node `yaml:"kind"`
	Defaults   defaults  `yaml:"defaults"`
	Tests      []test    `yaml:"tests"`

	secrets []string
}

type defaults struct {
	Headers map[string]*string `yaml:"headers"`
	Timeout string             `yaml:"timeout"`
}

type request struct {
	Method  string               `yaml:"method"`
	Path    string               `yaml:"path"`
	Query   map[string]yaml.Node `yaml:"query"`
	Headers map[string]*string   `yaml:"headers"`
	JSON    yaml.Node            `yaml:"json"`
	Form    map[string]yaml.Node `yaml:"form"`
	Body    *string              `yaml:"body"`
	Timeout string               `yaml:"timeout"`
	Expect  expect               `yaml:"expect"`
}

type test struct {
	request `yaml:",inline"`
	Name    string      `yaml:"name"`
	Needs   []string    `yaml:"needs"`
	Save    savedValues `yaml:"save"`
	Cleanup *request    `yaml:"cleanup"`

	line int
}

type expect struct {
	Status  int               `yaml:"status"`
	Headers map[string]string `yaml:"headers"`
	Match   string            `yaml:"match"`
	Body    yaml.Node         `yaml:"body"`
	Equal   equals            `yaml:"equal"`
}

type savedValues []savedValue

type savedValue struct {
	name string
	path path
}

type equals []equal

type equal struct {
	path  path
	value yaml.Node
}

// path points at one value in a response: the start, "body" or "headers", then string keys and int indexes.
type path struct {
	text  string
	steps []any
}

const maxTimeout = 5 * time.Minute

var (
	slug           = regexp.MustCompile(`^[a-z0-9-]+$`)
	secretVariable = regexp.MustCompile(`^secrets\.[a-z0-9-]+\.[A-Za-z0-9_-]+$`)
	needsVariable  = regexp.MustCompile(`^needs\.([a-z0-9-]+)\.([A-Za-z0-9_-]+)$`)
	unknownField   = regexp.MustCompile(`^line (\d+): field (.+) not found in type .+$`)
)

func loadSuite(path, text string) (suite, error) {
	fail := func(format string, args ...any) (suite, error) {
		return suite{}, fmt.Errorf("suite %s: %s", path, fmt.Sprintf(format, args...))
	}

	nodes := yaml.NewDecoder(strings.NewReader(text))
	var root, extra yaml.Node
	if err := nodes.Decode(&root); err != nil && err != io.EOF {
		return fail("%s", yamlLine(err))
	}
	switch err := nodes.Decode(&extra); {
	case err == nil:
		return fail("line %d: a suite holds one YAML document only", extra.Line)
	case err != io.EOF:
		return fail("%s", yamlLine(err))
	}
	if n := findAnchor(&root); n != nil {
		return fail("line %d: YAML anchors and aliases are not allowed in a suite", n.Line)
	}

	var s suite
	decoder := yaml.NewDecoder(strings.NewReader(text))
	decoder.KnownFields(true)
	if err := decoder.Decode(&s); err != nil && err != io.EOF {
		return fail("%s", yamlLine(err))
	}
	for _, field := range []struct {
		name, want string
		node       yaml.Node
	}{{"apiVersion", "surface/v1", s.APIVersion}, {"kind", "API", s.Kind}} {
		if field.node.Kind == 0 {
			return fail("%s is missing", field.name)
		}
		if field.node.Value != field.want {
			return fail("line %d: unknown %s %q (runner %s)", field.node.Line, field.name, field.node.Value, version)
		}
	}
	if len(s.Tests) == 0 {
		return fail("tests is missing or empty")
	}
	doc := root.Content[0]
	for i := 0; i < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "tests" {
			for j, item := range doc.Content[i+1].Content {
				s.Tests[j].line = item.Line
			}
		}
	}
	if err := checkTimeout(s.Defaults.Timeout); err != nil {
		return fail("defaults: %v", err)
	}
	names := map[string]bool{}
	for i, t := range s.Tests {
		switch {
		case t.Name == "":
			return fail("test %d: name is missing", i+1)
		case !slug.MatchString(t.Name):
			return fail("test %d: name %q is not a slug of [a-z0-9-]", i+1, t.Name)
		case names[t.Name]:
			return fail("test %d: name %q is not unique in the suite", i+1, t.Name)
		}
		names[t.Name] = true
		if err := checkRequest(t.request); err != nil {
			return fail("%s: %v", t.Name, err)
		}
		if t.Cleanup != nil {
			if err := checkRequest(*t.Cleanup); err != nil {
				return fail("%s: cleanup: %v", t.Name, err)
			}
		}
	}
	for _, t := range s.Tests {
		for _, name := range t.Needs {
			if !names[name] {
				return fail("%s: needs %s, which is not in the suite", t.Name, name)
			}
		}
	}
	if cycle := findCycle(s.Tests); cycle != nil {
		return fail("needs has a cycle: %s", strings.Join(cycle, " needs "))
	}
	secrets, err := checkVariables(&root, s.Tests)
	if err != nil {
		return fail("%v", err)
	}
	s.secrets = secrets
	return s, nil
}

func checkVariables(root *yaml.Node, tests []test) (secrets []string, err error) {
	saved := map[string]savedValues{}
	for _, t := range tests {
		saved[t.Name] = t.Save
	}
	check := func(readable []string) func(name string) error {
		return func(name string) error {
			if strings.HasPrefix(name, "secrets.") && !slices.Contains(secrets, name) {
				secrets = append(secrets, name)
			}
			return checkVariable(name, readable, saved)
		}
	}
	doc := root.Content[0]
	for i := 0; i < len(doc.Content); i += 2 {
		switch value := doc.Content[i+1]; doc.Content[i].Value {
		case "defaults":
			if err := walkVariables(value, check(nil)); err != nil {
				return nil, err
			}
		case "tests":
			for j, item := range value.Content {
				t := tests[j]
				for k := 0; k < len(item.Content); k += 2 {
					readable := t.Needs
					if item.Content[k].Value == "cleanup" {
						readable = append(slices.Clone(t.Needs), t.Name)
					}
					if err := walkVariables(item.Content[k+1], check(readable)); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	slices.Sort(secrets)
	return secrets, nil
}

func walkVariables(n *yaml.Node, check func(name string) error) error {
	switch n.Kind {
	case yaml.ScalarNode:
		for rest := n.Value; ; {
			_, after, found := strings.Cut(rest, "${{")
			if !found {
				return nil
			}
			name, after, closed := strings.Cut(after, "}}")
			if !closed {
				return fmt.Errorf("line %d: ${{ has no closing }}", n.Line)
			}
			if err := check(strings.TrimSpace(name)); err != nil {
				return fmt.Errorf("line %d: %v", n.Line, err)
			}
			rest = after
		}
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			if err := walkVariables(n.Content[i], check); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if err := walkVariables(item, check); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkVariable(name string, readable []string, saved map[string]savedValues) error {
	kind, _, _ := strings.Cut(name, ".")
	switch {
	case name == "run.id":
		return nil
	case kind == "secrets":
		if !secretVariable.MatchString(name) {
			return fmt.Errorf("${{ %s }} is not secrets.<group>.<name>, where <group> allows [a-z0-9-] and <name> allows [A-Za-z0-9_-]", name)
		}
		return nil
	case kind == "needs":
		m := needsVariable.FindStringSubmatch(name)
		switch {
		case m == nil:
			return fmt.Errorf("${{ %s }} is not needs.<test>.<saved value>", name)
		case !slices.Contains(readable, m[1]):
			return fmt.Errorf("${{ %s }} reads %s, which is not in needs", name, m[1])
		case !slices.ContainsFunc(saved[m[1]], func(v savedValue) bool { return v.name == m[2] }):
			return fmt.Errorf("${{ %s }}: %s has no saved value %s", name, m[1], m[2])
		}
		return nil
	}
	return fmt.Errorf("unknown variable ${{ %s }} (runner %s)", name, version)
}

func findCycle(tests []test) []string {
	needs := map[string][]string{}
	for _, t := range tests {
		needs[t.Name] = t.Needs
	}
	done := map[string]bool{}
	var chain []string
	var visit func(name string) []string
	visit = func(name string) []string {
		if i := slices.Index(chain, name); i >= 0 {
			return append(slices.Clone(chain[i:]), name)
		}
		if done[name] {
			return nil
		}
		chain = append(chain, name)
		for _, next := range needs[name] {
			if cycle := visit(next); cycle != nil {
				return cycle
			}
		}
		chain = chain[:len(chain)-1]
		done[name] = true
		return nil
	}
	for _, t := range tests {
		if cycle := visit(t.Name); cycle != nil {
			return cycle
		}
	}
	return nil
}

func checkRequest(r request) error {
	bodies := 0
	for _, set := range []bool{r.JSON.Kind != 0, r.Form != nil, r.Body != nil} {
		if set {
			bodies++
		}
	}
	switch {
	case r.Method == "":
		return errors.New("method is missing")
	case r.Path == "":
		return errors.New("path is missing")
	case !strings.HasPrefix(r.Path, "/"):
		return fmt.Errorf("path %q does not start with /", r.Path)
	case bodies > 1:
		return errors.New("set only one of json, form, and body")
	case r.Expect.Status == 0:
		return errors.New("expect.status is missing")
	case !slices.Contains([]string{"", "values", "types", "keys"}, r.Expect.Match):
		return fmt.Errorf("unknown match mode %q (runner %s)", r.Expect.Match, version)
	}
	return checkTimeout(r.Timeout)
}

func checkTimeout(text string) error {
	if text == "" {
		return nil
	}
	timeout, err := time.ParseDuration(text)
	switch {
	case err != nil:
		return fmt.Errorf("timeout %q is not a Go duration such as 10s", text)
	case timeout <= 0:
		return fmt.Errorf("timeout %s is not above zero", text)
	case timeout > maxTimeout:
		return fmt.Errorf("timeout %s is over the limit of 5m", text)
	}
	return nil
}

func yamlLine(err error) string {
	typeErr, ok := errors.AsType[*yaml.TypeError](err)
	if !ok {
		return strings.TrimPrefix(err.Error(), "yaml: ")
	}
	if m := unknownField.FindStringSubmatch(typeErr.Errors[0]); m != nil {
		return fmt.Sprintf("line %s: unknown field %q (runner %s)", m[1], m[2], version)
	}
	return typeErr.Errors[0]
}

func findAnchor(n *yaml.Node) *yaml.Node {
	if n.Anchor != "" || n.Kind == yaml.AliasNode {
		return n
	}
	for _, child := range n.Content {
		if found := findAnchor(child); found != nil {
			return found
		}
	}
	return nil
}

func (v *savedValues) UnmarshalYAML(n *yaml.Node) error {
	return eachPair(n, "save", func(key, value *yaml.Node) error {
		p, ok := parsePath(value.Value)
		if !ok {
			return fmt.Errorf("line %d: save %s: %q is not a path that starts at body or headers, such as body.items[0].id or headers.Location", value.Line, key.Value, value.Value)
		}
		*v = append(*v, savedValue{key.Value, p})
		return nil
	})
}

func (e *equals) UnmarshalYAML(n *yaml.Node) error {
	return eachPair(n, "expect.equal", func(key, value *yaml.Node) error {
		p, ok := parsePath(key.Value)
		switch {
		case ok && p.steps[0] == "headers":
			return fmt.Errorf("line %d: expect.equal %s: check a header with expect.headers", key.Line, key.Value)
		case !ok:
			return fmt.Errorf(`line %d: expect.equal: %q is not a path that starts at body, such as body.items[0].id or body["full.name"]`, key.Line, key.Value)
		}
		*e = append(*e, equal{p, *value})
		return nil
	})
}

func eachPair(n *yaml.Node, field string, f func(key, value *yaml.Node) error) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: %s is not a map", n.Line, field)
	}
	for i := 0; i < len(n.Content); i += 2 {
		if err := f(n.Content[i], n.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}

func parsePath(text string) (path, bool) {
	end := strings.IndexAny(text, ".[")
	if end < 0 {
		end = len(text)
	}
	p := path{text: text, steps: []any{text[:end]}}
	if p.steps[0] != "body" && p.steps[0] != "headers" {
		return path{}, false
	}
	for rest := text[end:]; rest != ""; {
		switch {
		case rest[0] == '.':
			end := strings.IndexAny(rest[1:], ".[")
			if end < 0 {
				end = len(rest) - 1
			}
			if end == 0 {
				return path{}, false
			}
			p.steps = append(p.steps, rest[1:1+end])
			rest = rest[1+end:]
		case strings.HasPrefix(rest, `["`):
			quoted, err := strconv.QuotedPrefix(rest[1:])
			if err != nil || !strings.HasPrefix(rest[1+len(quoted):], "]") {
				return path{}, false
			}
			key, _ := strconv.Unquote(quoted)
			p.steps = append(p.steps, key)
			rest = rest[2+len(quoted):]
		case rest[0] == '[':
			digits, after, closed := strings.Cut(rest[1:], "]")
			index, err := strconv.Atoi(digits)
			if !closed || err != nil || strconv.Itoa(index) != digits || index < 0 {
				return path{}, false
			}
			p.steps = append(p.steps, index)
			rest = after
		default:
			return path{}, false
		}
	}
	return p, true
}
