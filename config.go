package main

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

type config struct {
	url     string
	targets []string
}

func loadConfig(path string) (config, error) {
	var c config
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
			for _, target := range value.Content {
				if target.ShortTag() != "!!str" || target.Value == "" {
					return c, fmt.Errorf("%s: line %d: a target must be a string", path, target.Line)
				}
				c.targets = append(c.targets, target.Value)
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
	return c, nil
}
