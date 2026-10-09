# The failure text is the request and the response, with the checks marked

The failure text of a failed test is line one, the first failed check as `<what>: expected <X>, got <Y>`, then the request as the runner sent it, then the response as it came back, with a `✓` or `✗` mark beside each line a check looked at. Nothing is folded. A body is cut at 4 KiB. The runner runs every check and reports every failed one. A skipped test prints one line with the reason. The GitHub annotation is the test name plus line one, so line one stands alone.

## Considered options

- **A list of checks, with the request and the response folded in a `::group::` block.** Rejected. `::group::` folds only in the GitHub Actions web page. A downloaded log, a terminal, or another CI shows the raw marker lines. And a reader needs the response for nearly every failure, so the fold costs a click per failed test.
- **A git-style diff of the expected and the actual response.** Rejected. The example body is partial: a key the response has and the example does not pass ([suite-format.md](../design/suite-format.md)). A diff of the two documents either shows those passing keys as `+` lines or hides part of the response. And `-status 401` against `+status 200` says no more than the one-line check does.
- **The request and the response only in the run result, with a pointer line in the log.** Rejected. `GET /v1/runs/<id>` needs a GitHub OIDC token ([ADR 0007](0007-ci-job-sends-the-suites-and-proves-its-caller-with-a-github-oidc-token.md)), so a developer at a laptop cannot read it. The log is the one place a developer reads.
- **Stop at the first failed check.** Rejected. A `500` with a JSON error body is common, and the body says why. One failure text that shows every failed check saves a second run.

Sources: [the prototype](https://github.com/rajnandan1/surface/blob/prototype/failed-test-output/docs/prototype/failed-test-output.html) on the branch `prototype/failed-test-output`, which showed the three shapes side by side, and the resolution of [What does a failed test print?](https://github.com/rajnandan1/surface/issues/9).

## Consequences

- A failed test costs up to about 8 KiB of log: the request and the response, each body cut at 4 KiB. A run with many failures has a long log.
- The run result holds the failure text and nothing else about the request and the response. v1 stores no separate request and response record.
- A test state `skipped` exists, and the suite line and the last line of the log count it.
- The runner redacts secret values in the text before it stores it ([ADR 0005](0005-secret-groups-in-kubernetes-secrets.md)), because the request headers are in the text.
- The design is in [docs/design/failure-text.md](../design/failure-text.md).
