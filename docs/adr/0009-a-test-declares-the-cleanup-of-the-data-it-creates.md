# A test declares the cleanup of the data it creates, and cleanups run after the suite, last created first deleted

A test that creates data on the target carries a `cleanup:` block: one request, with its own `expect`, that removes what the test created. After every test in a suite has ended, the runner sends the suite's cleanups one at a time, in the reverse order in which their tests completed. A cleanup runs when its test's `expect.status` check passed, so a test that got `201` with a wrong body is still cleaned up, and a test that got `500` is not. This copies `defer` and `t.Cleanup` in Go: the code that creates a thing says how to remove it, and reverse order comes for free.

## Considered options

- **A flag `cleanup: true` on an ordinary test, ordered with `needs`.** Rejected. A cleanup that lists another cleanup in `needs` is skipped when that one is skipped. So when `create-user` fails, `delete-user` is skipped, `delete-org` is skipped, and the org leaks.
- **No cleanup in v1; an ordinary test at the end deletes the data.** Rejected. That test is skipped when any test before it fails, which is the common case in a pull request, so data leaks on every failure.
- **Run the cleanup only when the test passed.** Rejected. A `201` with a wrong body value leaves the record behind.

## Consequences

- A failed cleanup fails the suite and the run, and the CI job exits `1`. A broken `DELETE` cannot pass CI.
- A cleanup reads saved values with the existing `${{ needs.<test>.<value> }}` syntax. The suite format gets no new name kind.
- A suite's data lives until its last test ends, because the cleanups start after that.
- On cancel, the runner still runs the cleanups of the tests whose status check passed, so a cancel takes longer by at most one cleanup timeout per such test.
