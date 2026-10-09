# Secret groups live in Kubernetes Secrets, and each group is bound to callers and targets

Secret values live in Kubernetes Secrets in the runner's namespace, one per secret group, and the runner reads them from the Kubernetes API when a run starts. The CI job sends no secret values. The operator binds each group, in the runner's config, to the callers (repo and ref) that may use it and to the targets its values may be sent to. A suite is untrusted input ([ADR 0001](0001-suites-are-untrusted-input.md)) and chooses its own target, so without the binding any suite author could send a production token to a URL they control.

## Considered options

- **The CI job sends the values with the run request.** Rejected. A scheduled run has no CI job, and the map's givens say the secrets design must not block scheduled runs. It also sends values over the network on every run and needs a second redaction path.
- **Both: Kubernetes Secrets, plus values the CI job sends for one run.** Rejected. Two sources of truth and two redaction paths. An ephemeral environment gets a group of its own, for example `preview`, whose values work on every ephemeral environment.
- **The runner reads Vault or a cloud store directly.** Rejected. It puts store-specific code into the open-source core. An organization with such a store fills the Kubernetes Secrets with the External Secrets Operator.
- **Bind a group to callers only, or to targets only.** Rejected. Callers only does not stop an allowed caller from sending values to their own URL. Targets only lets a pull request in any repo use the production group against production.
- **Bindings as labels and annotations on each Kubernetes Secret.** Rejected. Annotations hold flat text, so the repo and ref lists need a mini-format. The runner config is YAML in git.

Sources: [the research note](https://github.com/rajnandan1/surface/blob/research/secrets-for-workloads/docs/research/secrets-for-workloads.md) and the resolution of [Where do secrets live, and how does a suite declare the secrets it needs?](https://github.com/rajnandan1/surface/issues/6).

## Consequences

- The operator, not the suite author, creates a group and decides who may use it. A new group is a config change plus a new Kubernetes Secret.
- The runner needs `get` on Secrets in its namespace, its own namespace, and encryption at rest. These are install steps.
- The runner must learn the caller's repo and ref. [How does a CI job start a run and get the result?](https://github.com/rajnandan1/surface/issues/8) decides how.
- The runner replaces every secret value in its output with the reference text `${{ secrets.<group>.<name> }}`. The design is in [docs/design/secrets.md](../design/secrets.md).
