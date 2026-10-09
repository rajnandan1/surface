# Runs

A run is one execution of a set of suites, started by one CI job. A run request is the message the CI job sends to start it, and the client is the `surface` command that runs inside the CI job, sends the run request, and waits for the result (see [GLOSSARY.md](../../GLOSSARY.md)). This doc defines how the CI job starts a run, how the runner learns who the caller is, which owners may use the runner, how the client waits, what the CI log shows, and the shape of the GitHub Action. The decisions come from [How does a CI job start a run and get the result?](https://github.com/rajnandan1/surface/issues/8), [Which callers may start a run?](https://github.com/rajnandan1/surface/issues/14), [ADR 0007](../adr/0007-ci-job-sends-the-suites-and-proves-its-caller-with-a-github-oidc-token.md), and [ADR 0011](../adr/0011-only-listed-github-owners-may-use-the-runner-matched-by-owner-id.md).

## Example

The product repo's workflow:

```yaml
permissions:
  id-token: write        # GitHub issues the OIDC token only with this.
  contents: read
steps:
  - uses: actions/checkout@v5
  - uses: rajnandan1/surface@v1
    with:
      runner: https://surface.internal.example.com   # Required. Also the token audience.
      target: https://pr-${{ github.event.number }}.preview.crustdata.com   # Required. See targets.md.
      suites: surface/                               # Optional. The default is surface/.
      timeout: 30m                                   # Optional. The wait limit. The default is 30m.
```

The operator side, in the runner's config:

```yaml
url: https://surface.internal.example.com   # Required. The runner's own URL, the audience it accepts.
github:
  issuer: https://token.actions.githubusercontent.com   # Optional. This is the default. GitHub Enterprise Server uses https://HOSTNAME/_services/token.
  owner_ids:            # Required. The owners whose repos may use this runner. See "Who may use the runner".
    - 63745551          # crustdata
```

The CI log, on a failed run:

```
surface 1.2.0 run 01j9z3k4m5n6p7q8r9s0t1u2v3 target https://pr-123.preview.crustdata.com
surface/api/users.yaml: 4 passed, 1 failed
surface/api/search.yaml: 2 passed

surface/api/users.yaml update-user
  status: expected 200, got 404
  > PATCH https://pr-123.preview.crustdata.com/users/8812
  > Authorization: Bearer ${{ secrets.production.api_token }}
  > Content-Type: application/json
  > User-Agent: surface/1.2.0
  >
  > {
  >   "user_id": 8812,
  >   "note": "updated by 01j9z3k4m5n6p7q8r9s0t1u2v3"
  > }
  < 404 Not Found in 31ms                 ✗ expected 200
  < Content-Type: application/json
  <
  < {
  <   "error": "user 8812 not found"
  < }

run 01j9z3k4m5n6p7q8r9s0t1u2v3: 6 passed, 1 failed
```

## The flow

1. The client collects the suite files and asks GitHub for an OIDC token with the runner URL as audience.
2. The client sends the run request, `POST <runner>/v1/runs`, with the token in `Authorization: Bearer`.
3. The runner checks the token and the token's owner, reads the caller from the token, checks the target against the target list, and answers `202 Accepted` with the run id. The run starts.
4. The client polls `GET <runner>/v1/runs/<id>` every 5 seconds until the run's `state` is final or the wait limit passes.
5. The client prints the result, writes the job summary and the annotations, and exits with `0`, `1`, or `2`.

## The suite source

- The CI job sends the suite files inside the run request. The runner has no git and no GitHub credential. It never fetches a repo.
- The client walks the `suites` path for `*.yaml` and `*.yml` files, in sorted order. A file path given directly is sent as is. Zero files is exit `2` with the message `surface run: no suites under surface/`.
- Each suite's key in the request is its path relative to the working directory, for example `surface/api/users.yaml`. In GitHub Actions the working directory is the repo root, so the key is the path in the repo, and "the file path in the repo names the suite" ([suite-format.md](suite-format.md)) holds.
- The runner rejects a request body over 16 MiB with `413`.

## Identity

- Every request to the runner carries a GitHub OIDC token (OpenID Connect: a signed token that GitHub gives a job on request; it names the repo, the ref, the commit, and the workflow) in `Authorization: Bearer <token>`. The CI job sends no secret and no API key.
- The client fetches the token itself. GitHub gives a job the environment variables `ACTIONS_ID_TOKEN_REQUEST_URL` and `ACTIONS_ID_TOKEN_REQUEST_TOKEN`. The client sends `GET <ACTIONS_ID_TOKEN_REQUEST_URL>&audience=<runner url>` with `Authorization: bearer <ACTIONS_ID_TOKEN_REQUEST_TOKEN>` and reads the token from the `value` field of the JSON answer. When the variables are missing, the client exits `2` with `surface run: no OIDC token: not inside GitHub Actions, or the job is missing permissions: id-token: write`.
- The audience is the runner's URL. The client sends the `runner` input with one trailing `/` removed. The operator sets the same text as `url` in the runner config. `url` is required, and the runner refuses to start without it. The two must be equal byte for byte.
- The runner checks the token's signature against the issuer's published keys, the issuer, the audience, and the expiry, with 60 seconds of clock skew. It fetches the keys from `<issuer>/.well-known/openid-configuration` and caches them. GitHub does not document the token lifetime, so the runner trusts `exp` alone.
- The caller is the `repository` claim as `repo` and the `ref` claim as `ref`. For a push, `ref` is `refs/heads/<branch>` or `refs/tags/<tag>`. For a pull request, it is `refs/pull/<number>/merge`. The runner matches the caller against each secret group's `callers` ([secrets.md](secrets.md)). The runner also records `sha`, `run_id`, and `workflow_ref` with the run, so a result can point back at `https://github.com/<repository>/actions/runs/<run_id>`.
- v1 trusts GitHub only. The runner config has one `github:` block with an optional `issuer` and the required `owner_ids`. Another CI vendor later is another block with its own issuer, claim names, and allow list, with no change to the run request.
- A pull request from a fork cannot start a run. GitHub turns every write permission of a fork's pull request to read-only unless the repo setting "Send write tokens to workflows from pull requests" is on, and `id-token` has only the levels `write` and `none`, so the job gets no token. Surface does not recommend `pull_request_target` as a way around it, because that event runs the base branch's workflow with the base repo's permissions on a fork's code.
- The runner verifies tokens with `github.com/coreos/go-oidc/v3` at v3.21.0. On 2026-10-08 it passed the library rule of [ADR 0002](../adr/0002-own-suite-format-and-engine.md).

## Who may use the runner

- An owner is the GitHub org or user account that holds a repo ([GLOSSARY.md](../../GLOSSARY.md)). The runner config's `github.owner_ids` lists the owners whose repos may use the runner. Each entry is the owner's ID: the number GitHub gives the account.
- `owner_ids` is required and must not be empty. The runner refuses to start without it, or when an entry is not a whole number of 1 or more.
- The token carries the owner's ID in the `repository_owner_id` claim, as decimal text, for example `"63745551"`. The runner compares that text with the entries, written as decimal text.
- The runner checks the owner on every route but `GET /healthz`. The check runs right after the token check and before the runner reads the request body. So a stranger's repo cannot make the runner read 16 MiB, hold a run slot, or write a run result file.
- A token whose owner is not on the list gets `403` with one line, for example `run: repo "someone/api" (owner id 98765) may not use this runner`. The runner writes no log line for a refusal, because a stranger could fill the log with requests. An operator whose own repo is refused reads the line in the CI log and copies the number.
- The operator finds an ID with `gh api orgs/<name> --jq .id` for an org, or `gh api users/<name> --jq .id` for a user account.
- Why IDs and not names: when an org or a user is renamed, GitHub frees the old name at once. When an org is deleted, its name is free after 90 days. A stranger who registers the name and creates a repo with the same name gets a token whose `repository` claim matches the old one. The ID never changes on a rename. GitHub changed its own default `sub` claim to carry the IDs for the same reason ([changelog, 2026-04-23](https://github.blog/changelog/2026-04-23-immutable-subject-claims-for-github-actions-oidc-tokens/)).
- Why no list per repo: a repo of a listed owner is the operator's own. The secret group binding still decides which repos and refs may use which secrets ([secrets.md](secrets.md)).
- The secret group `callers` and the rule in "Who may read or cancel a run" stay on names. A stranger who registers a recycled name never reaches them, because the owner check runs first.
- There is no cap on runs per repo. One repo can hold every run slot ([limits.md](limits.md)). The operator fixes such a repo, because it is the operator's own. A measured case of one repo starving the others is the trigger to add a cap.
- A scheduled run, later, has no token. The operator defines it in the runner config, so the owner list does not apply to it. Another CI vendor, later, gets its own block with its own allow list.
- The decision is recorded in [ADR 0011](../adr/0011-only-listed-github-owners-may-use-the-runner-matched-by-owner-id.md).

## Waiting

- The runner answers the run request with `202 Accepted` and `{"id": "<run id>"}` as soon as the run is accepted. It holds no request open.
- The client polls `GET /v1/runs/<id>` every 5 seconds.
- On `401`, the client fetches a fresh token once and retries. On `403`, it exits `2` with the runner's line. On a `5xx` or a network error, it retries at the next poll. On `404`, it exits `2`: the run is unknown or no longer stored. A `404` to the run request means the runner has no `/v1/` routes, and the client exits `2` with `surface run: runner at <url> has no /v1/runs: it may be a different major version` ([install.md](install.md)).
- On a `503` to the run request, the client sends the run request again every 5 seconds until the wait limit passes, because a `503` means the runner made no run ([runner.md](runner.md)). On a network error to the run request, it exits `2`, because the runner may have made the run.
- The wait limit is 30 minutes by default. The `timeout` input sets it. When it passes, the client exits `2` with `surface run: run <id> did not finish within 30m`. The run itself keeps running on the runner, bounded by the run time limit, 30m by default ([limits.md](limits.md)).
- Why poll and not hold: ingresses and load balancers close an idle connection after about 60 seconds by default. A held request needs a timeout tuned on every install, and a dropped connection loses the result.

## Who may read or cancel a run

- `GET /v1/runs/<id>` and `POST /v1/runs/<id>/cancel` need a valid token from a listed owner, and the token's `repository` claim must equal the run's caller `repo`. Any ref of the same repo may read or cancel the run, because anyone with write access to the repo can already read its secrets.
- The runner issues no run token and keeps no extra state. A run result holds response bodies from the target, so an unguessable id alone is not the protection, because ids land in logs.

## Cancel

- When GitHub cancels a job, it sends the step's process `SIGINT`, then `SIGTERM` after 7.5 seconds, then kills it 2.5 seconds later. A composite action has no `post:` step, so the client handles the signal itself.
- On `SIGINT` or `SIGTERM`, the client sends `POST /v1/runs/<id>/cancel` and exits `2`.
- The runner starts no new test for a cancelled run, lets in-flight requests finish or time out, runs the cleanups of the tests whose status check passed ([data-safety.md](data-safety.md)), and sets the run's `state` to `cancelled`.

## The wire shapes

The run request, `POST /v1/runs`:

```json
{
  "target": "https://pr-123.preview.crustdata.com",
  "suites": {
    "surface/api/users.yaml": "<the file's text>",
    "surface/api/search.yaml": "<the file's text>"
  }
}
```

The answer, `202 Accepted`:

```json
{"id": "01j9z3k4m5n6p7q8r9s0t1u2v3"}
```

The poll, `GET /v1/runs/<id>`:

```json
{
  "id": "01j9z3k4m5n6p7q8r9s0t1u2v3",
  "state": "running",
  "caller": {"repo": "crustdata/api", "ref": "refs/pull/123/merge", "sha": "<commit>"},
  "target": "https://pr-123.preview.crustdata.com",
  "suites": [
    {"path": "surface/api/users.yaml", "state": "passed",
     "tests": [{"name": "create-user", "state": "passed", "cleanup": {"state": "passed"}}]},
    {"path": "surface/api/search.yaml", "state": "running",
     "tests": [{"name": "search-person", "state": "failed", "failure": "status: expected 200, got 500\n> POST https://pr-123.preview.crustdata.com/person/search\n…"}]}
  ]
}
```

- A run `state` is `queued`, `running`, `passed`, `failed`, `error`, or `cancelled`. The last four are final. `passed` means every test passed. `failed` means at least one test, suite, or cleanup failed and the run completed. `error` means the runner could not complete the run. `queued` exists so a later queue adds no new state.
- A suite `state` is `pending`, `running`, `passed`, or `failed`. It is `failed` when any test or cleanup in it failed. A test `state` is one of those or `skipped` ([failure-text.md](failure-text.md)). A suite that fails at load ([secrets.md](secrets.md)) is `failed` with its `failure` text and no tests.
- `failure` is present only on a failed or skipped test, or a failed suite. Its text is defined in [failure-text.md](failure-text.md). Secret values in it are already redacted ([secrets.md](secrets.md)).
- `reason` is present only when the run `state` is `error`. It is one line that says why the runner could not complete the run, for example `runner stopped` ([runner.md](runner.md)).
- `cleanup` is present on a test entry only when the test's cleanup ran. It holds `state`, `passed` or `failed`, and on `failed` a `failure` text in the same format ([data-safety.md](data-safety.md)).
- The runner reads the caller from the token, never from the body.
- A finished run stays readable for the retention period, 7 days by default. After that, `GET /v1/runs/<id>` answers `404` ([runner.md](runner.md)).

## The run id

- A run id is 26 lowercase characters from `[0-9a-z]`: a ULID (48 bits of time, then 80 random bits, in Crockford base32) written in lowercase. It needs no library.
- It is `${{ run.id }}` inside suites ([suite-format.md](suite-format.md)). Lowercase keeps it safe inside an email address, a path, and a Kubernetes name.
- It sorts by time, so a results store can list runs in order with no index.

## Exit codes and the CI log

| Exit code | Meaning |
| --- | --- |
| `0` | Every test in every suite passed. |
| `1` | At least one test, suite, or cleanup failed. The run completed. |
| `2` | The run did not complete. The run request was rejected, the token was rejected, the runner returned an error, the run was cancelled, no suites were found, or the wait limit passed. |

A run `state` maps to an exit code: `passed` is `0`, `failed` is `1`, `error` and `cancelled` are `2`.

The log, in this order:

1. One first line with the client version, the run id, and the target.
2. One line per suite, in sorted path order, with its passed, failed, skipped, and cleanup failed counts (skipped and cleanup failed are left out when zero), or its load failure.
3. For each failed or skipped test, the suite path and the test name on one line, then the failure text indented by two spaces ([failure-text.md](failure-text.md)). For each failed cleanup, the same with ` cleanup` after the test name.
4. One last line with the run id and the totals. When the run `state` is `error`, the last line is `run <id>: error: <reason>` instead ([runner.md](runner.md)).

A rejected run request prints the runner's one-line reason, for example `run: target "http://10.0.0.5:8080" is not in the target list` ([targets.md](targets.md)), and exits `2`.

The client also writes:

- **The job summary.** A table with one row per suite (path, passed, failed, skipped, cleanup failed) and a totals row, with the run id, to `$GITHUB_STEP_SUMMARY`. GitHub caps it at 1 MiB per step, so the client truncates and says so.
- **Annotations.** One `::error file=<suite path>,line=<the test's line>::<test name>: <line one of the failure text>` per failed test, so the failure shows on the suite's line in the pull request. The test's line is the line of the test's mapping node in the YAML ([suite-format.md](suite-format.md)). A suite load failure gets `line=1` and no test name. A skipped test gets no annotation. A failed cleanup gets one on its test's line, with the message `<test name> cleanup: <line one>`.

## The action and the client

- The action lives at `action.yml` in this repo, so `uses: rajnandan1/surface@v1` works. It is a composite action (a list of workflow steps in YAML). It downloads the `surface` binary for the job's OS and architecture from the release that matches the action's version, checks its checksum, and runs `surface run`. The action reads its version from the `VERSION` file in its own files ([install.md](install.md)).
- The `surface` command is Go, in this repo. It is one binary with the runner: `surface serve` runs the runner ([install.md](install.md)). It runs in any CI later; only the token fetch is GitHub-specific.
- Inputs: `runner` (required), `target` (required), `suites` (default `surface/`), `timeout` (default `30m`). The action passes them to `surface run --runner <url> --target <url> --timeout <duration> <suites>`.
- `surface check <path>` loads the suites under `<path>` and prints every load error the runner would print before any request: an unknown field, a bad path, a cycle in `needs`, a bad variable name. It prints one line per error with the file and the line, exits `1` on any error, and sends nothing. It cannot check secret group bindings or the target, because those live in the runner's config.
- The runner's HTTP API has three run routes, `POST /v1/runs`, `GET /v1/runs/<id>`, and `POST /v1/runs/<id>/cancel`, plus `GET /healthz`, which needs no token ([runner.md](runner.md)).

## Run request errors

A run request fails, with one line and no run id, when:

- the token is missing, expired, has the wrong audience or issuer, or has a bad signature: `401`,
- the token's `repository_owner_id` is not in `github.owner_ids`: `403`,
- the body is over 16 MiB: `413`,
- the body is not the shape above, or `target` is missing or malformed: `400` ([targets.md](targets.md)). A field the runner does not know is `400`, and the line ends with the runner's version ([install.md](install.md)),
- the target is not in the target list: `403`,
- the runner holds as many runs in memory as its cap allows, or it is stopping: `503` with `Retry-After: 5`. No run was made, and the client sends the run request again ([runner.md](runner.md)).

```
run: token audience "https://other.example.com" is not this runner
run: repo "someone/api" (owner id 98765) may not use this runner
run: target is missing
run: target "http://10.0.0.5:8080" is not in the target list
run: unknown field "targets" (runner 1.1.0)
```

A suite that fails at load does not reject the run. The run starts, that suite is `failed`, and the other suites run ([secrets.md](secrets.md)).

## Known limits

- One repo can hold every run slot, because there is no cap on runs per repo.
- The secret group `callers` and the rule for who may read or cancel a run match names. When one listed owner renames itself, another listed owner can take the old name and match them. The operator trusts every listed owner.
- A fork's pull request cannot start a run. A product that takes outside contributions runs surface on `main` after the merge, or on a branch a maintainer pushes.
- A run keeps running, cleanups included, when the client stops polling without a cancel, for example when the job is killed. The run time limit bounds it ([limits.md](limits.md)).
- The client trusts the `runner` URL it is given. A workflow that points at a wrong URL sends that URL a token with that URL as audience, and nothing else. The token names no secret and grants nothing at GitHub.
