package main

import (
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

type suite struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Tests      []test `yaml:"tests"`
}

type test struct {
	Name   string `yaml:"name"`
	Method string `yaml:"method"`
	Path   string `yaml:"path"`
	Expect struct {
		Status int `yaml:"status"`
	} `yaml:"expect"`
}

func loadSuite(path, text string) ([]test, error) {
	var s suite
	decoder := yaml.NewDecoder(strings.NewReader(text))
	decoder.KnownFields(true)
	if err := decoder.Decode(&s); err != nil {
		if typeErr, ok := errors.AsType[*yaml.TypeError](err); ok {
			err = errors.New(typeErr.Errors[0])
		}
		return nil, fmt.Errorf("suite %s: %v", path, err)
	}
	if s.APIVersion != "surface/v1" {
		return nil, fmt.Errorf("suite %s: apiVersion must be surface/v1", path)
	}
	if s.Kind != "API" {
		return nil, fmt.Errorf("suite %s: kind must be API", path)
	}
	return s.Tests, nil
}
