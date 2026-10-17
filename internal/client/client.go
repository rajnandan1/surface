package client

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
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/rajnandan1/surface"
	"github.com/rajnandan1/surface/internal/api"
	"github.com/rajnandan1/surface/internal/suite"
)

var PollInterval = 5 * time.Second

func Run(args []string, stdout, stderr io.Writer) int {
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

	signaled, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(signaled, timeout)
	defer cancel()
	runner := &runnerAPI{url: strings.TrimSuffix(*runnerURL, "/")}
	if runner.token, err = fetchToken(ctx, runner.url); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	id, err := runner.start(ctx, api.RunRequest{Target: *target, Suites: suites})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintf(stdout, "surface %s run %s target %s\n", surface.Version, id, *target)

	result, err := runner.wait(ctx, id)
	if signaled.Err() != nil {
		line := fmt.Sprintf("surface run: %v: cancelled run %s", context.Cause(signaled), id)
		if err := runner.cancel(id); err != nil {
			line = fmt.Sprintf("surface run: %v: the cancel of run %s failed: %v", context.Cause(signaled), id, err)
		}
		fmt.Fprintln(stderr, line)
		return 2
	}
	if err != nil && ctx.Err() != nil {
		fmt.Fprintf(stderr, "surface run: run %s did not finish within %s\n", id, *timeoutText)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	printResult(stdout, result)
	printAnnotations(stdout, result, suites)
	if err := writeJobSummary(result); err != nil {
		fmt.Fprintf(stderr, "surface run: job summary: %v\n", err)
	}
	switch result.State {
	case "passed":
		return 0
	case "failed":
		return 1
	}
	return 2
}

func Check(args []string, stdout, stderr io.Writer) int {
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
		if _, err := suite.Load(path, suites[path]); err != nil {
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

func (runner *runnerAPI) start(ctx context.Context, request api.RunRequest) (string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	response, err := runner.send(ctx, http.MethodPost, "/v1/runs", body)
	for err == nil && response.StatusCode == http.StatusServiceUnavailable {
		full := runnerLine(response)
		response.Body.Close()
		select {
		case <-ctx.Done():
			return "", full
		case <-time.After(PollInterval):
		}
		response, err = runner.send(ctx, http.MethodPost, "/v1/runs", body)
	}
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("surface run: runner at %s has no /v1/runs: it may be a different major version", runner.url)
	}
	if response.StatusCode != http.StatusAccepted {
		return "", runnerLine(response)
	}
	var accepted struct{ ID string }
	if err := json.NewDecoder(response.Body).Decode(&accepted); err != nil {
		return "", fmt.Errorf("surface run: the runner's answer is not a run id: %v", err)
	}
	return accepted.ID, nil
}

func (runner *runnerAPI) cancel(id string) error {
	ctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	response, err := runner.send(ctx, http.MethodPost, "/v1/runs/"+id+"/cancel", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return runnerLine(response)
	}
	return nil
}

var errPollAgain = errors.New("surface run: poll again")

func (runner *runnerAPI) wait(ctx context.Context, id string) (api.RunResult, error) {
	for {
		result, err := runner.poll(ctx, id)
		if err != errPollAgain {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(PollInterval):
		}
	}
}

func (runner *runnerAPI) poll(ctx context.Context, id string) (api.RunResult, error) {
	var result api.RunResult
	response, err := runner.send(ctx, http.MethodGet, "/v1/runs/"+id, nil)
	if _, network := errors.AsType[*url.Error](err); network {
		return result, errPollAgain
	}
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 500:
		return result, errPollAgain
	case response.StatusCode != http.StatusOK:
		return result, runnerLine(response)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, fmt.Errorf("surface run: run %s: the runner's answer is not a run result: %v", id, err)
	}
	if !api.IsFinal(result.State) {
		return result, errPollAgain
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
		return nil, fmt.Errorf("surface run: %w", err)
	}
	return response, nil
}

func runnerLine(response *http.Response) error {
	text, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	line, _, _ := strings.Cut(strings.TrimSpace(string(text)), "\n")
	return errors.New(cmp.Or(line, "surface run: the runner answered "+response.Status))
}

func printResult(w io.Writer, result api.RunResult) {
	for _, suite := range result.Suites {
		if suite.Failure != "" {
			fmt.Fprintln(w, suite.Failure)
		} else {
			fmt.Fprintf(w, "%s: %s\n", suite.Path, api.CountTests(suite))
		}
	}
	for _, suite := range result.Suites {
		for _, t := range suite.Tests {
			if t.Failure != "" {
				fmt.Fprintf(w, "\n%s %s\n  %s\n", suite.Path, t.Name, strings.ReplaceAll(t.Failure, "\n", "\n  "))
			}
			if t.Cleanup != nil && t.Cleanup.Failure != "" {
				fmt.Fprintf(w, "\n%s %s cleanup\n  %s\n", suite.Path, t.Name, strings.ReplaceAll(t.Cleanup.Failure, "\n", "\n  "))
			}
		}
	}
	if result.State == "error" {
		fmt.Fprintf(w, "\nrun %s: error: %s\n", result.ID, result.Reason)
		return
	}
	fmt.Fprintf(w, "\nrun %s: %s\n", result.ID, api.CountTests(result.Suites...))
}

var (
	annotationData     = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	annotationProperty = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
)

func printAnnotations(w io.Writer, result api.RunResult, suites map[string]string) {
	annotate := func(path string, line int, message string) {
		lineOne, _, _ := strings.Cut(message, "\n")
		fmt.Fprintf(w, "::error file=%s,line=%d::%s\n", annotationProperty.Replace(path), line, annotationData.Replace(lineOne))
	}
	for _, ran := range result.Suites {
		if ran.Failure != "" {
			annotate(ran.Path, 1, ran.Failure)
			continue
		}
		lines := map[string]int{}
		if s, err := suite.Load(ran.Path, suites[ran.Path]); err == nil {
			for _, t := range s.Tests {
				lines[t.Name] = t.Line
			}
		}
		for _, t := range ran.Tests {
			line := cmp.Or(lines[t.Name], 1)
			if t.State == "failed" {
				annotate(ran.Path, line, t.Name+": "+t.Failure)
			}
			if t.Cleanup != nil && t.Cleanup.State == "failed" {
				annotate(ran.Path, line, t.Name+" cleanup: "+t.Cleanup.Failure)
			}
		}
	}
}

const maxJobSummary = 1 << 20

func jobSummary(result api.RunResult) string {
	title := result.State
	if result.State == "error" {
		title += ": " + result.Reason
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### surface run %s: %s\n\n| Suite | Passed | Failed | Skipped | Cleanup failed |\n| --- | ---: | ---: | ---: | ---: |\n", result.ID, title)
	row := func(name string, c api.Counts) {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", name, c.Passed, c.Failed, c.Skipped, c.CleanupFailed)
	}
	for _, suite := range result.Suites {
		name := "`" + strings.ReplaceAll(suite.Path, "|", `\|`) + "`"
		if suite.Failure != "" {
			name += " (load failed)"
		}
		row(name, api.CountTests(suite))
	}
	row("Total", api.CountTests(result.Suites...))
	if b.Len() <= maxJobSummary {
		return b.String()
	}
	const cut = "\nThe job summary stops here, because GitHub takes at most 1 MiB per step. The CI log shows every suite.\n"
	text := b.String()[:maxJobSummary-len(cut)]
	return text[:strings.LastIndexByte(text, '\n')+1] + cut
}

func writeJobSummary(result api.RunResult) error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = file.WriteString(jobSummary(result))
	return errors.Join(err, file.Close())
}
