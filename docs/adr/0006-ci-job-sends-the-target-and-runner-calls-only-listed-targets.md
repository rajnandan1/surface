# The CI job sends the target, and the runner calls only targets on its list

A run has one target, and the CI job sends it in the run request. The runner keeps a target list in its config and calls nothing else: it checks the run's target against the list before any request, and it never follows a redirect. A suite is untrusted input ([ADR 0001](0001-suites-are-untrusted-input.md)) and the runner runs inside the cluster, so without the list a run that uses no secrets could call the cloud metadata address `169.254.169.254`, the Kubernetes API, or any in-cluster service.

## Considered options

- **The suite holds a default target, and the CI job overrides it.** Rejected. A suite is untrusted, so its URL carries no authority, and the CI job must send the ephemeral URL anyway. That is two sources for one value.
- **The runner config names targets, and the CI job sends a name.** Rejected. An ephemeral environment has a new URL per pull request, so the CI job must be able to send a URL. A name is a second shape for the same value.
- **A config file in the product repo lists environments.** Rejected, for the same reason as the suite.
- **Several targets per run.** Rejected for v1. One target keeps the run request and the test format as they are. A product with two services runs two runs. A `target:` field on a test can be added later without a format break.
- **The union of the secret groups' `targets` as the list.** Rejected. A public target that uses no secrets would need a dummy group.
- **A deny-list of private and link-local IP ranges instead of a list.** Rejected. It misses in-cluster DNS names, and it needs the runner to resolve every name before every request.
- **Follow a redirect when its `Location` host is on the list.** Rejected. Not following is simpler, and an API test usually wants to assert the redirect itself.
- **Each list entry binds callers, like a secret group.** Rejected for v1. It doubles the binding config. The secret group binding limits secret use per caller, and the target's own authentication limits what a run without secrets can change.

Sources: the resolution of [How does a run get its target URL?](https://github.com/rajnandan1/surface/issues/7).

## Consequences

- The operator lists every host surface may call. A new environment domain is a config change.
- The CI workflow for main hardcodes the production URL in one line. The workflow for a pull request passes the ephemeral URL it just deployed.
- The run request has a `target` field. [How does a CI job start a run and get the result?](https://github.com/rajnandan1/surface/issues/8) decides the transport.
- A test that expects a redirect asserts the `3xx` and the `Location` header itself.
- The design is in [docs/design/targets.md](../design/targets.md).
