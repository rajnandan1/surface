package main

import (
	"fmt"
	"os"
	"strconv"

	"go.yaml.in/yaml/v3"
)

type config struct {
	url     string
	targets []target
	limits  limits
}

type limits struct {
	requests, runs int
}

var defaultLimits = limits{requests: 128, runs: 32}

func loadConfig(path string) (config, error) {
	c := config{limits: defaultLimits}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return c, fmt.Errorf("%s: %v", path, err)
	}
	root := &yaml.Node{Kind: yaml.MappingNode}
	if len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return c, fmt.Errorf("%s: line %d: config must be a mapping", path, root.Line)
	}
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		switch key.Value {
		case "url":
			if value.ShortTag() != "!!str" {
				return c, fmt.Errorf("%s: line %d: url must be a string", path, value.Line)
			}
			c.url = value.Value
		case "targets":
			if value.Kind != yaml.SequenceNode {
				return c, fmt.Errorf("%s: line %d: targets must be a list", path, value.Line)
			}
			for _, entry := range value.Content {
				parsed, err := parseTargetEntry(entry)
				if err != nil {
					return c, fmt.Errorf("%s: %v", path, err)
				}
				c.targets = append(c.targets, parsed)
			}
		case "limits":
			parsed, err := parseLimits(value)
			if err != nil {
				return c, fmt.Errorf("%s: %v", path, err)
			}
			c.limits = parsed
		default:
			return c, fmt.Errorf("%s: line %d: unknown key %q", path, key.Line, key.Value)
		}
	}
	if c.url == "" {
		return c, fmt.Errorf("%s: url is missing", path)
	}
	if len(c.targets) == 0 {
		return c, fmt.Errorf("%s: targets is missing or empty", path)
	}
	return c, nil
}

func parseTargetEntry(entry *yaml.Node) (target, error) {
	text, rate := entry, 0
	if entry.Kind == yaml.MappingNode {
		text = nil
		for i := 0; i < len(entry.Content); i += 2 {
			key, value := entry.Content[i], entry.Content[i+1]
			switch key.Value {
			case "url":
				text = value
			case "rate":
				n, ok := wholeNumber(value)
				if !ok {
					return target{}, fmt.Errorf("line %d: rate must be a whole number of 1 or more", value.Line)
				}
				rate = n
			default:
				return target{}, fmt.Errorf("line %d: unknown key %q", key.Line, key.Value)
			}
		}
		if text == nil {
			return target{}, fmt.Errorf("line %d: a target entry has no url", entry.Line)
		}
	}
	if text.ShortTag() != "!!str" {
		return target{}, fmt.Errorf("line %d: a target must be a string, or a mapping with url and rate", text.Line)
	}
	parsed, err := parseTarget(text.Value)
	if err != nil {
		return target{}, fmt.Errorf("line %d: %v", text.Line, err)
	}
	parsed.rate = rate
	return parsed, nil
}

func parseLimits(node *yaml.Node) (limits, error) {
	l := defaultLimits
	if node.Kind != yaml.MappingNode {
		return l, fmt.Errorf("line %d: limits must be a mapping", node.Line)
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		field := map[string]*int{"requests": &l.requests, "runs": &l.runs}[key.Value]
		if field == nil {
			return l, fmt.Errorf("line %d: unknown key %q", key.Line, key.Value)
		}
		n, ok := wholeNumber(value)
		if !ok {
			return l, fmt.Errorf("line %d: limits.%s must be a whole number of 1 or more", value.Line, key.Value)
		}
		*field = n
	}
	return l, nil
}

func wholeNumber(node *yaml.Node) (int, bool) {
	n, err := strconv.Atoi(node.Value)
	return n, node.ShortTag() == "!!int" && err == nil && n >= 1
}
