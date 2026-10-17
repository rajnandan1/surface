# Surface — Agent Instructions

## Overview

Surface runs end-to-end tests that live in product repos, outside the product's CI, at high parallelism, and reports what failed and why. One Go binary has three commands: `surface serve` is the runner, `surface run` is the client a CI job runs, and `surface check` validates suites locally.

Before you name a domain concept in code, tests, issues, or PRs, read `GLOSSARY.md` and use its word. Before you change an area, read the ADRs in `docs/adr/` that touch it.

## Build & Run

```sh
go build -trimpath ./cmd/surface       # writes ./surface, which git ignores
./surface check surface/               # in a product repo: validate its suites without a runner
```

## Testing

Before a commit, run `go vet ./...` and `go test ./...`. After a change to `.github/workflows/`, also run `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`.

- The chart tests need `helm` and the image test needs `docker`. Without them, those tests skip and pass, so install both to run every test.
- Tests compare exact output text, including the runner version. A version bump changes `VERSION`, `charts/surface/Chart.yaml`, the helm lines in `docs/install.md`, and every `1.0.1` in test expectations. Commit 46a6c22 is the model.

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

Before you write or review code, read `CODING_STANDARDS.md`: the Go style, the error and comment rules, and the test conventions.

## Git Workflow

- One branch per issue, `build/<issue number>-<slug>` (such as `build/55-create-results-dir`), merged into `main` through a pull request. A release branch is `release/vX.Y.Z`.
- A commit message is one sentence that says what changed and why.
- A release is a pushed tag `vX.Y.Z` that equals `VERSION`. `.github/workflows/release.yml` then builds the 6 binaries, the image, and the chart.

## Boundaries

- ✅ **Always do:** Treat every suite as untrusted input (ADR-0001). Pass a value into a workflow `run:` script through `env:`. `release_test.go` fails on a `${{ }}` expression inside a `run:` script of `action.yml` or `release.yml`.
- ⚠️ **Ask first:** Adding a dependency. Changing a type in `internal/api` or a runner route: within a major version the API only grows (ADR-0012). Changing `VERSION`, the release workflow, or `action.yml`. Changing what a suite may contain.
- 🚫 **Never do:** Write a secret value to a log, a failure text, or a run result file. The runner replaces each secret value with its `${{ secrets.… }}` reference before it stores a failure text.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `rajnandan1/surface`, managed with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the five default labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

The layout is single-context. See `docs/agents/domain.md`.

## Documentation

- `docs/design/`: how each part works. Tests read the examples in these docs, so an edit to an example can fail a test.
- `docs/install.md`: the operator guide.
