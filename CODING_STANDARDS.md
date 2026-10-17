# Coding Standards

## Code Style

- Go 1.26 with current idioms: `errors.AsType`, `wg.Go`, `cmp.Or`, `strings.SplitSeq`, method patterns in `http.ServeMux`. `go fix -diff ./...` prints nothing.
- The standard library first.
- An error is one line of plain English that starts with where it came from. A rejected unknown field names the runner version.
- `surface run` exits 0 when the run passed, 1 when it failed, and 2 on every other end: error, cancel, timeout, bad usage.
- A comment states only what the code cannot show, in one line.
- A `Suite` or `Test` field that `suite.Load` fills in carries `yaml:"-"`, so a suite that sets it fails to load.
- yaml.v3 prints Go type names in its errors (`cannot unmarshal !!seq into suite.Defaults`), so renaming a type in `internal/suite` changes text users see.

Example from `internal/runner/secrets.go`:

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

## Tests

- A test name is a sentence that states the behavior: `TestARunPastItsRunTimeLimitEndsInErrorAndItsCleanupRuns`.
- Tests that run the client against a real runner live in `internal/runner` and call `client.Run`. They set `client.PollInterval` low and restore it.
- Each package's tests carry their own small helpers (`header`, `usersSuite`, `writeSuite`, `fakeGitHubJob`). Copy a helper into the package that needs it.
