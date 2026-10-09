# Targets

A target is the base URL a run calls. The target list is the list in the runner's config of the targets the runner may call (see [GLOSSARY.md](../../GLOSSARY.md)). This doc defines where a run's target comes from, what a target string is, how the runner matches a target against a list, and why the runner never follows a redirect. The decisions come from [How does a run get its target URL?](https://github.com/rajnandan1/surface/issues/7) and [ADR 0006](../adr/0006-ci-job-sends-the-target-and-runner-calls-only-listed-targets.md).

## Example

The CI job side, in the run request:

```yaml
target: https://pr-123.preview.crustdata.com
```

The operator side, in the runner's config:

```yaml
targets:                              # The target list. The runner calls nothing else.
  - https://api.crustdata.com
  - https://*.preview.crustdata.com
  - http://localhost:8080             # A local install. Secrets go over plain text here.
  - url: https://staging.crustdata.com
    rate: 20                          # At most 20 requests per second. See limits.md.

groups:
  production:
    targets:
      - https://api.crustdata.com     # A second, separate check. See secrets.md.
```

A test in a suite:

```yaml
  - name: search-person
    method: POST
    path: /person/search              # Sent to https://pr-123.preview.crustdata.com/person/search
```

## Where a run's target comes from

- The CI job sends one target in the run request, in the field `target`. The run request and how the CI job sends it are in [runs.md](runs.md).
- A run has exactly one target. Every test path in every suite of the run is relative to it.
- A suite has no target field. The runner has no default target. The runner rejects a run request without `target`.
- A product with two services, for example `api.` and `auth.`, runs two runs, each with its own suites.
- v1 has no `${{ run.target }}` variable. The three name kinds in [suite-format.md](suite-format.md) stay.
- Later, a `target:` field on a test plus a named map in the run request can add several targets per run without a format break. It is not in v1.

## What a target string is

- A target is `scheme://host[:port]`.
- `scheme` is `http` or `https`.
- `host` is a DNS name or an IP literal.
- `port` is optional. A missing port means 443 for `https` and 80 for `http`, so `https://api.crustdata.com` and `https://api.crustdata.com:443` are the same target.
- The runner rejects a target that has anything after the host and port: a path (even a single `/`), a query, a fragment, or a user name. The test `path` holds the full path.
- In a list entry, `*` in the host matches one or more characters that are not a `.`. So `https://*.preview.crustdata.com` matches `https://pr-123.preview.crustdata.com`. It does not match `https://preview.crustdata.com` or `https://a.b.preview.crustdata.com`. `https://pr-*.preview.crustdata.com` is also allowed. `*` is not allowed in the scheme or the port. The run request's target has no `*`.
- There is no entry that matches every host, because `*` does not cross a `.`.

## How the runner matches a target against a list

A target matches a list when it matches at least one entry. A target matches an entry when:

- the schemes are equal,
- the hosts are equal, ignoring case, with `*` in the entry as above, and
- the ports are equal, after the default port is filled in.

The same rules apply to the target list and to a group's `targets` in [secrets.md](secrets.md).

## The target list

- The runner's config has a top-level `targets:` list. It is required and must not be empty. The runner refuses to start otherwise.
- Each entry is one target string, or a mapping with `url`, the target string, and `rate`, the per-target rate limit ([limits.md](limits.md)). An entry has no callers. The owner list decides which repos may start a run ([runs.md](runs.md)). The secret group binding decides which callers may use which secrets. The target's own authentication limits what a run without secrets can change.
- When a run starts, before any suite loads and before any request, the runner checks the run's target against the target list. If it does not match, the whole run fails, and no suite runs.
- The secret group binding is a second, separate check. A suite that references a group whose `targets` do not match the run's target fails at load ([secrets.md](secrets.md)). The runner does not check a group's `targets` against the target list at startup. Both checks run per run.
- Why the list is deny by default: a suite is untrusted input ([ADR 0001](../adr/0001-suites-are-untrusted-input.md)) and the runner runs inside the cluster. Without the list, a run that uses no secrets could call the cloud metadata address `169.254.169.254`, the Kubernetes API, or any in-cluster service.

## Redirects

- The runner never follows a redirect. A `3xx` response is the response the test sees. A test asserts it with `expect.status: 302` and `expect.headers.Location`.
- Following a redirect would let a target send a request to a host that is not on the target list, for example `Location: http://169.254.169.254/`.

## Building the request URL

- The request URL is the target text plus the path text. The runner joins them as text. It does not resolve `path` as a URL reference, because a path such as `//evil.com/x` would then change the host.
- A test `path` must start with `/`. The runner rejects a suite with a path that does not.
- A variable in `path` is URL-escaped ([suite-format.md](suite-format.md)), so a value cannot add a host or a second path segment.

## Run errors

A run fails before any suite loads when:

- the run request has no `target`,
- the target has a path, a query, a fragment, a user name, or a `*`, or
- the target is not in the target list.

The failure is one line:

```
run: target is missing
run: target "https://api.crustdata.com/v1" must not have a path
run: target "http://10.0.0.5:8080" is not in the target list
```

## Known limits

- The target list matches names, not addresses. The runner does not resolve DNS before a request to check the address. A listed name that resolves to an in-cluster address is called. The operator owns the listed names, so this is the operator's choice.
- An `http` target sends secrets in plain text. The operator lists it knowingly.
