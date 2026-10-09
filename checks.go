package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type failedCheck struct {
	text  string
	place []any
}

type checker struct {
	mode   string
	checks []failedCheck
}

func checkResponse(t test, status int, header http.Header, body []byte, overCap bool) ([]failedCheck, map[string]json.RawMessage) {
	c := &checker{mode: cmp.Or(t.Expect.Match, "values")}
	if status != t.Expect.Status {
		c.checks = append(c.checks, failedCheck{text: fmt.Sprintf("status: expected %d, got %d", t.Expect.Status, status), place: []any{"status"}})
	}
	for _, name := range slices.Sorted(maps.Keys(t.Expect.Headers)) {
		want, values := t.Expect.Headers[name], header.Values(name)
		switch {
		case len(values) == 0:
			c.fail([]any{"headers", name}, "expected %s, header is missing", compact(want))
		case values[0] != want:
			c.fail([]any{"headers", name}, "expected %s, got %s", compact(want), compact(values[0]))
		}
	}

	var got any
	parsed := false
	bodyChecks := t.Expect.Body.Kind != 0 || len(t.Expect.Equal) > 0 || slices.ContainsFunc(t.Save, func(s savedValue) bool { return s.path.steps[0] == "body" })
	switch {
	case overCap:
		c.fail([]any{"body"}, "response is over 10 MiB")
	case !bodyChecks:
	case !json.Valid(body):
		c.fail([]any{"body"}, "not JSON")
	default:
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		decoder.Decode(&got)
		parsed = true
	}
	if parsed {
		if t.Expect.Body.Kind != 0 {
			c.match(&t.Expect.Body, got, []any{"body"})
		}
		c.mode = "values"
		for _, e := range t.Expect.Equal {
			if value, found := find(got, e.path.steps[1:]); found < len(e.path.steps)-1 {
				c.missing(&e.value, e.path.steps)
			} else {
				c.match(&e.value, value, e.path.steps)
			}
		}
	}

	saved := map[string]json.RawMessage{}
	for _, s := range t.Save {
		var value any
		found, want := 0, len(s.path.steps)-1
		switch {
		case s.path.steps[0] == "headers":
			if name, ok := s.path.steps[want].(string); want == 1 && ok && len(header.Values(name)) > 0 {
				value, found = header.Values(name)[0], 1
			}
		case !parsed:
			continue
		default:
			value, found = find(got, s.path.steps[1:])
		}
		if found < want {
			c.checks = append(c.checks, failedCheck{text: fmt.Sprintf("save %s: %s finds nothing", s.name, s.path.text), place: s.path.steps[:found+2]})
			continue
		}
		saved[s.name], _ = json.Marshal(value)
	}
	return c.checks, saved
}

func lineOne(checks []failedCheck) string {
	switch len(checks) {
	case 0:
		return ""
	case 1:
		return checks[0].text
	}
	return fmt.Sprintf("%s (+%d more)", checks[0].text, len(checks)-1)
}

func find(v any, steps []any) (any, int) {
	for i, step := range steps {
		switch step := step.(type) {
		case string:
			object, ok := v.(map[string]any)
			if !ok {
				return nil, i
			}
			if v, ok = object[step]; !ok {
				return nil, i
			}
		case int:
			items, ok := v.([]any)
			if !ok || step >= len(items) {
				return nil, i
			}
			v = items[step]
		}
	}
	return v, len(steps)
}

func (c *checker) fail(place []any, format string, args ...any) {
	c.checks = append(c.checks, failedCheck{text: placeText(place) + ": " + fmt.Sprintf(format, args...), place: place})
}

func (c *checker) match(want *yaml.Node, got any, place []any) {
	wantType, gotType := exampleType(want), jsonType(got)
	switch {
	case wantType == "null" || gotType == "null":
		if c.mode == "values" && wantType != gotType {
			c.fail(place, "expected %s, got %s", compact(exampleValue(want)), compact(got))
		}
	case wantType != gotType:
		switch c.mode {
		case "values":
			c.fail(place, "expected %s, got %s", compact(exampleValue(want)), compact(got))
		case "types":
			c.fail(place, "expected %s, got %s", wantType, gotType)
		}
	case wantType == "an object":
		object := got.(map[string]any)
		for i := 0; i < len(want.Content); i += 2 {
			key, value := want.Content[i].Value, want.Content[i+1]
			item, ok := object[key]
			if !ok {
				c.missing(value, slices.Concat(place, []any{key}))
				continue
			}
			c.match(value, item, slices.Concat(place, []any{key}))
		}
	case wantType == "an array":
		items := got.([]any)
		if c.mode == "values" {
			if len(items) != len(want.Content) {
				c.fail(place, "expected %d items, got %d", len(want.Content), len(items))
				return
			}
			for i, item := range items {
				c.match(want.Content[i], item, slices.Concat(place, []any{i}))
			}
		} else if len(want.Content) > 0 {
			for i, item := range items {
				c.match(want.Content[0], item, slices.Concat(place, []any{i}))
			}
		}
	case c.mode == "values" && !sameScalar(want, got):
		c.fail(place, "expected %s, got %s", compact(exampleValue(want)), compact(got))
	}
}

func (c *checker) missing(want *yaml.Node, place []any) {
	switch c.mode {
	case "values":
		c.fail(place, "expected %s, key is missing", compact(exampleValue(want)))
	case "types":
		c.fail(place, "expected %s, key is missing", exampleType(want))
	default:
		c.fail(place, "key is missing")
	}
}

func exampleType(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "an object"
	case yaml.SequenceNode:
		return "an array"
	}
	switch n.ShortTag() {
	case "!!int", "!!float":
		return "a number"
	case "!!bool":
		return "a bool"
	case "!!null":
		return "null"
	}
	return "a string"
}

func jsonType(v any) string {
	switch v.(type) {
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case json.Number:
		return "a number"
	case bool:
		return "a bool"
	case nil:
		return "null"
	}
	return "a string"
}

func exampleValue(n *yaml.Node) any {
	switch exampleType(n) {
	case "an object":
		object := map[string]any{}
		for i := 0; i < len(n.Content); i += 2 {
			object[n.Content[i].Value] = exampleValue(n.Content[i+1])
		}
		return object
	case "an array":
		items := []any{}
		for _, item := range n.Content {
			items = append(items, exampleValue(item))
		}
		return items
	case "a number":
		if json.Valid([]byte(n.Value)) {
			return json.Number(n.Value)
		}
		var f float64
		n.Decode(&f)
		return json.Number(strconv.FormatFloat(f, 'g', -1, 64))
	case "a bool":
		var b bool
		n.Decode(&b)
		return b
	case "null":
		return nil
	}
	return n.Value
}

func sameScalar(want *yaml.Node, got any) bool {
	switch got := got.(type) {
	case json.Number:
		w, g := new(big.Rat), new(big.Rat)
		_, wok := w.SetString(string(exampleValue(want).(json.Number)))
		_, gok := g.SetString(string(got))
		return wok && gok && w.Cmp(g) == 0
	default:
		return exampleValue(want) == got
	}
}

func compact(v any) string {
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	encoder.Encode(v)
	text := strings.TrimSuffix(b.String(), "\n")
	if runes := []rune(text); len(runes) > 80 {
		text = string(runes[:80]) + "…"
	}
	return text
}

func placeText(place []any) string {
	var b strings.Builder
	for i, step := range place {
		switch step := step.(type) {
		case int:
			fmt.Fprintf(&b, "[%d]", step)
		case string:
			switch {
			case i == 0:
				b.WriteString(step)
			case step != "" && !strings.ContainsAny(step, `.[]"`):
				b.WriteString("." + step)
			default:
				b.WriteString("[" + strconv.Quote(step) + "]")
			}
		}
	}
	return b.String()
}
