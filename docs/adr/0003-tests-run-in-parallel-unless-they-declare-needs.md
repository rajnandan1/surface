# Tests in a suite run in parallel unless they declare `needs`

The runner starts all tests of a suite at the same time. A test that must wait for another test lists that test in `needs`. Only then can it read that test's saved values, with `${{ needs.<test>.<value> }}`. Surface exists to run tests with more parallelism than CI gives, so order is the exception that a suite marks, not the default.

## Considered options

- **One ordered list in file order**, as in Venom, runn, and scenarigo. Rejected. Parallelism then comes only from many suites at the same time, so one long suite is slow.
- **File order unless a test is marked `parallel: true`.** Rejected. The slow path is the default, and authors forget the mark.

## Consequences

- A reader who knows Venom, runn, or scenarigo expects file order. Two tests that change the same data, and do not declare `needs`, can run at the same time.
- A cycle in `needs` is a load error. A `${{ needs.<test>.<value> }}` that names a test missing from `needs` is a load error.
- A test whose `needs` test failed or was skipped is skipped ([data-safety.md](../design/data-safety.md)). Cleanup is [ADR 0009](0009-a-test-declares-the-cleanup-of-the-data-it-creates.md).
