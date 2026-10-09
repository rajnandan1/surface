# Install

The operator is the person or team that installs surface in their own cluster. The runner is the surface service that receives suites and runs their tests, and the client is the `surface` command that runs inside a CI job (see [GLOSSARY.md](../../GLOSSARY.md)). This doc defines the Helm chart, how the operator gives the runner its config, the image, how a release is made, how a client and a runner of different versions work together, and what the operator docs cover. The decisions come from [How does an organization install surface?](https://github.com/rajnandan1/surface/issues/13) and [ADR 0012](../adr/0012-within-a-major-version-the-api-only-grows-and-the-runner-rejects-what-it-does-not-know.md).

## Example

The operator's install, in a shell:

```
gh api orgs/crustdata --jq .id                    # 63745551, for github.owner_ids
kubectl create namespace surface
kubectl -n surface create secret generic production --from-literal=api_token=...
helm install surface oci://ghcr.io/rajnandan1/charts/surface --version 1.2.0 -n surface -f values.yaml
curl https://surface.example.com/healthz          # 200
```

The operator's `values.yaml`:

```yaml
config:                       # The runner's config. The chart copies it as is into a ConfigMap.
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

ingress:
  enabled: true               # Off by default. The host comes from config.url.
  className: traefik
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt
  tls:
    secretName: surface-tls
```

## The chart

- One Helm chart is the only install path. It lives in this repo at `charts/surface` and is published as an OCI chart, a chart stored in a container registry, at `oci://ghcr.io/rajnandan1/charts/surface`. An operator who does not use Helm runs `helm template` and applies the YAML.
- The chart does not create the namespace. The operator installs into a namespace of its own (see "The operator docs").
- The chart renders these objects:
  - a Deployment, described in "The pod",
  - a ConfigMap with the runner config,
  - a PersistentVolumeClaim for the run result files,
  - a Service of type `ClusterIP` that sends port 80 to the runner's port 8080,
  - a ServiceAccount, a Role, and a RoleBinding for the group Secrets,
  - an Ingress, off by default,
  - an HTTPRoute, off by default.

### The pod

- The Deployment has one replica and the `Recreate` strategy ([runner.md](runner.md)). The container runs `surface serve --config /etc/surface/config.yaml`.
- The pod template has the annotation `checksum/config`, the hash of the rendered config. So a config change makes `helm upgrade` restart the pod.
- The chart sets a readiness probe on `GET /healthz`, port 8080, every 5 seconds. It sets no liveness probe.
- Why no liveness probe: a liveness failure kills the pod, and with one replica that fails every run in flight with `runner restarted`. A runner under heavy load can answer a probe slowly. A Go HTTP server rarely deadlocks. A readiness failure only takes the pod out of the Service for a moment, and the client retries at its next poll ([runs.md](runs.md)).
- `terminationGracePeriodSeconds` is 900, 15 minutes. It bounds the cancel on SIGTERM ([runner.md](runner.md)). The runner exits as soon as its runs end, so a normal deploy stops in seconds.
- Why 15 minutes: it is the same bound as the run time limit's ([limits.md](limits.md)). Requests in flight get 5 minutes to finish or time out, and cleanups get 10 minutes.
- Resources: the CPU request is `100m` and the memory request is `256Mi`. The memory limit is `4Gi`. There is no CPU limit. The chart sets the environment variable `GOMEMLIMIT`, the Go runtime's soft memory cap, to 90% of the memory limit, because Go does not read the container's limit by itself.
- Why `4Gi`: it covers the worst case at the default `limits` ([limits.md](limits.md)). Response bodies take at most 128 × 10 MiB, which is 1.25 GiB. 32 runs in memory hold at most 32 × 16 MiB of suite text, about 2 GiB in raw and parsed form. The parsed size is an estimate, not a measurement. An operator who raises `limits.requests` or `limits.runs` raises the memory limit by the same sum.
- Why no CPU limit: CPU throttling slows the runner, and its tests then time out when the target is fine.
- The security context: the pod runs as user and group 65532 with `runAsNonRoot`, and `fsGroup: 65532` makes the volume writable. The root file system is read-only, `allowPrivilegeEscalation` is false, every capability is dropped, and the seccomp profile is `RuntimeDefault`.
- The chart mounts the ServiceAccount token, because the runner reads the group Secrets with it ([secrets.md](secrets.md)).

### The volume

- The PersistentVolumeClaim is mounted at `results.dir` from the config, `/var/lib/surface/results` by default ([runner.md](runner.md)). The chart reads the path from the config, so the mount and the config cannot disagree.
- The default size is `1Gi`, set by `persistence.size`. A volume of 1 GiB holds about 1,000 runs of 1,000 tests with 100 failures each ([runner.md](runner.md)).
- The access mode is `ReadWriteOnce`. `persistence.storageClass` is empty by default, so the cluster's default StorageClass applies.
- With `persistence.enabled: false`, the chart mounts an `emptyDir`, a volume that Kubernetes deletes with the pod. Then no run result survives a restart.

### Secrets access

- The Role has one rule: `get` on `secrets`, with `resourceNames` set to the Kubernetes Secret of each group in `config.groups`. That is the group's `secret`, or the group name when `secret` is not set ([secrets.md](secrets.md)). The RoleBinding binds the Role to the ServiceAccount.
- When `config.groups` is empty or absent, the chart renders no Role and no RoleBinding. An empty `resourceNames` list would allow `get` on every Secret in the namespace, Helm's own release Secrets included.
- The runner needs no `list` and no `watch`.
- A new group is a change to the values. `helm upgrade` then updates the config and the Role in one step.

### Reaching the runner

- With `ingress.enabled: true`, the chart renders an Ingress for the host of `config.url`, path `/`, to the Service. It takes `ingress.className`, `ingress.annotations`, and `ingress.tls.secretName`. cert-manager works through the annotations.
- With `httpRoute.enabled: true`, the chart renders a Gateway API HTTPRoute for the same host. It takes `httpRoute.parentRefs`, the Gateway it attaches to. The Gateway holds the TLS certificate.
- Why both: ingress-nginx was retired, and its repo was archived in March 2026. Gateway API is the successor that the Kubernetes project recommends. The Ingress API is frozen, but it stays GA, and many controllers still serve it. Each template is about 15 lines.
- Why both are off by default: an install whose GitHub runners run in the same cluster needs neither. Those runners call the Service.
- Why the host comes from `config.url`: the runner checks the token audience against `config.url`, and the client sends the URL it calls as the audience ([runs.md](runs.md)). One value sets both.

## The runner config

- The operator writes the whole runner config under `config:` in the values. The chart copies it unchanged into the ConfigMap as `config.yaml`, mounted at `/etc/surface/config.yaml`.
- The config has six top-level keys:
  - `url` and `github` ([runs.md](runs.md)),
  - `targets` ([targets.md](targets.md)),
  - `groups` ([secrets.md](secrets.md)),
  - `results` ([runner.md](runner.md)),
  - `limits` ([limits.md](limits.md)).
- The chart reads three values from the config: `config.url` for the route host, `config.groups` for the Role, and `config.results.dir` for the mount path.
- The runner reads the config once, at start. It refuses to start on an unknown key at any level, a missing required key, or a bad value. It prints one line that names the key.
- Why the config lives in the values and not in the operator's own ConfigMap: the Role and the route host come from the config, so a separate list of Secret names would drift from `groups`.
- Why no reload: a reload is more code, and the config could change halfway through a run. The cost: a new target or a new group restarts the pod, and that cancels the runs in flight with `runner stopped`, as a deploy does ([runner.md](runner.md)). Operators who ask for a reload are the trigger to add one.

## The image

- The image is `ghcr.io/rajnandan1/surface:X.Y.Z`, for `linux/amd64` and `linux/arm64`. There is no `latest` tag. The chart uses its `appVersion` as the image tag.
- The base is `gcr.io/distroless/static-debian13:debug-nonroot`, pinned by digest in the `Dockerfile`. It holds busybox, one small binary that gives `sh`, `cat`, and `ls`. It also holds the CA certificates, and it runs as user 65532.
- Why busybox: the operator reads a run result with `kubectl exec` and `cat` ([runner.md](runner.md)). `kubectl cp` needs `tar` in the image. The `nonroot` variant has no shell, so neither works there.
- Each release updates the base digest. `gcr.io/distroless/static-debian12` stopped getting builds on 2026-08-11, so the base moves and the pin must follow.

## One binary

- The client and the runner are one Go binary, `surface`, with three subcommands: `run` and `check` ([runs.md](runs.md)), and `serve`, the runner. The image runs `surface serve`.
- Why one binary: one build and one version, so the client and the runner cannot drift apart. They already share the suite loader.

## The release

- The file `VERSION` at the repo root holds one semver version, `X.Y.Z`. The `surface` binary embeds it with `//go:embed`. Nothing passes a version at build time.
- One version names every part of a release: the git tag `vX.Y.Z`, the image tag `X.Y.Z`, the chart's `version` and `appVersion`, and the binaries.
- The repo has GitHub immutable releases turned on. After a release is published, nobody can add, change, or delete its assets or move its `vX.Y.Z` tag, and GitHub creates a release attestation, a signed record of the release's assets.

A release happens in this order:

1. A maintainer changes `VERSION` in a pull request and merges it.
2. The maintainer pushes the tag `vX.Y.Z` on the merge commit.
3. The tag push runs `.github/workflows/release.yml`. It fails when the tag is not `v` followed by the text of `VERSION`.
4. The workflow builds `surface` with `go build -trimpath` for each of the 6 pairs of {`linux`, `darwin`, `windows`} × {`amd64`, `arm64`}. Each binary is one file, `surface_X.Y.Z_<os>_<arch>`, with `.exe` on Windows. There is no archive.
5. `sha256sum` writes `checksums.txt` for the 6 binaries.
6. `docker buildx build --platform linux/amd64,linux/arm64 --push` builds and pushes the image.
7. `helm package` and `helm push` publish the chart to `oci://ghcr.io/rajnandan1/charts`.
8. `gh release create vX.Y.Z --draft` uploads the binaries and `checksums.txt`. Then the workflow publishes the draft, and the release becomes immutable.
9. The workflow moves the tag `v1` to the same commit.

- The tag `v1` moves last, so `@v1` never points at a release whose assets are missing.
- Why plain commands and not GoReleaser: each step is one command that a maintainer can read.

## The action

- `action.yml` reads the version from the file `$GITHUB_ACTION_PATH/VERSION`, inside the action's own files.
- Why that file: inside a composite action, `github.action_ref` gives `v1`, not the exact version, and it is empty inside `run:` ([actions/runner#2473](https://github.com/actions/runner/issues/2473)). The expression `${{ github.action_path }}` gives a wrong path in container jobs ([actions/runner#716](https://github.com/actions/runner/issues/716)). The environment variable `GITHUB_ACTION_PATH` is right in both cases.
- The action maps `runner.os` and `runner.arch` to an OS and an architecture: `Linux` to `linux`, `macOS` to `darwin`, `Windows` to `windows`, `X64` to `amd64`, and `ARM64` to `arm64`. Another pair is exit `2` with `surface: no binary for <os>/<arch>`.
- It downloads the binary and `checksums.txt` from `https://github.com/rajnandan1/surface/releases/download/vX.Y.Z/`, checks the binary's SHA-256 against its line in `checksums.txt`, and runs `surface run` ([runs.md](runs.md)).
- What the check catches: a broken download. A swapped asset cannot happen, because the release is immutable. A compromised release workflow publishes both the binary and its checksum, so no check inside the action catches it. That is true of every GitHub Action.
- `v1` is a plain tag that moves. An operator who wants a fixed action pins `@vX.Y.Z` or a commit SHA in the workflow. The tag of an immutable release cannot move.

## Versions that differ

- `@v1` gets the newest 1.x client, and the operator's runner can be older. The rule below keeps them working together. [ADR 0012](../adr/0012-within-a-major-version-the-api-only-grows-and-the-runner-rejects-what-it-does-not-know.md) records it.
- Within 1.x, the `/v1/` routes, the run request, the run result, the suite format `surface/v1`, and the runner config only gain fields and values. Nothing is removed or renamed.
- The client ignores unknown fields in the run result. So an old client works against a new runner, and it prints what it knows.
- The runner rejects an unknown field in the run request with `400`. At load, it rejects a suite with an unknown field or an unknown value ([suite-format.md](suite-format.md)). The error line ends with the runner's version, so the developer knows to ask the operator to upgrade:

```
run: unknown field "targets" (runner 1.1.0)
suite surface/api/users.yaml: line 12: unknown field "retry" (runner 1.1.0)
```

- A `404` to `POST /v1/runs` makes the client exit `2` with `surface run: runner at <url> has no /v1/runs: it may be a different major version`.
- There is no version negotiation and no minimum client version.
- `surface check` in a newer client can pass a suite that an older runner rejects. The runner's line names its version.

## The operator docs

The operator docs live at `docs/install.md` and are written during the v1 build. They have five sections:

1. **Its own namespace.** The operator creates the namespace with `kubectl create namespace surface`, before the group Secrets, and installs with `helm install surface oci://ghcr.io/rajnandan1/charts/surface -n surface -f values.yaml`. The reason: anyone who can create a Pod in a namespace can read every Secret in it ([secrets.md](secrets.md)).
2. **Encryption at rest.** The doc states the defaults for Secrets, because Kubernetes stores them in plain text unless something else encrypts them:
   - EKS envelope-encrypts all Kubernetes API data by default on Kubernetes 1.28 and later, since 2025-03.
   - GKE and AKS encrypt at the storage layer by default. Encryption of Secrets with a KMS key is opt-in on both.
   - A self-managed cluster needs an `EncryptionConfiguration`.

   Surface does not check any of this.
3. **What the runner calls.** The runner needs outbound access to the OIDC issuer, `token.actions.githubusercontent.com` by default, and to the targets.
4. **The first run, end to end:**
   1. Find the owner ID with `gh api orgs/<name> --jq .id`.
   2. Write `values.yaml`.
   3. Create the group Secrets with `kubectl create secret generic`.
   4. Run `helm install`.
   5. Turn on the Ingress or the HTTPRoute, and set up DNS and TLS.
   6. Check that `curl https://<url>/healthz` answers `200`.
   7. Add the workflow, with `id-token: write`, to a product repo ([runs.md](runs.md)).
   8. Push, and read the CI log.
5. **Day 2:**
   - `helm upgrade` cancels the runs in flight, so the operator upgrades when no run is in flight.
   - The operator reads a run with `kubectl exec deploy/surface -- cat /var/lib/surface/results/<id>.json`.
   - When the GitHub runners run in the same cluster, for example with Actions Runner Controller, `runner: http://surface.surface.svc` and the same `config.url` work without an Ingress or an HTTPRoute. The token then travels over plain HTTP inside the cluster.

## Known limits

- A new target or a new group restarts the pod, and that cancels the runs in flight.
- With `Recreate`, no run can start until the old pod is gone. A hung target can hold a deploy for up to 15 minutes.
- A node that stops with less notice than the grace period, for example a spot node, kills the pod, and the crash rule of [runner.md](runner.md) applies.
- The `4Gi` memory limit rests on an estimate of the parsed suite size.
- The action downloads from github.com. A GitHub Enterprise Server whose runners cannot reach github.com cannot use it.
- No check inside the action catches a compromised release workflow.

## Decided elsewhere

- One process, one replica, the run result files, and the cancel on SIGTERM: [runner.md](runner.md)
- The pool, the caps, and the run time limit: [limits.md](limits.md)
- The secret groups, the namespace, and how the runner reads a Secret: [secrets.md](secrets.md)
- The target list: [targets.md](targets.md)
- The client, the token, the owner list, and the CI log: [runs.md](runs.md)
