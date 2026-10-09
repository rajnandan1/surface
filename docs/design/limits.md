# Limits

The runner puts limits on a run, so that one run cannot take the runner's memory, overload a target, or run without end. A run is one execution of a set of suites, started by one CI job, and a target is the base URL a run calls (see [GLOSSARY.md](../../GLOSSARY.md)). This doc defines how many requests the runner sends at once, how fast it sends them to one target, how long a test and a run may take, and how much of a response the runner reads. The decisions come from [What limits does the runner put on a run?](https://github.com/rajnandan1/surface/issues/12).

## Example

The operator side, in the runner's config:

```yaml
limits:
  requests: 128     # The pool: requests in flight across all runs. The default is 128.
  runs: 32          # Runs held in memory. Above it, POST /v1/runs answers 503. The default is 32.
  run_time: 30m     # The run time limit. The default is 30m.

targets:
  - https://*.preview.crustdata.com      # A string entry has no rate limit.
  - url: https://api.crustdata.com
    rate: 20                             # At most 20 requests per second to this target, across all runs.
```

A suite that sets a timeout over the maximum, in the run result:

```
suite surface/api/users.yaml: create-user: timeout 10m is over the limit of 5m
```

A run that hit the run time limit, in the CI log of a workflow that set `timeout: 45m`:

```
surface 1.2.0 run 01j9z3k4m5n6p7q8r9s0t1u2v3 target https://pr-123.preview.crustdata.com
surface/api/users.yaml: 4 passed
surface/api/search.yaml: 812 passed

run 01j9z3k4m5n6p7q8r9s0t1u2v3: error: run time limit of 30m passed
```

## Values

| Limit | Value | Set by |
| --- | --- | --- |
| The pool | 128 requests in flight across all runs | The operator, `limits.requests` |
| Runs in memory | 32 | The operator, `limits.runs` |
| The run time limit | 30m | The operator, `limits.run_time` |
| The rate of one target | No limit | The operator, `rate` on a target list entry |
| The maximum `timeout` of a test or a cleanup | 5m | Fixed |
| Cleanup time after the run time limit | 10m | Fixed |
| The response body that the runner reads | 10 MiB | Fixed |
| The run request | 16 MiB | Fixed ([runs.md](runs.md)) |
| A body in the failure text | 4 KiB | Fixed ([failure-text.md](failure-text.md)) |

The runner refuses to start when `limits.requests` or `limits.runs` is not a whole number of 1 or more, when `limits.run_time` is not a Go duration above zero, or when a `rate` is not a whole number of 1 or more.

## The pool

- The pool is the number of requests, tests and cleanups, that the runner has in flight at once, across all runs. `limits.requests` sets it. The default is 128.
- A request waits for a pool slot in the order it asked. The pool is one semaphore ([runner.md](runner.md)).
- There is no cap per run. A run with 2,000 tests that can run in parallel puts 2,000 requests in the line, and a second run's first request waits behind them. The wait is the time the pool needs to send them: about 2 seconds at 100 ms per request, and about 16 seconds at 1 second per request.
- A suite cannot change the pool or its own parallelism. `needs` orders tests ([suite-format.md](suite-format.md)).
- Why no cap per run: the wait is short at real request times, and a cap is one more setting for every operator. A run that waits a long, measured time behind another run is the trigger to add one.
- Why 128: the per-target rate limit is off by default, so the pool is the only default bound on the load one run puts on its target.

## Runs in memory

- `limits.runs` caps the runs that the runner holds in memory. The default is 32. Above the cap, `POST /v1/runs` answers `503` with `Retry-After: 5`, and the client sends the run request again ([runner.md](runner.md)).
- One run in memory holds up to 16 MiB of suite text plus the parsed suites. The operator sets the cap and the pod's memory limit together ([How does an organization install surface?](https://github.com/rajnandan1/surface/issues/13)).
- There is no cap on suites or tests per run. The 16 MiB cap on the run request, the cap on runs, and the run time limit bound a run.

## The per-target rate limit

- An entry in the target list is a target string, or a mapping with `url`, the target string, and `rate`, a whole number of requests per second. A string entry has no rate limit. There is no default rate.
- The runner sends at most `rate` requests per second to a target that matches the entry, across all runs. Tests and cleanups count. The runner spaces the requests evenly, one every `1/rate` seconds, with no burst.
- The runner keeps one rate per target, after the default port is filled in ([targets.md](targets.md)). With `url: https://*.preview.crustdata.com` and `rate: 50`, `https://pr-1.preview.crustdata.com` and `https://pr-2.preview.crustdata.com` each get 50 requests per second.
- When a target matches more than one entry, the lowest `rate` of the matching entries applies.
- A request first waits for its target's rate, then for a pool slot. So a request that waits for a slow target holds no pool slot, and the runs on other targets do not wait for it.
- The runner does not retry a `429`. A test that gets `429` sees it as its response.
- The runner has its own rate code, about 20 lines. `golang.org/x/time/rate` is at v0.x, and the library rule of [ADR 0002](../adr/0002-own-suite-format-and-engine.md) needs v1.0.0 or later.
- Why requests per second and not requests in flight: a target states its own limit in requests per second and answers `429` above it.
- Why off by default: the operator knows which targets have a limit of their own, for example production with a quota per API key. A preview environment has no quota, and the pool bounds it.

## Timeouts

- A test's `timeout` covers connect, send, wait, and read ([suite-format.md](suite-format.md)). It starts when the runner sends the request, after the request has had its rate turn and its pool slot. The time a request waits counts only against the run time limit.
- The maximum `timeout` is 5m, for a test, a cleanup, and `defaults.timeout`. It is fixed in v1.
- A suite with a larger `timeout` fails at load, with one line: `suite surface/api/users.yaml: create-user: timeout 10m is over the limit of 5m`. The other suites of the run still run ([runs.md](runs.md)).
- Why the clock starts at send: a busy runner would otherwise fail tests with `no response: timeout after 30s` when the target is fine.

## The run time limit

- The run time limit is the longest time a run may spend on its tests. `limits.run_time` sets it, as a Go duration. The default is 30m. It starts when the runner answers `202 Accepted`.
- When it passes while a test of the run has not ended, the runner starts no new test and lets the requests in flight finish or time out, as on a cancel ([data-safety.md](data-safety.md)). The cleanups then run, and the run's `state` becomes `error` with the `reason` `run time limit of 30m passed` ([runner.md](runner.md)).
- When every test had ended before the limit, the limit stops nothing, and the run's `state` follows the usual rules ([runs.md](runs.md)).
- The limit stops tests, not cleanups. Cleanups may start up to 10 minutes after the limit. That time is fixed in v1. A cleanup that has not started by then never starts. It is a failed cleanup with the one-line failure text `not sent: run time limit passed`, so the run is at least `failed`.
- The 10 minutes apply to every run, also after a cancel from the client. A stop on SIGTERM keeps its own bound, the grace period ([runner.md](runner.md)).
- So every run ends at most 15 minutes after its run time limit: 10 minutes for cleanups to start, and 5 minutes for the last cleanup to time out.
- Why 30m: the client's wait limit is 30m by default ([runs.md](runs.md)). A run whose client went away, for example a killed CI job, ends about when its client would have stopped waiting.
- Why cleanups get more time: test data left on the target is worse than 10 more minutes. A total bound stops one bad suite from holding a run slot for hours.

## The response body

- The runner reads at most 10 MiB of a response body. The value is fixed in v1.
- Above 10 MiB, the runner stops reading and closes the connection. The test fails with the check `body: response is over 10 MiB`. The status and header checks still run. The checks of `expect.body`, `expect.equal`, and `save` do not run, and `body: response is over 10 MiB` stands for all of them, like `body: not JSON` ([failure-text.md](failure-text.md)).
- The same rule applies to the response of a cleanup.
- The failure text prints the first 4 KiB of the body that the runner read ([failure-text.md](failure-text.md)).
- Why 10 MiB: one search or enrichment response of the first user holds up to 100 profiles, often 0.5 to 2 MiB. The memory for bodies is at most the pool times 10 MiB, which is 1.25 GiB at the default of 128.

## Known limits

- With both defaults at 30m, the client's wait limit usually passes a few seconds before the run time limit, because the client starts its clock before it sends the run request. The CI log then shows `did not finish within 30m`, and the run result holds `run time limit of 30m passed`.
- A large run delays the other runs by the time the pool needs to send its requests, because there is no cap per run.
- The runner does not limit who starts a run or how many runs one caller starts. That is decided in [Which callers may start a run?](https://github.com/rajnandan1/surface/issues/14).

## Decided elsewhere

- The pool as one semaphore, the `503` above the cap, and the restart: [runner.md](runner.md)
- The 16 MiB cap, the client's wait limit, and the cancel: [runs.md](runs.md)
- The 4 KiB cut and the check lines: [failure-text.md](failure-text.md)
- The target list and how a target matches an entry: [targets.md](targets.md)
- Cleanups and their order: [data-safety.md](data-safety.md)
- The pod's memory limit and the grace period: [How does an organization install surface?](https://github.com/rajnandan1/surface/issues/13)
- Which callers may start a run: [Which callers may start a run?](https://github.com/rajnandan1/surface/issues/14)
