package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const maxResponseBody = 10 << 20

var targetClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// newRequest is the one place where the runner puts variable values into a request.
func newRequest(target string, d defaults, t request, vars map[string]json.RawMessage) (*http.Request, error) {
	var body bytes.Buffer
	contentType := ""
	switch {
	case t.JSON.Kind != 0:
		if err := writeJSON(&body, &t.JSON, vars); err != nil {
			return nil, err
		}
		contentType = "application/json"
	case t.Form != nil:
		form, err := formValues("form", t.Form, vars)
		if err != nil {
			return nil, err
		}
		body.WriteString(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	case t.Body != nil:
		text, err := expand(*t.Body, vars, nil)
		if err != nil {
			return nil, err
		}
		body.WriteString(text)
	}
	path, err := expand(t.Path, vars, url.PathEscape)
	if err != nil {
		return nil, err
	}
	query, err := formValues("query", t.Query, vars)
	if err != nil {
		return nil, err
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	request, err := http.NewRequest(t.Method, target+path, bytes.NewReader(body.Bytes()))
	if err != nil {
		return nil, err
	}
	for name, value := range d.Headers {
		if value != nil {
			request.Header.Set(name, *value)
		}
	}
	for name, value := range t.Headers {
		request.Header.Del(name)
		if value != nil {
			request.Header.Set(name, *value)
		}
	}
	for _, values := range request.Header {
		if values[0], err = expand(values[0], vars, nil); err != nil {
			return nil, err
		}
	}
	if _, set := request.Header["Content-Type"]; !set && contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request, nil
}

func formValues(field string, m map[string]yaml.Node, vars map[string]json.RawMessage) (url.Values, error) {
	values := url.Values{}
	for key, n := range m {
		items := []*yaml.Node{&n}
		if n.Kind == yaml.SequenceNode {
			items = n.Content
		}
		for _, item := range items {
			if item.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("line %d: %s.%s is not text, a number, a bool, or a list of them", item.Line, field, key)
			}
			text, err := expand(item.Value, vars, nil)
			if err != nil {
				return nil, err
			}
			values.Add(key, text)
		}
	}
	return values, nil
}

func writeJSON(b *bytes.Buffer, n *yaml.Node, vars map[string]json.RawMessage) error {
	switch n.Kind {
	case yaml.MappingNode:
		b.WriteByte('{')
		for i := 0; i < len(n.Content); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			key, _ := json.Marshal(n.Content[i].Value)
			b.Write(key)
			b.WriteByte(':')
			if err := writeJSON(b, n.Content[i+1], vars); err != nil {
				return err
			}
		}
		b.WriteByte('}')
		return nil
	case yaml.SequenceNode:
		b.WriteByte('[')
		for i, item := range n.Content {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSON(b, item, vars); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	}
	var value any = n.Value
	switch n.ShortTag() {
	case "!!int", "!!float", "!!bool", "!!null":
		if err := n.Decode(&value); err != nil {
			return err
		}
	default:
		if strings.HasPrefix(n.Value, "${{") && strings.Index(n.Value, "}}") == len(n.Value)-2 {
			name := strings.TrimSpace(n.Value[3 : len(n.Value)-2])
			if vars[name] == nil {
				return fmt.Errorf("${{ %s }} has no value", name)
			}
			b.Write(vars[name])
			return nil
		}
		text, err := expand(n.Value, vars, nil)
		if err != nil {
			return err
		}
		value = text
	}
	out, err := json.Marshal(value)
	b.Write(out)
	return err
}

func expand(text string, vars map[string]json.RawMessage, escape func(string) string) (string, error) {
	var out strings.Builder
	for {
		before, after, found := strings.Cut(text, "${{")
		out.WriteString(before)
		if !found {
			return out.String(), nil
		}
		name, rest, _ := strings.Cut(after, "}}")
		name = strings.TrimSpace(name)
		raw := vars[name]
		var value string
		switch {
		case raw == nil:
			return "", fmt.Errorf("${{ %s }} has no value", name)
		case raw[0] == '"':
			json.Unmarshal(raw, &value)
		case raw[0] == '{':
			return "", fmt.Errorf("${{ %s }} is an object inside text", name)
		case raw[0] == '[':
			return "", fmt.Errorf("${{ %s }} is an array inside text", name)
		default:
			value = string(raw)
		}
		if escape != nil {
			value = escape(value)
		}
		out.WriteString(value)
		text = rest
	}
}

// send returns at most maxResponseBody+1 bytes of the body, so check can tell a body over the cap.
func send(runID string, request *http.Request, timeout string) (*http.Response, []byte, time.Duration, error) {
	userAgent := "surface/" + version + " (run " + runID + ")"
	if own := request.Header.Get("User-Agent"); own != "" {
		userAgent = own + " " + userAgent
	}
	request.Header.Set("User-Agent", userAgent)
	limit, _ := time.ParseDuration(timeout)
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	start := time.Now()
	response, err := targetClient.Do(request.WithContext(ctx))
	if err != nil {
		return nil, nil, 0, noResponse(err, timeout)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody+1))
	if err != nil {
		return nil, nil, 0, noResponse(err, timeout)
	}
	return response, body, time.Since(start), nil
}

func noResponse(err error, timeout string) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("timeout after %s", timeout)
	}
	for inner := errors.Unwrap(err); inner != nil; inner = errors.Unwrap(err) {
		err = inner
	}
	return err
}
