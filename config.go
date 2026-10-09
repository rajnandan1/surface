package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"
)

type config struct {
	url      string
	targets  []target
	issuer   string
	ownerIDs []string
	runTime  string
	requests int
	runs     int

	resultsDir string
	retention  string

	groups map[string]group
}

func loadConfig(path string) (config, error) {
	c := config{issuer: "https://token.actions.githubusercontent.com"}
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
		case "github":
			if err := c.loadGitHub(path, value); err != nil {
				return c, err
			}
		case "limits":
			if err := c.loadLimits(path, value); err != nil {
				return c, err
			}
		case "results":
			if err := c.loadResults(path, value); err != nil {
				return c, err
			}
		case "groups":
			if err := c.loadGroups(path, value); err != nil {
				return c, err
			}
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
	if len(c.ownerIDs) == 0 {
		return c, fmt.Errorf("%s: github.owner_ids is missing or empty", path)
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

func wholeNumber(node *yaml.Node) (int, bool) {
	n, err := strconv.Atoi(node.Value)
	return n, node.ShortTag() == "!!int" && err == nil && n >= 1
}

func (c *config) loadGitHub(path string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: line %d: github must be a mapping", path, node.Line)
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		switch key.Value {
		case "issuer":
			if value.ShortTag() != "!!str" {
				return fmt.Errorf("%s: line %d: github.issuer must be a string", path, value.Line)
			}
			c.issuer = value.Value
		case "owner_ids":
			if value.Kind != yaml.SequenceNode {
				return fmt.Errorf("%s: line %d: github.owner_ids must be a list", path, value.Line)
			}
			for _, entry := range value.Content {
				var id int64
				if entry.ShortTag() != "!!int" || entry.Decode(&id) != nil || id < 1 {
					return fmt.Errorf("%s: line %d: an owner id must be a whole number of 1 or more", path, entry.Line)
				}
				c.ownerIDs = append(c.ownerIDs, strconv.FormatInt(id, 10))
			}
		default:
			return fmt.Errorf("%s: line %d: unknown key %q", path, key.Line, "github."+key.Value)
		}
	}
	return nil
}

func (c *config) loadLimits(path string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: line %d: limits must be a mapping", path, node.Line)
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		switch key.Value {
		case "run_time":
			if limit, err := time.ParseDuration(value.Value); value.ShortTag() != "!!str" || err != nil || limit <= 0 {
				return fmt.Errorf("%s: line %d: limits.run_time must be a duration above zero, such as 30m", path, value.Line)
			}
			c.runTime = value.Value
		case "requests", "runs":
			n, ok := wholeNumber(value)
			if !ok {
				return fmt.Errorf("%s: line %d: limits.%s must be a whole number of 1 or more", path, value.Line, key.Value)
			}
			if key.Value == "requests" {
				c.requests = n
			} else {
				c.runs = n
			}
		default:
			return fmt.Errorf("%s: line %d: unknown key %q", path, key.Line, "limits."+key.Value)
		}
	}
	return nil
}

func (c *config) loadResults(path string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: line %d: results must be a mapping", path, node.Line)
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		switch key.Value {
		case "dir":
			if value.ShortTag() != "!!str" || value.Value == "" {
				return fmt.Errorf("%s: line %d: results.dir must be a path", path, value.Line)
			}
			c.resultsDir = value.Value
		case "retention":
			if retention, err := time.ParseDuration(value.Value); value.ShortTag() != "!!str" || err != nil || retention <= 0 {
				return fmt.Errorf("%s: line %d: results.retention must be a duration above zero, such as 168h", path, value.Line)
			}
			c.retention = value.Value
		default:
			return fmt.Errorf("%s: line %d: unknown key %q", path, key.Line, "results."+key.Value)
		}
	}
	return nil
}

func (c *config) loadGroups(path string, node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: line %d: groups must be a mapping", path, node.Line)
	}
	c.groups = map[string]group{}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if !slug.MatchString(key.Value) {
			return fmt.Errorf("%s: line %d: group name %q is not a slug of [a-z0-9-]", path, key.Line, key.Value)
		}
		g, err := parseGroup(key, value)
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		c.groups[key.Value] = g
	}
	return nil
}

func parseGroup(name, node *yaml.Node) (group, error) {
	g := group{secret: name.Value}
	if node.Kind != yaml.MappingNode {
		return g, fmt.Errorf("line %d: groups.%s must be a mapping", node.Line, name.Value)
	}
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		switch key.Value {
		case "secret":
			if value.ShortTag() != "!!str" || value.Value == "" {
				return g, fmt.Errorf("line %d: groups.%s.secret must be a Kubernetes Secret name", value.Line, name.Value)
			}
			g.secret = value.Value
		case "callers":
			if value.Kind != yaml.SequenceNode {
				return g, fmt.Errorf("line %d: groups.%s.callers must be a list", value.Line, name.Value)
			}
			for _, entry := range value.Content {
				parsed, err := parseCaller(entry)
				if err != nil {
					return g, err
				}
				g.callers = append(g.callers, parsed)
			}
		case "targets":
			if value.Kind != yaml.SequenceNode {
				return g, fmt.Errorf("line %d: groups.%s.targets must be a list", value.Line, name.Value)
			}
			for _, entry := range value.Content {
				if entry.ShortTag() != "!!str" {
					return g, fmt.Errorf("line %d: a group target must be a string", entry.Line)
				}
				parsed, err := parseTarget(entry.Value)
				if err != nil {
					return g, fmt.Errorf("line %d: %v", entry.Line, err)
				}
				g.targets = append(g.targets, parsed)
			}
		default:
			return g, fmt.Errorf("line %d: unknown key %q", key.Line, "groups."+name.Value+"."+key.Value)
		}
	}
	if len(g.callers) == 0 {
		return g, fmt.Errorf("line %d: groups.%s.callers is missing or empty", name.Line, name.Value)
	}
	if len(g.targets) == 0 {
		return g, fmt.Errorf("line %d: groups.%s.targets is missing or empty", name.Line, name.Value)
	}
	return g, nil
}

func parseCaller(entry *yaml.Node) (caller, error) {
	var c caller
	if entry.Kind == yaml.MappingNode {
		for i := 0; i < len(entry.Content); i += 2 {
			key, value := entry.Content[i], entry.Content[i+1]
			switch key.Value {
			case "repo":
				c.Repo = value.Value
			case "ref":
				c.Ref = value.Value
			default:
				return c, fmt.Errorf("line %d: unknown key %q", key.Line, key.Value)
			}
			if value.ShortTag() != "!!str" {
				return c, fmt.Errorf("line %d: a caller's %s must be a string", value.Line, key.Value)
			}
		}
	}
	if c.Repo == "" || c.Ref == "" {
		return c, fmt.Errorf("line %d: a caller must be a mapping with repo and ref", entry.Line)
	}
	return c, nil
}
