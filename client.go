package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var pollInterval = 5 * time.Second

func runCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("surface run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runnerURL := flags.String("runner", "", "the runner's URL")
	target := flags.String("target", "", "the target the run calls")
	timeoutText := flags.String("timeout", "30m", "the wait limit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	timeout, err := time.ParseDuration(*timeoutText)
	if *runnerURL == "" || *target == "" || err != nil || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: surface run --runner <url> --target <url> [--timeout <duration>] <suites>")
		return 2
	}

	suites, err := collectSuites(flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "surface run: %v\n", err)
		return 2
	}
	if len(suites) == 0 {
		fmt.Fprintf(stderr, "surface run: no suites under %s\n", flags.Arg(0))
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	runner := &runnerAPI{url: strings.TrimSuffix(*runnerURL, "/")}
	if runner.token, err = fetchToken(ctx, runner.url); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	id, err := runner.start(ctx, runRequest{Target: *target, Suites: suites})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stdout, "surface %s run %s target %s\n", version, id, *target)

	result, err := runner.wait(ctx, id)
	if err != nil && ctx.Err() != nil {
		fmt.Fprintf(stderr, "surface run: run %s did not finish within %s\n", id, *timeoutText)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	printResult(stdout, result)
	switch result.State {
	case "passed":
		return 0
	case "failed":
		return 1
	}
	return 2
}

func checkCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: surface check <suites>")
		return 2
	}
	suites, err := collectSuites(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "surface check: %v\n", err)
		return 2
	}
	code := 0
	for _, path := range slices.Sorted(maps.Keys(suites)) {
		if _, err := loadSuite(path, suites[path]); err != nil {
			fmt.Fprintln(stdout, err)
			code = 1
		}
	}
	return code
}

func collectSuites(root string) (map[string]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	suites := map[string]string{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if ext := filepath.Ext(path); path != root && ext != ".yaml" && ext != ".yml" {
			return nil
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		key, err := filepath.Rel(cwd, abs)
		if err != nil {
			return err
		}
		suites[filepath.ToSlash(key)] = string(text)
		return nil
	})
	return suites, err
}

func fetchToken(ctx context.Context, audience string) (string, error) {
	requestURL, requestToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
	if requestURL == "" || requestToken == "" {
		return "", errors.New("surface run: no OIDC token: not inside GitHub Actions, or the job is missing permissions: id-token: write")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL+"&audience="+url.QueryEscape(audience), nil)
	if err != nil {
		return "", fmt.Errorf("surface run: no OIDC token: %v", err)
	}
	request.Header.Set("Authorization", "bearer "+requestToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("surface run: no OIDC token: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("surface run: no OIDC token: GitHub answered %s", response.Status)
	}
	var answer struct{ Value string }
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil || answer.Value == "" {
		return "", errors.New("surface run: no OIDC token: GitHub's answer has no value")
	}
	return answer.Value, nil
}

type runnerAPI struct {
	url   string
	token string
}

func (runner *runnerAPI) start(ctx context.Context, request runRequest) (string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	response, err := runner.send(ctx, http.MethodPost, "/v1/runs", body)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return "", runnerLine(response)
	}
	var accepted struct{ ID string }
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		return "", fmt.Errorf("surface run: the runner's answer is not a run id: %v", err)
	}
	return accepted.ID, nil
}

func (runner *runnerAPI) wait(ctx context.Context, id string) (runResult, error) {
	for {
		result, err := runner.poll(ctx, id)
		if err != nil || isFinal(result.State) {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func (runner *runnerAPI) poll(ctx context.Context, id string) (runResult, error) {
	var result runResult
	response, err := runner.send(ctx, http.MethodGet, "/v1/runs/"+id, nil)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, runnerLine(response)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("surface run: run %s: the runner's answer is not a run result: %v", id, err)
	}
	return result, nil
}

func (runner *runnerAPI) send(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	response, err := runner.sendOnce(ctx, method, path, body)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		return response, err
	}
	response.Body.Close()
	if runner.token, err = fetchToken(ctx, runner.url); err != nil {
		return nil, err
	}
	return runner.sendOnce(ctx, method, path, body)
}

func (runner *runnerAPI) sendOnce(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, runner.url+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("surface run: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+runner.token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("surface run: %v", err)
	}
	return response, nil
}

func runnerLine(response *http.Response) error {
	text, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	line, _, _ := strings.Cut(strings.TrimSpace(string(text)), "\n")
	return errors.New(cmp.Or(line, "surface run: the runner answered "+response.Status))
}

func isFinal(state string) bool {
	switch state {
	case "passed", "failed", "error", "cancelled":
		return true
	}
	return false
}

func printResult(w io.Writer, result runResult) {
	for _, suite := range result.Suites {
		if suite.Failure != "" {
			fmt.Fprintln(w, suite.Failure)
		} else {
			fmt.Fprintf(w, "%s: %s\n", suite.Path, countTests(suite))
		}
	}
	for _, suite := range result.Suites {
		for _, t := range suite.Tests {
			if t.Failure != "" {
				fmt.Fprintf(w, "\n%s %s\n  %s\n", suite.Path, t.Name, strings.ReplaceAll(t.Failure, "\n", "\n  "))
			}
		}
	}
	fmt.Fprintf(w, "\nrun %s: %s\n", result.ID, countTests(result.Suites...))
}
