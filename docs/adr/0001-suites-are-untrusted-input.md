# A suite is untrusted input

The runner holds the secrets of every product repo, and a suite can come from any of those repos. So a suite can only send HTTP requests and read the secret groups it declares. A suite cannot run shell commands, read or write files on the runner, read the runner's environment variables, or load plugins.

## Consequences

- Every API test tool we read (Venom, runn, scenarigo) has at least one of these features, so contributors will ask for one. Adding one lets a suite read the secrets of every other repo.
- When a library in the runner reads suite data, the runner turns off that library's file, network, and environment access. For example, a JSON Schema check must not download a schema from a `$ref` URL.
- Which hosts a test may call is a separate decision: [ADR 0006](0006-ci-job-sends-the-target-and-runner-calls-only-listed-targets.md).
