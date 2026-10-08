# How Kubernetes and CI systems give secrets to workloads

## Answer

Secrets reach a workload in one of two ways: the CI job sends the secret values with its request, or the workload reads the values from a store that it can reach itself. A third mechanism, OIDC (OpenID Connect, a standard way for one service to prove its identity to another service with a signed token), sends no secret value. It proves which repo, branch, and workflow started the CI job, so that a service can decide which secrets that job may use.

For surface, three patterns fit:

1. The runner reads one Kubernetes Secret per secret group from the Kubernetes API when a run starts. A Kubernetes Secret is a Kubernetes object that holds a small amount of sensitive data. A secret group is a named set of secrets that a suite refers to by its name. This pattern works in any cluster, and it also works for a scheduled run.
2. An organization that keeps its secrets in an outside store (for example, HashiCorp Vault or AWS Secrets Manager) copies them into those Kubernetes Secrets with the External Secrets Operator. The External Secrets Operator is an open-source Kubernetes controller that does this copy. Surface needs no code for this.
3. The CI job sends a GitHub OIDC token with the run request. The runner checks the token, and then allows only the secret groups that the operator bound to that repo and branch.

The pattern "the CI job sends the secret values" is the simplest for v1. But it does not work for a scheduled run, because a scheduled run has no CI job. The details and the sources are below.

This file answers issue #2 ("How do Kubernetes and CI systems give secrets to workloads?"). The map is issue #1. Issue #6 ("Where do secrets live, and how does a suite declare the secrets it needs?") makes the choice. This file does not make it.

## Terms

- **Suite author**: the developer who writes a suite in a product repo.
- **Surface operator**: the person or team who installs surface and runs it in their own Kubernetes cluster.
- **CI job**: one job in a CI (continuous integration) system, for example GitHub Actions or CircleCI. In v1, a CI job starts each run.
- **Secret group**: a named set of secrets. A suite refers to a secret group by its name. This file uses this name for the idea "named secret groups" from the map's idea note.
- **Pod**: the smallest unit that Kubernetes runs. A Pod holds one or more containers.
- **Kubernetes Secret**: a Kubernetes object that holds a small amount of sensitive data, for example a password, a token, or a key ([K8s Secrets][k8s-secret]).
- **Namespace**: a named partition of a Kubernetes cluster. Most access rules apply per namespace.
- **RBAC** (role-based access control): the Kubernetes system that says which identity can do which action on which object ([K8s RBAC][k8s-rbac]).
- **ServiceAccount**: a Kubernetes identity for a Pod. A Pod uses it to call the Kubernetes API ([K8s service accounts][k8s-sa]).
- **etcd**: the database where the Kubernetes API server stores all objects, Secrets included.
- **ESO** (External Secrets Operator): an open-source Kubernetes controller. It copies secrets from an outside store into Kubernetes Secrets ([ESO overview][eso-overview]).
- **Vault**: HashiCorp Vault, a secret store that runs as its own service.
- **OIDC** (OpenID Connect): a standard way for one service to prove an identity to another service with a signed token.
- **JWT** (JSON Web Token): the signed token format that OIDC uses. A JWT carries **claims**, which are named fields such as `repository` or `ref`.

## Kubernetes Secrets

**Storage.** Kubernetes stores Secrets unencrypted in etcd by default ([K8s Secrets][k8s-secret]). The API server writes plain text to etcd unless an operator turns on encryption at rest ([K8s encryption at rest][k8s-encrypt]). Kubernetes encodes Secret values in base64, and base64 is not encryption ([K8s Secrets good practices][k8s-good]). One Secret can hold at most 1 MiB ([K8s Secrets][k8s-secret]).

**Who can read a Secret.** Anyone who can create a Pod in a namespace can read every Secret in that namespace ([K8s Secrets][k8s-secret]). The permission to `list` or `watch` Secrets lets the holder read all Secret data in the namespace ([K8s Secrets][k8s-secret]). Kubernetes recommends that a component gets `get` access to Secrets only if its normal work needs it ([K8s Secrets good practices][k8s-good]). Kubernetes also recommends separate namespaces to isolate access to Secrets ([K8s Secrets good practices][k8s-good]).

**Three ways a Pod uses a Secret.**

- `env[].valueFrom.secretKeyRef` puts one key of a Secret into one environment variable of a container ([K8s inject Secrets][k8s-task]).
- `envFrom[].secretRef` puts every key of a Secret into environment variables. Each key becomes the variable name ([K8s inject Secrets][k8s-task]). If a key is not a valid variable name, Kubernetes skips that key, and the Pod still starts ([K8s Secrets][k8s-secret]).
- A `secret` volume makes each key a file inside the container ([K8s inject Secrets][k8s-task]).

**Updates.** A container does not see a changed Secret in an environment variable until the container restarts ([K8s inject Secrets][k8s-task]). A volume gets the new value with an eventually-consistent delay. The delay can be as long as the kubelet sync period plus the cache delay. A `subPath` mount never gets the update ([K8s Secrets][k8s-secret]).

**Missing Secrets.** By default, no container of a Pod starts until all Secrets that the Pod references exist. A reference with `optional: true` lets the Pod start without that Secret. If a required Secret exists but does not have the named key, the Pod fails at startup ([K8s Secrets][k8s-secret]).

**Reading a Secret from the API.** A Pod does not have to receive a Secret through `env` or a volume. Other parts of the system can use a Secret without exposing it to the Pod ([K8s Secrets][k8s-secret]). Kubernetes names "read-only access to sensitive information stored in Secrets" as a use of a ServiceAccount ([K8s service accounts][k8s-sa]). An RBAC rule can use `resourceNames` to allow `get` on named Secrets only. A client that has `list` or `watch` limited by name must send a `metadata.name` field selector ([K8s RBAC][k8s-rbac]).

**Outside stores without ESO.** The Secrets Store CSI Driver runs on every node. It lets the kubelet fetch secrets from an outside store and mount them as a volume into the Pods that you allow ([K8s Secrets good practices][k8s-good]). CSI (Container Storage Interface) is the Kubernetes plugin interface for volumes.

## External Secrets Operator and Vault

**How ESO works.** The ESO controller fetches secrets from an outside API and creates Kubernetes Secrets. When the outside secret changes, the controller updates the Kubernetes Secret ([ESO overview][eso-overview]). A `SecretStore` says how to reach the outside store, and it is namespaced. An `ExternalSecret` says what to fetch. A `ClusterSecretStore` is a `SecretStore` that all namespaces can use ([ESO overview][eso-overview]).

**Refresh.** The default refresh policy is `Periodic`. With it, ESO updates the Kubernetes Secret at each `refreshInterval` ([ESO ExternalSecret][eso-es]). The documented example uses `1h0m0s`.

**Cost and risk.** ESO runs with elevated privileges. It can create, read, and update Secrets in all namespaces ([ESO overview][eso-overview]). With one shared `ClusterSecretStore`, every application developer can reach every secret, and ESO has no mechanism to limit keys per namespace ([ESO multi-tenancy][eso-multitenancy]). ESO's Vault provider supports only the Vault KV (key-value) engine. It can log in to Vault with a token, AppRole, Kubernetes, LDAP, user and password, JWT/OIDC, AWS, or a TLS certificate ([ESO Vault provider][eso-vault]).

**Vault Kubernetes auth.** A Pod can log in to Vault with its Kubernetes ServiceAccount token ([Vault Kubernetes auth][vault-k8s-auth]). A Vault role binds ServiceAccount names and namespaces to Vault policies. The documented example allows the `myapp` ServiceAccount in the `default` namespace ([Vault Kubernetes auth][vault-k8s-auth]).

**Vault JWT/OIDC auth.** Vault checks a JWT signature with a JWKS (JSON Web Key Set) URL or an OIDC discovery URL. Then Vault checks the role's `bound_audiences`, `bound_subject`, and `bound_claims` against the token's claims ([Vault JWT/OIDC auth][vault-jwt]). For GitHub Actions, GitHub documents `bound_issuer` and `oidc_discovery_url` as `https://token.actions.githubusercontent.com`. GitHub's example role binds the `repository` claim. GitHub says the role must have at least one condition ([GitHub OIDC in Vault][gh-oidc-vault]).

**Three Vault delivery tools for Kubernetes.**

- The Vault Agent Injector is a mutating webhook. It adds an init container and a sidecar container to a Pod that has the annotation `vault.hashicorp.com/agent-inject: true`. These containers write the secrets to a shared memory volume at `/vault/secrets` ([Vault Agent Injector][vault-agent-inj]).
- The Vault Secrets Operator (VSO) syncs Vault secrets into Kubernetes Secrets ([Vault Secrets Operator][vault-vso]). These Secrets live in etcd by default ([Vault Kubernetes comparison][vault-k8s-compare]).
- The Vault CSI provider mounts secrets as a volume. The secret lives as long as the Pod lives ([Vault Kubernetes comparison][vault-k8s-compare]).

HashiCorp says the Agent Injector puts the highest load on Vault, because each Pod opens its own connection and runs a sidecar ([Vault Kubernetes comparison][vault-k8s-compare]).

## GitHub Actions secrets and environments

**Levels.** GitHub stores secrets at the organization, repository, and environment level ([GitHub secrets][gh-secrets-concept]). If two levels have a secret with the same name, the lowest level wins: environment, then repository, then organization ([GitHub secrets reference][gh-secrets-ref]). A workflow can read a secret only if the workflow names it explicitly ([GitHub secrets][gh-secrets-concept]).

**Limits.** GitHub allows 1,000 organization secrets, 100 repository secrets, and 100 environment secrets. Each secret holds at most 48 KB. A workflow can use only the first 100 organization secrets, in alphabetical order ([GitHub secrets reference][gh-secrets-ref]).

**When GitHub withholds secrets.** GitHub gives no secrets except `GITHUB_TOKEN` to a workflow that a pull request from a fork starts. GitHub gives no secrets to a workflow that a Dependabot event starts ([GitHub use secrets][gh-secrets-howto]).

**Who can read them.** Any user with write access to a repository can read all secrets of that repository ([GitHub secure use][gh-hardening]). GitHub hides secret values in GitHub's own workflow logs, but GitHub does not guarantee this ([GitHub secrets][gh-secrets-concept]). GitHub tells users not to put structured data such as JSON or YAML in one secret, because the log redaction can then fail ([GitHub secure use][gh-hardening]).

**Environments.** An environment is a named deploy target in a GitHub repository, with its own secrets and rules. Only a job that references the environment can read its secrets ([GitHub environments][gh-env]). An environment can require a reviewer's approval before the job reads its secrets. An environment can also allow only some branches or tags ([GitHub environments][gh-env]). On GitHub Free, environment secrets work only in public repositories ([GitHub environments][gh-env]).

**Schedules in GitHub.** The GitHub `schedule` event starts a workflow at a set time, at most once every 5 minutes. It runs on the latest commit of the default branch, and GitHub can delay it under high load ([GitHub events][gh-events]).

## GitHub OIDC tokens

**What the CI job gets.** Each GitHub Actions job can ask GitHub's OIDC provider for a JWT. This JWT is unique to the job ([GitHub OIDC][gh-oidc]). The workflow must grant `permissions: id-token: write`, or GitHub does not issue the token ([GitHub OIDC reference][gh-oidc-ref]). The issuer is `https://token.actions.githubusercontent.com`. Its discovery document is at `https://token.actions.githubusercontent.com/.well-known/openid-configuration` ([GitHub OIDC reference][gh-oidc-ref]).

**How the service checks the repo, branch, and workflow.** The service checks the JWT signature against the issuer's keys, which it gets from a JWKS URL or the OIDC discovery URL ([Vault JWT/OIDC auth][vault-jwt]). Then it compares the claims with its own trust rules ([GitHub OIDC][gh-oidc]). These claims identify the source ([GitHub OIDC reference][gh-oidc-ref]):

- `repository` and `repository_id` identify the repo. `repository_owner_id` identifies the organization.
- `ref` gives the git ref, for example `refs/heads/main`.
- `workflow_ref` gives the workflow file and its ref. `job_workflow_ref` gives the reusable workflow file, if the job uses one.
- `environment` gives the GitHub environment, if the job references one.
- `event_name` gives the trigger. Dependabot jobs get the value `dynamic`.
- `aud` (audience) defaults to the URL of the repository owner. A job can ask for a custom audience with `core.getIDToken(audience)`.

**The `sub` claim.** The `sub` (subject) claim joins some of these values into one string. Its form depends on the trigger. For a job with an environment, it is `repo:ORG/REPO:environment:NAME`. For a pull request with no environment, it is `repo:ORG/REPO:pull_request`. For a branch, it is `repo:ORG/REPO:ref:refs/heads/BRANCH`. The branch form appears only when the job has no environment and no pull request started it ([GitHub OIDC reference][gh-oidc-ref]).

**A format change in 2026.** Repositories created after July 15, 2026 use an immutable `sub` format with owner and repo IDs, for example `repo:octo-org@123456/octo-repo@456789:ref:refs/heads/main`. Older repositories keep the old format unless they opt in. GitHub Enterprise Server does not have the new format ([GitHub OIDC reference][gh-oidc-ref]). A verifier that matches `sub` strings must handle both formats.

**The rule that matters most.** GitHub says the trust configuration must have at least one condition, so that untrusted repositories cannot get access ([GitHub OIDC reference][gh-oidc-ref]).

## CircleCI contexts and CircleCI OIDC

**Contexts.** A CircleCI context is a named set of environment variables that many projects share. CircleCI injects the variables at run time ([CircleCI contexts][cci-contexts]). A job gets a context through the `context:` key in the config file. If two contexts set the same variable, the last one in the list wins. One context holds at most 100 variables, and one organization has at most 500 contexts ([CircleCI contexts][cci-contexts]).

**Restrictions.** A new context is open to all organization members. An admin can restrict a context by security group (which users), by project (which repos), and by expression (for example `pipeline.git.branch == "main"`). A job can use the context only if every restriction type passes ([CircleCI contexts][cci-contexts]). A context is the closest prior art to a surface secret group: a name, a set of values, and rules about who can use the name.

**Schedules.** A CircleCI schedule trigger runs as an actor: a user or the scheduling system. If the actor has no access to a restricted context, the workflow fails with the status `Unauthorized` ([CircleCI schedule triggers][cci-sched]).

**CircleCI OIDC.** CircleCI puts OIDC tokens in `$CIRCLE_OIDC_TOKEN` and `$CIRCLE_OIDC_TOKEN_V2`. The issuer is `https://oidc.circleci.com/org/<organization_id>`, and the default audience is the organization ID. The claim `oidc.circleci.com/project-id` identifies the project. CircleCI issues the token to a fork build only if the project setting "Pass secrets to builds from forked pull requests" is on ([CircleCI OIDC][cci-oidc]). The issuer and the claims differ from GitHub's. A verifier needs one configuration for each CI vendor.

## The patterns and their costs

Each pattern below says how it works, what it costs the suite author, what it costs the surface operator, and whether it works for a scheduled run. A sentence that starts with "Inference" is my reasoning for surface. It is not a fact from a source.

### P1. The CI job sends the secret values with the run request

The suite author stores secrets in GitHub (organization, repository, or environment secrets) or in a CircleCI context. The CI job reads them and sends the values to the runner with the run request.

- **Suite author cost.** The suite author adds each secret to the CI system and lists it in the workflow file. The author needs no help from the operator. GitHub allows 100 repository secrets and 100 environment secrets ([GitHub secrets reference][gh-secrets-ref]).
- **Operator cost.** The operator stores nothing. Inference: the runner must accept secret values over the network, keep them only in memory, and keep them out of its logs and results. GitHub's log redaction does not cover the runner's logs.
- **Limits.** A pull request from a fork gets no secrets on GitHub ([GitHub use secrets][gh-secrets-howto]). Anyone with write access to the repo can read the secrets ([GitHub secure use][gh-hardening]).
- **Scheduled run.** This pattern does not work, because no CI job exists. Inference: a CI-side schedule (the GitHub `schedule` event) can keep the pattern alive, but then the schedule lives in CI and not in surface ([GitHub events][gh-events]).

### P2. Kubernetes Secrets in the runner's namespace, one per secret group

The operator creates one Kubernetes Secret per secret group. A suite names the group. The runner gives the group's values to the tests of that suite. The runner can get the values in three ways:

- **P2a. `envFrom` or `secretKeyRef` on the runner Pod.** Inference: this way fits only a runner shape that starts one Pod per run. A long-lived runner Pod would need every group in its Pod spec, and it would need a restart to see a changed value ([K8s inject Secrets][k8s-task]).
- **P2b. A volume mount.** The values update without a restart, after a delay ([K8s Secrets][k8s-secret]). Inference: the Pod spec must still list each group, so a new group needs a change to the runner's deployment.
- **P2c. A read from the Kubernetes API at run start.** The runner's ServiceAccount gets `get` on Secrets ([K8s service accounts][k8s-sa]). An RBAC rule with `resourceNames` limits it to named Secrets ([K8s RBAC][k8s-rbac]). Inference: a new group then needs a new Secret and, with `resourceNames`, a change to the Role. Without `resourceNames`, the namespace is the boundary.

Costs:

- **Suite author cost.** The suite author declares a group name in the suite. The author asks the operator to create the group, or the author has write access to Secrets in that namespace.
- **Operator cost.** The operator creates and rotates the Secrets. The operator turns on encryption at rest, because the default is plain text in etcd ([K8s encryption at rest][k8s-encrypt]). The operator keeps the runner in its own namespace, because anyone who can create a Pod there can read every Secret there ([K8s Secrets][k8s-secret]).
- **Scheduled run.** This pattern works. The runner reads the group without a CI job.
- **Open risk.** Inference: a suite also chooses its target. If any suite can name any group, a suite can send a production token to a target that its author controls. The runner needs a rule that binds each group to the repos, branches, or targets that may use it.

### P3. An outside store synced into Kubernetes Secrets (ESO or Vault Secrets Operator)

The organization keeps the source of truth in Vault, AWS Secrets Manager, or another store. ESO or VSO copies each secret group into a Kubernetes Secret ([ESO overview][eso-overview]; [Vault Secrets Operator][vault-vso]). The runner then reads it as in P2.

- **Suite author cost.** The suite author writes the secret in the organization's existing store and declares the group name in the suite.
- **Operator cost.** The operator installs ESO or VSO and writes one `ExternalSecret` (or the VSO equivalent) per group. ESO runs with elevated privileges in all namespaces ([ESO overview][eso-overview]). A changed value reaches the cluster at the next refresh, for example after one hour ([ESO ExternalSecret][eso-es]).
- **Surface code cost.** Inference: surface needs no code for P3, because surface reads only Kubernetes Secrets. The operator picks the store.
- **Scheduled run.** This pattern works.

### P4. The runner reads the outside store directly

The runner logs in to Vault with its ServiceAccount token (Vault Kubernetes auth) and reads the group's path at run start ([Vault Kubernetes auth][vault-k8s-auth]). A variant uses the Vault Agent Injector or the Vault CSI provider, which write secrets into the Pod at start ([Vault Agent Injector][vault-agent-inj]; [Vault Kubernetes comparison][vault-k8s-compare]).

- **Suite author cost.** The suite author writes the secret in Vault and declares the path or group in the suite.
- **Operator cost.** The operator configures a Vault role for the runner's ServiceAccount and the Vault policies.
- **Surface code cost.** Inference: the runner core needs a client for each store (Vault, AWS, GCP, and so on). This conflicts with a store-neutral open-source core. The injector and CSI variants fix the secrets at Pod start, so they have the same limit as P2a.
- **Scheduled run.** This pattern works.

### P5. The CI job proves its identity with OIDC

The CI job asks GitHub for an OIDC token and sends it with the run request. The runner checks the signature against `https://token.actions.githubusercontent.com`. Then the runner checks claims such as `repository_id`, `ref`, and `workflow_ref` ([GitHub OIDC reference][gh-oidc-ref]). The runner allows only the secret groups that the operator bound to those claims. P5 delivers no secret value. It is the "who may use which group" rule for P2, P3, or P4. A variant lets the CI job log in to Vault with the token, read the values, and send them as in P1 ([GitHub OIDC in Vault][gh-oidc-vault]).

- **Suite author cost.** The suite author adds `permissions: id-token: write` to the workflow ([GitHub OIDC reference][gh-oidc-ref]). The author stores no secret in GitHub.
- **Operator cost.** The operator writes trust rules from repo and branch to secret groups. Inference: the runner must fetch and cache the issuer's keys and check the signature, the expiry, and the audience. The runner must accept both `sub` formats, or match on `repository_id` instead of `sub`. CircleCI has a different issuer and different claims ([CircleCI OIDC][cci-oidc]).
- **Scheduled run.** Inference: a scheduled run has no OIDC token. The operator binds the schedule to its groups directly, in the same way that a CircleCI schedule runs as a chosen actor ([CircleCI schedule triggers][cci-sched]).

### P6. Named secret groups inside the CI system

GitHub environments and CircleCI contexts are named secret groups that the CI system owns ([GitHub environments][gh-env]; [CircleCI contexts][cci-contexts]). They are a storage choice for P1. They are also the prior art for the rules a surface secret group needs: a name, a set of values, and restrictions by user, repo, and branch.

- **Suite author cost.** The suite author references the environment or the context in the workflow. The suite author gets an approval gate and branch rules from the CI system.
- **Operator cost.** The surface operator has nothing to run. The CI admin manages the groups.
- **Limits.** On GitHub Free, environment secrets work only in public repositories ([GitHub environments][gh-env]).
- **Scheduled run.** This pattern has the same limit as P1.

### Summary

| Pattern | Suite author | Surface operator | Scheduled run |
| --- | --- | --- | --- |
| P1. CI sends values | The author manages secrets in CI. | The operator stores nothing, but the runner handles values in transit. | It does not work without a CI-side schedule. |
| P2. Kubernetes Secret per group | The author names a group in the suite. | The operator creates the Secrets and turns on encryption and RBAC. | It works. |
| P3. Outside store plus ESO or VSO | The author writes to the existing store. | The operator installs ESO or VSO and maps each group. | It works. |
| P4. Runner reads the store directly | The author writes to the store. | The operator configures store auth, and surface needs one client per store. | It works. |
| P5. OIDC identity | The author adds one permission line. | The operator writes repo-to-group trust rules, and the runner checks JWTs. | The operator binds each schedule to its groups. |
| P6. Groups inside CI | The author references an environment or a context. | The operator runs nothing. | It does not work without a CI-side schedule. |

## Patterns that fit surface

This section is inference from the facts above. It tests each pattern against the map's givens: surface runs in any organization's cluster, a CI job starts a run in v1, and a scheduled run must stay possible.

- **P2c fits.** The runner reads a Kubernetes Secret per group from the API at run start. It needs only standard Kubernetes, it needs no restart for a new value, and it works for a scheduled run.
- **P3 fits on top of P2.** An organization with Vault or a cloud store adds ESO or VSO, and surface stays store-neutral.
- **P5 fits as the access rule.** It ties each run to a verified repo, branch, and workflow without a stored password. It also replaces a shared token that the CI job would otherwise need to call the runner.
- **P1 fits v1 only.** It is the simplest start, but it does not cover scheduled runs, and it sends values over the network on every run.
- **P2a fits only one runner shape.** It works only if the runner starts one Pod per run. The map lists the runner shape as not yet specified.
- **P4 does not fit the core.** It puts store-specific code into the open-source core.

## Points for issue #6

- Issue #6 must decide how a secret group is bound to repos, branches, and targets. Without that rule, a suite can send a group's values to a target that its author controls.
- Issue #6 must decide whether a run can mix P1 values (for example, a one-off token for an ephemeral environment) with P2 groups.
- Issue #6 must decide how a scheduled run gets its groups, because it has no OIDC token.

## Not verified

- I did not find a number for the lifetime of a GitHub OIDC JWT in the GitHub docs. GitHub says only that the cloud access token is valid for one job ([GitHub OIDC][gh-oidc]).
- The ESO documentation pages showed the banner "You're not viewing the latest version" when I read the `/latest/` URLs on 2026-10-08. The facts above come from those pages.
- I did not test any pattern in a cluster. All facts come from documentation.

[k8s-secret]: https://kubernetes.io/docs/concepts/configuration/secret/
[k8s-good]: https://kubernetes.io/docs/concepts/security/secrets-good-practices/
[k8s-task]: https://kubernetes.io/docs/tasks/inject-data-application/distribute-credentials-secure/
[k8s-encrypt]: https://kubernetes.io/docs/tasks/administer-cluster/encrypt-data/
[k8s-sa]: https://kubernetes.io/docs/concepts/security/service-accounts/
[k8s-rbac]: https://kubernetes.io/docs/reference/access-authn-authz/rbac/
[eso-overview]: https://external-secrets.io/latest/introduction/overview/
[eso-es]: https://external-secrets.io/latest/api/externalsecret/
[eso-vault]: https://external-secrets.io/latest/provider/hashicorp-vault/
[eso-multitenancy]: https://external-secrets.io/latest/guides/multi-tenancy/
[vault-k8s-auth]: https://developer.hashicorp.com/vault/docs/auth/kubernetes
[vault-jwt]: https://developer.hashicorp.com/vault/docs/auth/jwt
[vault-agent-inj]: https://developer.hashicorp.com/vault/docs/deploy/kubernetes/injector
[vault-vso]: https://developer.hashicorp.com/vault/docs/deploy/kubernetes/vso
[vault-k8s-compare]: https://developer.hashicorp.com/vault/docs/deploy/kubernetes/comparisons
[gh-secrets-concept]: https://docs.github.com/en/actions/concepts/security/secrets
[gh-secrets-howto]: https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets
[gh-secrets-ref]: https://docs.github.com/en/actions/reference/security/secrets
[gh-hardening]: https://docs.github.com/en/actions/reference/security/secure-use
[gh-env]: https://docs.github.com/en/actions/reference/workflows-and-actions/deployments-and-environments
[gh-events]: https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows
[gh-oidc]: https://docs.github.com/en/actions/concepts/security/openid-connect
[gh-oidc-ref]: https://docs.github.com/en/actions/reference/security/oidc
[gh-oidc-vault]: https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments/oidc-in-hashicorp-vault
[cci-contexts]: https://circleci.com/docs/guides/security/contexts/
[cci-sched]: https://circleci.com/docs/guides/orchestrate/schedule-triggers/
[cci-oidc]: https://circleci.com/docs/guides/permissions-authentication/openid-connect-tokens/
