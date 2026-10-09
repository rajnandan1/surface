# Surface

Surface runs end-to-end tests that live in product repos, outside the product's CI, at high parallelism. It reports what failed and why.

## Language

**Suite**:
One YAML file in a product repo that holds many tests.
_Avoid_: Spec, spec file, test file

**Test**:
One HTTP request plus the response it must get back.
_Avoid_: Case, check, spec

**Runner**:
The surface service that receives suites and runs their tests.
_Avoid_: Executor, worker, engine

**Run**:
One execution of a set of suites, started by one CI job.
_Avoid_: Job, execution, build

**Run request**:
The message a CI job sends to start a run. It names the target and carries the suites.
_Avoid_: Job request, submission, payload

**Run result**:
The record the runner keeps for one run: the run's state and the states and failure texts of its suites and tests. The client reads it while it waits, and the operator can read it after.
_Avoid_: Report, results, output, run record

**Client**:
The surface command that runs inside a CI job, sends the run request, and waits for the result.
_Avoid_: CLI, action, agent

**Target**:
The base URL a run calls, such as production or an ephemeral environment. The CI job sets it when it starts the run.
_Avoid_: Host, environment, base URL

**Target list**:
The list in the runner's config of the targets the runner may call. The runner calls no other target.
_Avoid_: Allow-list, whitelist, host list

**Saved value**:
A value a test takes from its response, so later tests in the same suite can use it.
_Avoid_: Capture, output, variable

**Cleanup**:
The request a test declares to remove the data it created. The runner sends it after every test in the suite has ended.
_Avoid_: Teardown, after hook, finalizer, rollback

**Match mode**:
How strictly a test compares a response body with its example body: by values, by types, or by keys only.
_Avoid_: Match type, strictness

**Check**:
One comparison a test makes on its response: the status, one header, one key or item of the example body, one `equal` path, or one `save` path. A test fails when any check fails.
_Avoid_: Assertion, expectation, matcher

**Failure text**:
The text the runner records for a failed or skipped test: the first failed check on line one, then the request and the response with the failed checks marked. The client prints it in the CI log.
_Avoid_: Error message, report, diff

**Secret group**:
A named set of secrets that the operator creates and that a suite reads by name.
_Avoid_: Context, environment, secret set, vault

**Secret**:
One named value inside a secret group, such as an API token.
_Avoid_: Credential, env var, variable

**Caller**:
The source of a run: a repo and a git ref (a branch, a tag, or a pull request's merge ref), or later a schedule.
_Avoid_: Identity, principal, source

**Owner**:
The GitHub organization or user account that holds a repo. Only callers whose owner is on the runner's list may use the runner.
_Avoid_: Org, organization, account, namespace

**Operator**:
The person or team that installs surface in their own cluster and decides which callers may use which secret groups.
_Avoid_: Admin, platform team, SRE

**Suite author**:
The developer who writes a suite in a product repo.
_Avoid_: Developer, tester, user
