# Surface has its own suite format and engine

Surface does not use Venom, runn, or scenarigo as its engine (the Go code in the runner that runs one test) or as its suite format. None of them can be limited to HTTP requests with a library setting, so each one breaks [ADR 0001](0001-suites-are-untrusted-input.md) unless we fork it, and we do not fork. Without a tool's engine, its format gives us nothing, because familiarity with a format is not a goal. So surface writes its own format and its own engine in Go, and copies ideas from these tools.

## Considered options

- **Adopt a tool's format and engine.** Rejected. The source of each tool, read on 2026-10-08, shows the gaps:
  - Venom loads a `.so` plugin file from the suite's folder for an unknown step `type`. Its HTTP step reads local files (`bodyfile`). It has no option to replace its HTTP client, and its logger is global.
  - runn lets a suite read the runner's environment variables (`${X}`, `env.X`), with no off switch. No setting blocks its database, SSH, or browser runners, or its `dump:` writes to any path.
  - scenarigo lets a suite read the runner's environment variables (`{{env.X}}`), with no off switch. No setting turns off plugins or `include:`. It is still at v0.x.
- **Adopt a tool's format, write our own engine.** Rejected. We would follow another project's format and its changes, and get nothing for it.

Sources: [the research note](https://github.com/rajnandan1/surface/blob/research/existing-api-test-tools/docs/research/existing-api-test-tools.md) and the resolution of [Build on an existing tool or write our own runner?](https://github.com/rajnandan1/surface/issues/4).

## Consequences

- The engine may import stable, mature libraries. Stable means a tagged release v1.0.0 or later. Mature means a release in the last 12 months. Each one is pinned to an exact tagged version in `go.mod`, never a branch or an untagged commit.
- The suite format has room for ideas from other tools. [What does a suite file look like?](https://github.com/rajnandan1/surface/issues/5) decides which ones go in.
