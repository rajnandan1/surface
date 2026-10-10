# Install surface

This doc is for the operator: the person or team that installs surface in their own Kubernetes cluster. The runner is the surface service that receives suites and runs their tests. The client is the `surface` command that runs inside a CI job (see [GLOSSARY.md](../GLOSSARY.md)). The reasons behind each step are in [design/install.md](design/install.md).

You need:

- a Kubernetes cluster and `kubectl`,
- Helm 3.8 or later, because the chart is an OCI chart (a chart stored in a container registry),
- `gh`, the GitHub CLI, to find your owner ID,
- a GitHub org or user account that holds the repos that start runs.

Sections 1 to 3 are decisions to make before the install. Section 4 installs the runner and makes the first run. Section 5 is the work after that. Every `kubectl` and `helm` command in this doc uses the namespace `surface` and the Helm release name `surface`, so the Deployment and the Service are both named `surface`.

## 1. Its own namespace

Create a namespace for surface alone, before you create any group Secret:

```
kubectl create namespace surface
```

- Anyone who can create a Pod in a namespace can read every Secret in it. The group Secrets hold the tokens that your suites send to your targets ([design/secrets.md](design/secrets.md)). So do not put the runner in a namespace that other workloads or other teams use.
- Give the right to create Pods and to read Secrets in `surface` only to the people who run surface.
- The chart does not create the namespace.
- The runner's ServiceAccount gets `get` on the group Secrets only, by name. It cannot list Secrets, and it cannot read Helm's own release Secrets in the same namespace ([design/install.md](design/install.md#secrets-access)).

## 2. Encryption at rest

Kubernetes stores Secrets in etcd in plain text unless something else encrypts them. Check your cluster before you put a production token in a group Secret:

- **EKS** envelope-encrypts all Kubernetes API data, Secrets included, by default on Kubernetes 1.28 and later, since 2025-03 ([AWS docs](https://docs.aws.amazon.com/eks/latest/userguide/envelope-encryption.html)).
- **GKE** encrypts at the storage layer by default. Encryption of Secrets with your own Cloud KMS key is opt-in ([GKE docs](https://cloud.google.com/kubernetes-engine/docs/how-to/encrypting-secrets)).
- **AKS** encrypts at the storage layer by default. Encryption of Secrets with your own Key Vault key is opt-in ([AKS docs](https://learn.microsoft.com/en-us/azure/aks/use-kms-etcd-encryption)).
- **A self-managed cluster** needs an `EncryptionConfiguration` on the API server ([Kubernetes docs](https://kubernetes.io/docs/tasks/administer-cluster/encrypt-data/)).

Surface does not check any of this.

## 3. What the runner calls

When your cluster limits outbound traffic, for example with a NetworkPolicy or a firewall, allow these connections from the runner's pod:

- **The OIDC issuer**, `https://token.actions.githubusercontent.com` by default. The runner reads the issuer's configuration when it starts, and `surface serve` exits when it cannot reach the issuer. Then it fetches the issuer's keys to check the token of each CI job. GitHub Enterprise Server uses `https://HOSTNAME/_services/token`, which you set as `github.issuer` in the config ([design/runs.md](design/runs.md#identity)).
- **Each target** in the target list, `config.targets`. The tests send their requests there ([design/targets.md](design/targets.md)).
- **The Kubernetes API**, inside the cluster. The runner reads the group Secrets from it when a run starts ([design/secrets.md](design/secrets.md#where-secrets-live)).

Your CI jobs call the runner at `config.url`. GitHub-hosted runners have no fixed address, so the runner may have to be on the public internet. Every route except `GET /healthz` needs a valid GitHub token from an owner in `github.owner_ids`. The runner checks the token and the owner before it reads the request body ([design/runs.md](design/runs.md#who-may-use-the-runner)).

The runner never follows a redirect, so a target cannot send the runner to a host that is not in the target list ([design/targets.md](design/targets.md#redirects)).

## 4. The first run, end to end

### Step 1. Find your owner ID

```
gh api orgs/crustdata --jq .id
```

For a user account, use `gh api users/<name> --jq .id`. The answer is a number, for example `63745551`. The runner matches the owner by this ID and not by name, because GitHub frees a name when an org or a user is renamed ([design/runs.md](design/runs.md#who-may-use-the-runner)).

### Step 2. Write `values.yaml`

```yaml
config:                       # The runner config. The chart copies it as is into a ConfigMap.
  url: https://surface.example.com
  github:
    owner_ids: [63745551]
  targets:
    - https://*.preview.crustdata.com
    - url: https://api.crustdata.com
      rate: 20
  groups:
    production:
      callers:
        - repo: crustdata/api
          ref: refs/heads/main
      targets:
        - https://api.crustdata.com
```

- `url` is the URL that your CI jobs call. It is also the token audience, so the `runner` input of the action must be the same text, byte for byte. Write it with no trailing `/`. It is required.
- `github.owner_ids` lists the owners whose repos may use the runner. It is required. `github.issuer` is optional ([design/runs.md](design/runs.md#who-may-use-the-runner)).
- `targets` is the target list: the only base URLs that a run may call. It is required. `*` matches one part of a host name, and `rate` caps the requests per second to that target ([design/targets.md](design/targets.md), [design/limits.md](design/limits.md)).
- `groups` binds each secret group to the callers (a repo and a ref) and the targets that may use it. It is optional. The chart gives the runner `get` on the Kubernetes Secret of each group, and on no other Secret ([design/secrets.md](design/secrets.md#who-may-use-a-group)).
- `results` and `limits` are optional. The defaults keep run results for 7 days and allow 128 requests in flight and 32 runs at once ([design/runner.md](design/runner.md), [design/limits.md](design/limits.md)).

The runner refuses to start on an unknown key at any level, a missing required key, or a bad value. The pod log then names the key in one line.

### Step 3. Create the group Secrets

```
kubectl -n surface create secret generic production --from-literal=api_token=...
```

- Each group has one Kubernetes Secret. Its name is the group name, or the group's `secret` when you set one. Each key in it is one secret, and a suite reads it as `${{ secrets.production.api_token }}` ([design/secrets.md](design/secrets.md)).
- `--from-literal` leaves the value in your shell history. `--from-file=api_token=./api_token.txt` does not.
- A missing Secret does not stop the install. A suite that uses its group fails at load with `secret group "production" was not read: secrets "production" not found`, and the other suites run.

### Step 4. Install the chart

```
helm install surface oci://ghcr.io/rajnandan1/charts/surface --version 1.0.0 -n surface -f values.yaml
kubectl -n surface rollout status deploy/surface
```

The chart creates a Deployment with one replica, a ConfigMap with the config, a PersistentVolumeClaim of 1 GiB for the run result files, a Service on port 80, and a ServiceAccount. It creates a Role and a RoleBinding only when `config.groups` has a group ([design/install.md](design/install.md#the-chart)).

When the pod does not become ready, read its log:

```
kubectl -n surface logs deploy/surface
```

A config error is one line, for example `surface serve: /etc/surface/config.yaml: github.owner_ids is missing or empty`.

### Step 5. Turn on the Ingress or the HTTPRoute, and set up DNS and TLS

Skip this step when your GitHub runners run in the same cluster as the runner. See "Day 2".

For an Ingress, add this to `values.yaml`:

```yaml
ingress:
  enabled: true
  className: traefik
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
  tls:
    secretName: surface-tls
```

For a Gateway API HTTPRoute, add this instead. The Gateway holds the TLS certificate:

```yaml
httpRoute:
  enabled: true
  parentRefs:
    - name: public
      namespace: gateway
```

Then apply the change:

```
helm upgrade surface oci://ghcr.io/rajnandan1/charts/surface --version 1.0.0 -n surface -f values.yaml
```

- The chart takes the host from `config.url`, so the route and the token audience cannot disagree.
- Point a DNS record for that host at your Ingress controller or your Gateway.
- With cert-manager, the annotation above gets the certificate for the Ingress. Without it, create the TLS Secret `surface-tls` in the namespace `surface` yourself.
- A Gateway accepts routes only from its own namespace by default. When the Gateway is in another namespace, its listener needs `allowedRoutes.namespaces` that lets in the namespace `surface` ([Gateway API docs](https://gateway-api.sigs.k8s.io/guides/multiple-ns/)). `kubectl -n surface get httproute surface -o yaml` shows the condition `Accepted` with the status `"True"` when the Gateway took the route.

### Step 6. Check that the runner answers

```
curl -i https://surface.example.com/healthz
```

The answer is `200` with an empty body. `GET /healthz` needs no token.

### Step 7. Add the workflow to a product repo

Add a suite to the product repo, for example `surface/api/health.yaml`:

```yaml
apiVersion: surface/v1
kind: API
tests:
  - name: health
    method: GET
    path: /health
    expect:
      status: 200
```

Add the action to a workflow of the same repo:

```yaml
permissions:
  id-token: write        # GitHub issues the OIDC token only with this.
  contents: read
steps:
  - uses: actions/checkout@v5
  - uses: rajnandan1/surface@v1
    with:
      runner: https://surface.example.com            # The same text as config.url.
      target: https://pr-${{ github.event.number }}.preview.crustdata.com   # Must match config.targets.
      suites: surface/                               # Optional. The default is surface/.
      timeout: 30m                                   # Optional. The wait limit. The default is 30m.
```

The suite format is in [design/suite-format.md](design/suite-format.md), and the action is in [design/runs.md](design/runs.md#the-action-and-the-client).

### Step 8. Push, and read the CI log

Push the change. The job prints one line per suite and one last line with the totals, then exits `0` when every test passed, `1` when a test failed, and `2` when the run did not complete ([design/runs.md](design/runs.md#exit-codes-and-the-ci-log)). The job page also shows a summary table, and each failure shows on its line in the pull request.

```
surface 1.0.0 run 01j9z3k4m5n6p7q8r9s0t1u2v3 target https://pr-123.preview.crustdata.com
surface/api/health.yaml: 1 passed

run 01j9z3k4m5n6p7q8r9s0t1u2v3: 1 passed
```

When the first run fails before any test, the CI log has one of these lines:

- `surface run: no OIDC token: not inside GitHub Actions, or the job is missing permissions: id-token: write`: add `id-token: write` to the workflow's `permissions`.
- `run: token audience "https://surface.internal.example.com" is not this runner`: make the action's `runner` input the same text as `config.url`.
- `run: repo "crustdata/api" (owner id 63745551) may not use this runner`: add the number to `github.owner_ids`.
- `run: target "https://pr-123.preview.crustdata.com" is not in the target list`: add the target, or a `*` entry that matches it, to `config.targets`.
- `surface run: runner at https://surface.example.com has no /v1/runs: it may be a different major version`: check that the `runner` input points at the runner and not at another service.

A suite that cannot use a secret group fails at load with one line, and the other suites run. For example, `suite surface/api/users.yaml: secret group "production" is not available to crustdata/api on refs/pull/123/merge` means that the group's `callers` has no entry for that repo and ref ([design/secrets.md](design/secrets.md#load-errors)).

## 5. Day 2

### Upgrades and config changes

```
helm upgrade surface oci://ghcr.io/rajnandan1/charts/surface --version 1.0.1 -n surface -f values.yaml
```

- `helm upgrade` restarts the pod when the image or the config changes. A new target, a new group, and a new caller are config changes too.
- The restart cancels every run in flight. Each one ends with `run <id>: error: runner stopped`, and its CI job exits `2`. The runner first runs the cleanups of those runs, for up to 15 minutes ([design/runner.md](design/runner.md#a-restart)).
- The old pod stops before the new pod starts, because the run result volume attaches to one pod at a time. A run request in that gap fails with a connection error, and its CI job exits `2`.
- So upgrade when no run is in flight. The runner log has a line when a run is accepted and a line when it ends. A run with only the first line is in flight:

```
kubectl -n surface logs deploy/surface --since=1h
```

### Rotate a secret

Change the Kubernetes Secret. The runner reads it again at the start of each run, so the next run uses the new value, with no restart:

```
kubectl -n surface create secret generic production --from-literal=api_token=... --dry-run=client -o yaml | kubectl -n surface replace -f -
```

### Read a run result

The run id is in the first line of the CI log. The runner keeps each run result as a file for 7 days by default:

```
kubectl -n surface exec deploy/surface -- ls /var/lib/surface/results
kubectl -n surface exec deploy/surface -- cat /var/lib/surface/results/01j9z3k4m5n6p7q8r9s0t1u2v3.json
```

The file has the same JSON as `GET /v1/runs/<id>`, with secret values already redacted ([design/runs.md](design/runs.md#the-wire-shapes)).

### GitHub runners in the same cluster

When your GitHub runners run in the same cluster, for example with [Actions Runner Controller](https://github.com/actions/actions-runner-controller), they can call the Service, with no Ingress and no HTTPRoute. Set the same URL in both places:

```yaml
config:
  url: http://surface.surface.svc
```

```yaml
- uses: rajnandan1/surface@v1
  with:
    runner: http://surface.surface.svc
```

The token then travels over plain HTTP inside the cluster.

### Memory

The chart sets a memory limit of `4Gi`, which covers the default `limits` ([design/install.md](design/install.md#the-pod)). When you raise `config.limits.requests` or `config.limits.runs`, also raise `resources.limits.memory` in `values.yaml`. Each request in flight can hold a response body of 10 MiB. Each run can hold 16 MiB of suite text, and about 64 MiB with the parsed suites. The 64 MiB is an estimate, not a measurement. The chart sets `GOMEMLIMIT` from the memory limit for you.
