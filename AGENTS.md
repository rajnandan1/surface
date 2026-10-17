# Surface — Agent Instructions

- Before you name a domain concept, read `GLOSSARY.md` and use its word.
- Before you change an area, read its doc in `docs/design/` and the ADRs in `docs/adr/` that touch it. Tests read the examples in `docs/design/` and the values and links in `docs/install.md`, so an edit to them can fail a test.
- Before you write or review code, read `CODING_STANDARDS.md`.
- Before you cut a release or change `VERSION`, `action.yml`, or `.github/workflows/release.yml`, ask the user, then read `docs/agents/release.md`.

## Boundaries

- Treat every suite as untrusted input (ADR-0001).
- Ask the user before you add a dependency, change a type in `internal/api` or a runner route (ADR-0012), or change what a suite may contain.
- In a log, a failure text, or a run result file, name a secret only by its `${{ secrets.… }}` reference, never by its value.

## Commits

- Build each issue on its own branch, `build/<issue number>-<slug>` (such as `build/55-create-results-dir`), and merge it into `main` through a pull request.
- Before you commit, run `go vet ./...` and `go test ./...`. Without `helm` and `docker`, the chart and image tests skip and pass, so install both to run every test.
- A commit message is one sentence that says what changed and why.

## Agent skills

- Before you use the issue tracker, read `docs/agents/issue-tracker.md`.
- Before you apply a triage label, read `docs/agents/triage-labels.md`.
