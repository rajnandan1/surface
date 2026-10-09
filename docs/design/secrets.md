# Secrets

A secret is one named value, for example an API token. A secret group is a named set of secrets that the operator creates and that a suite reads by name (see [GLOSSARY.md](../../GLOSSARY.md)). This doc defines where secrets live, how a suite reads them, who may use them, and how the runner keeps them out of its output. The decisions come from [Where do secrets live, and how does a suite declare the secrets it needs?](https://github.com/rajnandan1/surface/issues/6) and [ADR 0005](../adr/0005-secret-groups-in-kubernetes-secrets.md).

## Example

The suite side:

```yaml
defaults:
  headers:
    Authorization: Bearer ${{ secrets.production.api_token }}
```

The operator side, in the runner's config:

```yaml
groups:
  production:
    secret: production          # The Kubernetes Secret name. Optional. The default is the group name.
    callers:
      - repo: crustdata/api
        ref: refs/heads/main
    targets:
      - https://api.crustdata.com
  preview:
    callers:
      - repo: crustdata/*
        ref: refs/pull/*/merge
    targets:
      - https://*.preview.crustdata.com
```

The Kubernetes Secret, in the runner's namespace:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: production
stringData:
  api_token: "..."
```

## Where secrets live

- Secret values live in Kubernetes Secrets in the runner's namespace, one Kubernetes Secret per secret group.
- The runner reads a group from the Kubernetes API when a run starts. It reads only the groups that the run's suites reference. It keeps the values in memory for that run and reads them again at the next run. So a rotated value takes effect at the next run, with no restart.
- An organization that keeps secrets in Vault or a cloud store fills the Kubernetes Secrets with the External Secrets Operator or a similar controller. Surface has no code for any store.
- The CI job sends no secret values. Only the runner holds them.

## How a suite reads a secret

- `${{ secrets.<group>.<name> }}` reads the key `<name>` of the group `<group>`. It is allowed anywhere a `${{ }}` is allowed (see [suite-format.md](suite-format.md)).
- `<group>` is a slug `[a-z0-9-]`, like a test name, because it is also the default Kubernetes Secret name. `<name>` allows `[A-Za-z0-9_-]` and is matched exactly against the Secret's key. Neither allows a dot, because the dot is the separator.
- A secret value is always text, even when it is the whole JSON value. `"limit": "${{ secrets.x.n }}"` sends a string.
- A suite has no list of the groups it uses. The references are the declaration. The runner collects the groups from the references at load.
- A secret in `path` or `query` reaches the target's access logs. That is the suite author's choice.

## Who may use a group

- The runner's config has a `groups:` section. Each group has `callers`, `targets`, and an optional `secret`.
- A caller is a `repo` and a `ref`. Both are matched as a glob where `*` does not cross a `/`, for example `crustdata/*` or `refs/pull/*/merge`. A run may use a group only if its caller matches one entry. The runner learns the caller's `repo` and `ref` from the CI job's GitHub OIDC token ([runs.md](runs.md)).
- `callers` match names. A stranger who registers a renamed org's old name still cannot match them, because the runner refuses every token whose owner is not on `github.owner_ids` before it looks at any group ([runs.md](runs.md)).
- `targets` lists the targets the group's values may be sent to. A target string and its matching rules are in [targets.md](targets.md). The runner also keeps a target list for every run, in the same doc. That list is a separate check.
- `callers` and `targets` are required and must not be empty. The runner refuses to start on a group with an empty list.
- A Kubernetes Secret that no group names is invisible to suites. That includes Helm's own release Secrets in the same namespace.
- A scheduled run later is a caller with a schedule name in place of a repo. The binding does not block it.

## Redaction

- The runner replaces every occurrence of every secret value in everything it writes (its logs, the failure report, and the CI output) with the reference text, for example `${{ secrets.production.api_token }}`.
- It also replaces the URL-escaped form of each value, because the runner URL-escapes values in `path`, `query`, and `form`.
- It replaces longer values first, so a value that is a prefix of another value does not break the replacement.
- Redaction runs on the output, not on the input. So a target that echoes a token back in a response body is also redacted.
- Known limits: a value of one or two characters redacts more text than needed. An encoded form that the runner did not make, for example base64, is not caught. A suite cannot encode, so a Basic auth value is stored already encoded, and the exact match catches it.

## Load errors

A suite fails at load, before any request, when:

- the group is not in the config,
- the Kubernetes Secret does not have the key, or the value is empty,
- the caller is not bound to the group, or
- the target is not bound to the group.

The failure is one line that names the suite, the group, and the key or the reason:

```
suite api/users.yaml: secret group "production" is not available to crustdata/web on refs/heads/feature-x
suite api/users.yaml: secret group "production" has no key "api_token"
```

The other suites in the run still run. The report counts the failed suite as failed.

## What the operator sets up

- The runner runs in its own namespace, because anyone who can create a Pod in a namespace can read every Secret in it.
- The runner's ServiceAccount gets `get` on Secrets in that namespace, limited with `resourceNames` to the group Secrets.
- Encryption at rest for Secrets, because Kubernetes stores them in plain text by default.

The Helm chart and the exact RBAC are part of the install, decided in [How does an organization install surface?](https://github.com/rajnandan1/surface/issues/13).
