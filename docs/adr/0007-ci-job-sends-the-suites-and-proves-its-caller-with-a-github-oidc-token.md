# The CI job sends the suites and proves its caller with a GitHub OIDC token

The CI job sends the suite files inside the run request, and it proves who it is with a GitHub OIDC token (a signed token GitHub gives a job on request, naming the repo, the ref, the commit, and the workflow) on every request to the runner. The runner reads the caller's `repo` and `ref` from the token's claims and never from the request body, fetches no repo, and holds no GitHub credential. The CI job stores no API key for surface. The client is a Go `surface` command, wrapped by a composite GitHub Action, and it polls for the result instead of holding one request open.

## Considered options

- **The runner fetches the repo at the token's commit.** Rejected. The runner would hold a GitHub App or a token that reads every product repo: a second credential for the operator to install and rotate, and it fails on a private repo the App is not installed on. The CI job already has the checkout.
- **A shared bearer token per repo, stored as a GitHub secret.** Rejected. It is one more secret to rotate, and it proves nothing about the ref, so the secret group binding on `ref` ([ADR 0005](0005-secret-groups-in-kubernetes-secrets.md)) would be meaningless.
- **One request held open until the run ends.** Rejected. Ingresses and load balancers close an idle connection after about 60 seconds by default, so every operator would tune a timeout, and a dropped connection loses the result.
- **A run token issued with the `202` answer, for polling.** Rejected. The same OIDC check on every request needs no new state. A run is readable by any ref of its caller's repo.
- **A JavaScript or Docker action as the client.** Rejected. A JavaScript action adds a second language to a Go repo, a Docker action pulls an image per job, and both work on GitHub only. The Go command runs in any CI later; only the token fetch is GitHub-specific.

Sources: [the research note](https://github.com/rajnandan1/surface/blob/research/secrets-for-workloads/docs/research/secrets-for-workloads.md) and the resolution of [How does a CI job start a run and get the result?](https://github.com/rajnandan1/surface/issues/8).

## Consequences

- A product workflow adds `permissions: id-token: write`. A pull request from a fork gets no token, so it cannot start a run.
- The operator sets the runner's own `url` in its config, and the token's audience must equal it. v1 trusts GitHub only; another CI vendor later is another issuer block.
- The runner caps the run request at 16 MiB, because it carries the files.
- The release pipeline builds a `surface` binary per OS and architecture with checksums, because the action downloads it.
- The design is in [docs/design/runs.md](../design/runs.md).
