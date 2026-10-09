# Data safety

A test can create, change, and delete data on the target. Tests in a suite run in parallel unless they declare `needs` ([ADR 0003](../adr/0003-tests-run-in-parallel-unless-they-declare-needs.md)), and a test reads the saved values of the tests in its `needs` (see [GLOSSARY.md](../../GLOSSARY.md)). This doc defines what happens to the tests that need a failed test, how a test declares the cleanup of the data it creates, when and in what order cleanups run, what a failed cleanup does, and how two runs on one target stay apart. The decisions come from [How do tests that change data run safely?](https://github.com/rajnandan1/surface/issues/10) and [ADR 0009](../adr/0009-a-test-declares-the-cleanup-of-the-data-it-creates.md).

## Example

```yaml
tests:
  - name: create-org
    method: POST
    path: /orgs
    json: {"name": "org-${{ run.id }}"}        # run.id keeps two runs apart.
    expect:
      status: 201
    save:
      org_id: body.id
    cleanup:                                   # Removes what this test created.
      method: DELETE
      path: /orgs/${{ needs.create-org.org_id }}   # A cleanup reads its own test's saved values.
      expect:
        status: 204

  - name: create-user
    needs: [create-org]
    method: POST
    path: /orgs/${{ needs.create-org.org_id }}/users
    form:
      email: u-${{ run.id }}@example.com
    expect:
      status: 201
    save:
      user_id: body.data.id
    cleanup:
      method: DELETE
      path: /orgs/${{ needs.create-org.org_id }}/users/${{ needs.create-user.user_id }}
      expect:
        status: 204

  - name: update-user
    needs: [create-user]
    method: PATCH
    path: /users/${{ needs.create-user.user_id }}
    json: {"note": "updated by ${{ run.id }}"}
    expect:
      status: 200
```

When every test passes, the runner sends the three requests in order, then `DELETE` the user, then `DELETE` the org. The CI log shows `surface/api/users.yaml: 3 passed`.

When `create-user` gets a `500`:

```
surface/api/users.yaml: 1 passed, 1 failed, 1 skipped

surface/api/users.yaml create-user
  status: expected 201, got 500
  > POST https://pr-123.preview.crustdata.com/orgs/41/users
  …

surface/api/users.yaml update-user
  skipped: needs create-user, which failed
```

`update-user` is skipped. The cleanup of `create-user` does not run, because its status check failed. The cleanup of `create-org` runs, because `create-org` passed, so the org does not leak.

When every test passes and the org's `DELETE` gets a `500`:

```
surface/api/users.yaml: 3 passed, 1 cleanup failed

surface/api/users.yaml create-org cleanup
  status: expected 204, got 500
  > DELETE https://pr-123.preview.crustdata.com/orgs/41
  > User-Agent: surface/1.2.0
  < 500 Internal Server Error in 12ms     ✗ expected 204
  < Content-Type: application/json
  <
  < {
  <   "error": "org has members"
  < }
```

The suite is `failed`, and the CI job exits `1`.

## When a test fails

- A test fails when any check fails, when it gets no response, or when a `save` path finds nothing ([failure-text.md](failure-text.md)).
- Every test that lists a failed test in `needs` is `skipped`, with the failure text `skipped: needs create-user, which failed`. Every test that lists a skipped test in `needs` is `skipped`, with `skipped: needs update-user, which was skipped`. So one failure skips the whole chain after it.
- This applies when the dependent reads no saved value. `needs` also means "the target is in the state that test left", and a failed test did not leave it.
- A skipped test sends no request, and its cleanup does not run.
- The suite is `failed`, because the test that caused the skips failed. The suite line counts the skipped tests ([failure-text.md](failure-text.md)).
- Why skip and not run: a dependent that runs after a failed test produces a second failure, and that failure hides the first. The author wants the first.

## Cleanup

- A test may carry `cleanup:`. It holds one request and its `expect`: the fields `method`, `path`, `query`, `headers`, `json`, `form`, `body`, `timeout`, and `expect`, with the same rules as a test ([suite-format.md](suite-format.md)). `expect.status` is required.
- A cleanup has no `name`, `needs`, `save`, or `cleanup`.
- Its `headers` merge with `defaults.headers`, and its `timeout` falls back to `defaults.timeout`, then to 30s, like a test's.
- A cleanup reads the saved values of its own test and of the tests in its test's `needs`, with the same syntax: `${{ needs.create-user.user_id }}` inside the cleanup of `create-user`. It cannot read any other test. The three name kinds of the suite format stay.
- A test whose purpose is to delete data declares no cleanup.
- Why the cleanup is on the test: the test that creates a thing says how to remove it, like `defer` and `t.Cleanup` in Go, and the order below comes for free. [ADR 0009](../adr/0009-a-test-declares-the-cleanup-of-the-data-it-creates.md) records the choice and the rejected options.

## When a cleanup runs

- A cleanup runs when its test's `expect.status` check passed. The status is the target's own word that it did the thing. The body and header checks say whether the response had the right shape, not whether the data exists.
- So a test that got `201` with a wrong body value fails, and its cleanup still runs. A test that got `400` or `500` fails, and its cleanup does not run.
- A cleanup does not run, and the runner prints nothing for it, when:
  - the test's status check failed,
  - the test got no response,
  - the test was skipped, or
  - a saved value the cleanup reads was not found. The test's own failure text already says `save user_id: body.data.id finds nothing`.
- Known limit: a target that creates the data and answers with a status the test did not expect leaks that data. The author fixes `expect.status`.

## Order

- After every test in a suite has ended (passed, failed, or skipped), the runner sends the suite's cleanups one at a time, in the reverse order in which their tests completed: last created, first deleted. So the user inside the org is deleted before the org.
- Two tests with no `needs` between them complete in any order, so their cleanups run in any order. That is safe, because the data they created is independent.
- A suite's data lives until the suite's tests end. A test without `needs` that reaches the data through a `run.id` key finds it.
- Each suite cleans up on its own, as soon as its own tests end, at the same time as other suites.
- A cleanup has no `needs`. Its order comes from the completion order only.

## Cancel

- On `POST /v1/runs/<id>/cancel`, the runner starts no new test, lets in-flight requests finish or time out, runs the cleanups of the tests whose status check passed, in the order above, and then sets the run's `state` to `cancelled` ([runs.md](runs.md)).
- The cancel adds at most one cleanup request, bounded by its timeout, per test that created data. Cleanups may start up to 10 minutes after the run time limit. A cleanup that has not started by then is a failed cleanup with the failure text `not sent: run time limit passed` ([limits.md](limits.md)).
- When the client stops polling without a cancel, for example when the job is killed, the run keeps running, cleanups included.
- When the runner stops on SIGTERM, for example at a deploy, it cancels the runs in flight the same way, so the cleanups run, and the run's `state` becomes `error` with the reason `runner stopped` ([runner.md](runner.md)).
- Known limit: when the runner crashes, the cleanups do not run. The runner marks the run `error` at its next start, and the data that is left carries the run id.

## A failed cleanup

- A cleanup fails when any of its checks fails or when it gets no response, with the same rules as a test. It also fails when it has not started 10 minutes after the run time limit, with the one-line failure text `not sent: run time limit passed` ([limits.md](limits.md)).
- A failed cleanup makes the suite `failed` and the run `failed`, so the CI job exits `1`. The test's own `state` does not change: a test can be `passed` with a failed cleanup.
- In `GET /v1/runs/<id>`, the test entry gets a `cleanup` object, present only when the cleanup ran: `{"state": "passed"}` or `{"state": "failed", "failure": "<text>"}` ([runs.md](runs.md)).
- The failure text has the same format as a test's: line one, the request as sent, the response with the marks ([failure-text.md](failure-text.md)). Secret values in it are redacted ([secrets.md](secrets.md)).
- The CI log prints a failed cleanup in the failures section, under the line `<suite path> <test name> cleanup`, with the failure text indented by two spaces.
- The suite line and the last line of the log add `, N cleanup failed` after the skipped count, left out when N is zero. The passed, failed, and skipped counts count tests only.
- The job summary table gets a `cleanup failed` column.
- The GitHub annotation of a failed cleanup sits on its test's line in the suite, with the message `<test name> cleanup: <line one>`.
- Why a failed cleanup fails the suite: a broken `DELETE` would otherwise pass CI, and the data would grow until someone looked. One exit code for "something is wrong" is simpler for the author than a warning channel.

## Collisions between runs

- Two runs can call one target at the same time: two pull requests, or a matrix job. The runner does not serialize runs per target, and it does not lock data. Parallel runs on one preview target are the main use of surface.
- Every value that must be unique on the target carries `${{ run.id }}`: an email address, a name, a slug. The run id is 26 lowercase characters from `[0-9a-z]`, so it is safe inside an email address, a path, and a Kubernetes name ([runs.md](runs.md)).
- One suite that creates two of the same thing adds a literal suffix: `u-${{ run.id }}-a@example.com` and `u-${{ run.id }}-b@example.com`. Every suite in a run shares the same `run.id`, so two suites that create the same kind of thing also use different literal text.
- v1 adds no variable for this. `${{ test.name }}` was considered and rejected: it changes the suite format, and a test name is unique only inside its suite, so two suites with a `create-user` test would still clash.
- Two tests in one suite that change the same data, and do not declare `needs`, can run at the same time ([ADR 0003](../adr/0003-tests-run-in-parallel-unless-they-declare-needs.md)). The author adds `needs` to order them.
- Known limit: data left behind by a cancelled, killed, or crashed run carries the run id in the values the suite put it in. Sweeping it is the target's job. Surface has no janitor.

## Decided elsewhere

- `needs`, `save`, `run.id`, and the fields of a test: [suite-format.md](suite-format.md)
- The failure text, and the one line of a skipped test: [failure-text.md](failure-text.md)
- The cancel flow, the run and suite states, the wire shapes, the counts, the job summary, and the annotations: [runs.md](runs.md)
- Redaction: [secrets.md](secrets.md)
- What a runner restart does to a run in flight: [runner.md](runner.md)
- The run time limit and the request limits: [limits.md](limits.md)
- Whether a failed test runs again, and whether its cleanup runs between tries: the map's "Not yet specified"
