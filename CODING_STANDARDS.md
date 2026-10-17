# Coding Standards

## Go

- Go 1.26 with current idioms: `errors.AsType`, `wg.Go`, `cmp.Or`, `strings.SplitSeq`, method patterns in `http.ServeMux`. `go fix -diff ./...` prints nothing.
- The standard library first.
- A comment states only what the code cannot show, in one line.

## Errors and exit codes

- An error is one line of plain English that starts with where it came from.
- A rejected unknown field names the runner version.
- `surface run` exit codes follow `docs/design/runs.md#exit-codes-and-the-ci-log`. Bad usage also exits `2`.

Example of the error rule, from `internal/runner/secrets.go`:

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

## Suite types

- A `Suite` or `Test` field that `suite.Load` fills in carries `yaml:"-"`, so a suite that sets it fails to load.
- yaml.v3 prints Go type names in its errors (`cannot unmarshal !!seq into suite.Defaults`), so renaming a type in `internal/suite` changes text users see.

## Tests

- A test name is a sentence that states the behavior: `TestARunPastItsRunTimeLimitEndsInErrorAndItsCleanupRuns`.
- Tests that run the client against a real runner live in `internal/runner` and call `client.Run`. They set `client.PollInterval` low and restore it.
- Each package's tests carry their own small helpers (`header`, `usersSuite`, `writeSuite`, `fakeGitHubJob`). Copy a helper into the package that needs it.
