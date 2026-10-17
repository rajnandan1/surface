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

Before a commit, run `go vet ./...` and `go test ./...`.

The chart tests need `helm` and the image test needs `docker`. Without them, those tests skip and pass, so install both to run every test.

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

Before you write or review code, read `CODING_STANDARDS.md`.

## Git Workflow

- One branch per issue, `build/<issue number>-<slug>` (such as `build/55-create-results-dir`), merged into `main` through a pull request.
- A commit message is one sentence that says what changed and why.
- Before you change `VERSION`, `action.yml`, or `.github/workflows/`, or cut a release, read `docs/agents/release.md`.

## Boundaries

- Treat every suite as untrusted input (ADR-0001).
- Ask the user before you:
  - add a dependency.
  - change a type in `internal/api` or a runner route. Within a major version, the API only grows (ADR-0012).
  - change `VERSION`, the release workflow, or `action.yml`.
  - change what a suite may contain.
- In a log, a failure text, or a run result file, name a secret only by its `${{ secrets.… }}` reference. Never write the secret value there. The runner replaces each secret value with its reference before it stores a failure text.

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `rajnandan1/surface`. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the five default labels. See `docs/agents/triage-labels.md`.

### Domain docs

The layout is single-context. See `docs/agents/domain.md`.

## Documentation

- For how a part works, read its doc in `docs/design/`.
- To install or operate the runner, read `docs/install.md`, the operator guide.
- Tests read the examples in `docs/design/` and the values and links in `docs/install.md`. An edit to them can fail a test.
