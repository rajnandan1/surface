# Surface — Agent Instructions

## Overview

Surface runs end-to-end tests that live in product repos, outside the product's CI, at high parallelism, and reports what failed and why. One Go binary has three commands: `surface serve` is the runner, `surface run` is the client a CI job runs, and `surface check` validates suites locally.

Before you name a domain concept in code, tests, issues, or PRs, read `GLOSSARY.md` and use its word. Before you change an area, read the ADRs in `docs/adr/` that touch it.

## Build & Run

```sh
go build -trimpath ./cmd/surface       # writes ./surface, which git ignores
./surface check surface/               # in a product repo: validate its suites without a runner
docker build .                         # the runner image
helm lint charts/surface               # the runner chart
```

## Testing

```sh
go vet ./...                                                      # CI step
go test ./...                                                     # CI step
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12         # CI step
go test -run TestLoadSuiteRejectsABrokenSuite ./internal/suite    # one test
```

- The chart tests need `helm` and the image test needs `docker`. Without them, those tests skip and pass, so install both to run every test.
- Tests compare exact output text, including the runner version. A version bump changes `VERSION`, `charts/surface/Chart.yaml`, the helm lines in `docs/install.md`, and every `1.0.1` in test expectations. Commit 46a6c22 is the model.
- A test name is a sentence that states the behavior: `TestARunPastItsRunTimeLimitEndsInErrorAndItsCleanupRuns`.
- Tests that run the client against a real runner live in `internal/runner` and call `client.Run`. They set `client.PollInterval` low and restore it.
- Each package's tests carry their own small helpers (`header`, `usersSuite`, `writeSuite`, `fakeGitHubJob`). Copy a helper into the package that needs it.

## Project Structure

```
version.go         package surface: Version, embedded from VERSION
cmd/surface/       main.go only: sends serve, run, check to their package
internal/api/      the types the client and the runner both send and read
internal/suite/    load a suite, send a test, check the response, write the failure text
internal/runner/   the runner: HTTP API, config, OIDC identity, secret groups, run result files
internal/client/   surface run and surface check
charts/surface/    the Helm chart for the runner
action.yml         the GitHub Action: downloads the release binary and runs surface run
```

- `version.go` stays at the root because `//go:embed VERSION` only reads its own folder, and `action.yml` and the release workflow read `VERSION` at the root too.
- The root `release_test.go` and `image_test.go` test `action.yml`, the release workflow, and the Dockerfile.

## Code Style

- Go 1.26 with current idioms: `errors.AsType`, `wg.Go`, `cmp.Or`, `strings.SplitSeq`, method patterns in `http.ServeMux`. `go fix -diff ./...` prints nothing.
- The standard library first. The only dependencies are `go.yaml.in/yaml/v3` and `github.com/coreos/go-oidc/v3`.
- An error is one line of plain English that starts with where it came from. A rejected unknown field names the runner version.
- `surface run` exits 0 when the run passed, 1 when it failed, and 2 on every other end: error, cancel, timeout, bad usage.
- A comment states only what the code cannot show, in one line.
- A `Suite` or `Test` field that `suite.Load` fills in carries `yaml:"-"`, so a suite that sets it fails to load.
- yaml.v3 prints Go type names in its errors (`cannot unmarshal !!seq into suite.Defaults`), so renaming a type in `internal/suite` changes text users see.

Example from `internal/runner/secrets.go`:

```go
switch {
case read[groupName] != nil:
	return fmt.Errorf("suite %s: %v", path, read[groupName])
case !found:
	return fmt.Errorf("suite %s: secret group %q has no key %q", path, groupName, key)
case value == "":
	return fmt.Errorf("suite %s: secret group %q key %q is empty", path, groupName, key)
}
```

## Git Workflow

- One branch per issue, `build/<issue number>-<slug>` (such as `build/55-create-results-dir`), merged into `main` through a pull request. A release branch is `release/vX.Y.Z`.
- A commit message is one sentence that says what changed and why.
- A release is a pushed tag `vX.Y.Z` that equals `VERSION`. `.github/workflows/release.yml` then builds the 6 binaries, the image, and the chart.

## Boundaries

- ✅ **Always do:** Run `go vet ./...` and `go test ./...` before a commit. Treat every suite as untrusted input (ADR-0001). Pass a value into a workflow `run:` script through `env:`. `release_test.go` fails on a `${{ }}` expression inside a `run:` script of `action.yml` or `release.yml`.
- ⚠️ **Ask first:** Adding a dependency. Changing a type in `internal/api` or a runner route: within a major version the API only grows (ADR-0012). Changing `VERSION`, the release workflow, or `action.yml`. Changing what a suite may contain.
- 🚫 **Never do:** Write a secret value to a log, a failure text, or a run result file. The runner replaces each secret value with its `${{ secrets.… }}` reference before it stores a failure text.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `rajnandan1/surface`, managed with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the five default labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `GLOSSARY.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Documentation

- `GLOSSARY.md`: the domain words.
- `docs/adr/`: the decisions and why they were made.
- `docs/design/`: how each part works. Tests read the examples in these docs, so an edit to an example can fail a test.
- `docs/install.md`: the operator guide.
