# Runner

The runner is the surface service that receives suites and runs their tests. A run result is the record the runner keeps for one run, and `GET /v1/runs/<id>` returns it (see [GLOSSARY.md](../../GLOSSARY.md)). This doc defines the shape of the runner on Kubernetes, where a run's state lives while it runs and after it ends, what a restart does to a run, what happens under load, and how long a run result stays readable. The decisions come from [How is the runner deployed, and where do run results live?](https://github.com/rajnandan1/surface/issues/11) and [ADR 0010](../adr/0010-one-runner-process-with-one-replica-and-run-results-as-files.md).

## Example

The operator side, in the runner's config:

```yaml
results:
  dir: /var/lib/surface/results   # Run result files, one per run. The chart mounts a volume here. This is the default.
  retention: 168h                 # The runner deletes older run results. The default is 168h (7 days).
```

The directory after three runs:

```
/var/lib/surface/results/
  01j9z0a1b2c3d4e5f6g7h8j9k0.json
  01j9z1m2n3p4q5r6s7t8v9w0x1.json
  01j9z3k4m5n6p7q8r9s0t1u2v3.json
```

A run that a deploy of the runner interrupted, in the CI log:

```
surface 1.2.0 run 01j9z3k4m5n6p7q8r9s0t1u2v3 target https://pr-123.preview.crustdata.com
surface/api/users.yaml: 2 passed
surface/api/search.yaml: 1 passed

run 01j9z3k4m5n6p7q8r9s0t1u2v3: error: runner stopped
```

## The shape

- The runner is one Go process in one Kubernetes Deployment with one replica. Every run is a set of goroutines inside that process, one per request in flight, bounded by the pool in "Load" below.
- The Deployment uses the `Recreate` strategy: Kubernetes stops the old pod before it starts the new one, because the volume below attaches to one pod at a time. The client survives the gap: on a connection error or a `5xx` it retries at the next poll ([runs.md](runs.md)).
- The runner listens on plain HTTP on port 8080. TLS ends at the Ingress, and the Ingress URL is the `url` in the config and the token audience ([runs.md](runs.md)). GitHub-hosted runners have no fixed address, so the runner may sit on the public internet. Its guards are the ones in [runs.md](runs.md): the token check runs before the runner reads the body, the body is capped at 16 MiB, and every route but `GET /healthz` needs a valid token.
- `GET /healthz` answers `200` while the runner accepts runs and `503` after it got SIGTERM. The chart uses it for the liveness and readiness probes.
- The namespace, the ServiceAccount, and the Role are the ones in [secrets.md](secrets.md). One process keeps that RBAC as it is, because no second pod has to read Secrets.
- The runner writes its logs to stdout: one line when a run is accepted (the run id, the caller, the target) and one line when it ends (the run id, the state, the counts). It has no metrics endpoint in v1.
- Why one process and not a Kubernetes Job per run: a test is one HTTP request, so one Go process holds thousands in flight. A Job per run needs RBAC to create Jobs, the Secrets RBAC on every Job pod, two components to install, and 5 to 30 seconds of pod start per run. A suite can only send HTTP requests ([ADR 0001](../adr/0001-suites-are-untrusted-input.md)), so a run needs no process of its own for safety.

## Where a run's state lives

- A run in flight lives in memory. `GET /v1/runs/<id>` for a run in flight reads memory.
- The runner writes the run result as one JSON file, `<results.dir>/<run id>.json`, with the same JSON that `GET /v1/runs/<id>` returns. It writes the file when the run is accepted, when a suite ends, and when the run ends. Each write goes to a temporary file in the same directory, which the runner then renames over the old file, so a reader never sees a half-written file.
- When a run ends and its file is written, the runner drops the run from memory. `GET /v1/runs/<id>` for a finished run reads the file.
- A failed write is logged with the OS error, and the run keeps running. The runner keeps a run in memory until its final write succeeds, so `GET` still answers from memory. Such a run counts against the cap on runs in memory in "Load" below, so a full disk ends in `503` for new run requests, and the operator sees the OS error in the logs.
- `results.dir` is `/var/lib/surface/results` by default. The chart mounts a PersistentVolumeClaim there. An install that wants nothing to survive a restart mounts an `emptyDir` instead.
- The run id is a ULID, a 26-character id whose first 10 characters encode the time, so `ls` lists the files in time order ([runs.md](runs.md)).
- Why files and not a database: every Kubernetes cluster has a volume, so the core stays free of any vendor and any install dependency. The operator reads a run with `kubectl exec` and `cat`, with no UI and no token. A second replica would need a shared store. That is the trigger to add one, and it is not in v1.

## A restart

- On SIGTERM, the runner stops accepting run requests: `GET /healthz` and `POST /v1/runs` answer `503`. It then treats every run in flight like `POST /v1/runs/<id>/cancel`: it starts no new test, lets in-flight requests finish or time out, and runs the cleanups of the tests whose status check passed ([data-safety.md](data-safety.md)). Then it sets each run's `state` to `error` with the `reason` `runner stopped`, writes the files, and exits.
- The grace period that Kubernetes gives the pod bounds the cancel. The chart sets it ([How does an organization install surface?](https://github.com/rajnandan1/surface/issues/13)). When the grace period passes first, Kubernetes kills the pod, and the crash rule below applies.
- At start, the runner reads every file in `results.dir`. A file whose `state` is not final belongs to a run that a crash or a kill interrupted. The runner sets its `state` to `error` with the `reason` `runner restarted` and writes it back. The cleanups of that run did not run, and the data that is left on the target carries the run id ([data-safety.md](data-safety.md)).
- `reason` is a field of the run result, present only when `state` is `error`. It is one line. The client prints it as the last line of the CI log, `run <id>: error: <reason>`, and exits `2` ([runs.md](runs.md)).
- Why cancel and not drain: a drain holds the old pod for up to the run time limit, and with one replica and one volume no new run can start until it exits. A cancel fails the interrupted CI runs with a clear reason, and the developer re-runs the job. A drain can come later with no design change.

## Load

- The runner has no run queue. Every accepted run starts at once, and its `state` is `running`. The state `queued` exists in the run states and stays unused in v1 ([runs.md](runs.md)).
- The tests and cleanups of all runs share one pool of N requests in flight per runner: one semaphore. A request waits for a slot in the order it asked. Under load every run gets slower, and none waits in a queue. There is no cap per run, so a large run delays the others by the time the pool needs to send its requests ([limits.md](limits.md)). The trade-off: a run can hit its run time limit because the pool is full.
- The runner caps the runs it holds in memory. Above the cap, `POST /v1/runs` answers `503` with `Retry-After: 5` and makes no run. The client retries the run request every 5 seconds until its wait limit passes ([runs.md](runs.md)).
- N is `limits.requests`, 128 by default. The cap on runs in memory is `limits.runs`, 32 by default. Both are in [limits.md](limits.md), with the per-target rate limit.

## Retention

- `results.retention` is a Go duration. The default is `168h`, 7 days.
- At start and then once an hour, the runner deletes every run result file whose run id time is older than `results.retention`.
- After that, `GET /v1/runs/<id>` answers `404`, and the client exits `2` ([runs.md](runs.md)). The client never meets this, because its wait limit is far shorter than the retention.
- Why 7 days: in v1 the only reader after the client has printed the result is an operator who debugs a run, and a week covers that.
- Size: a run with 1,000 tests and 100 failures is about 1 MiB, because the run result holds one failure text of at most about 8 KiB per failed test and a few bytes per passed test ([failure-text.md](failure-text.md)). A volume of 1 GiB holds about 1,000 such runs. The chart sets the default size ([How does an organization install surface?](https://github.com/rajnandan1/surface/issues/13)).

## Known limits

- One replica. A deploy of the runner fails the runs in flight with `runner stopped`. A second replica needs a shared store, and that is the trigger to add one.
- The runner does not bound the disk. The operator sizes the volume. When it is full, writes fail, runs stay in memory, and new run requests get `503` once the cap is reached.
- The retention sweep runs once an hour, so a file can live up to an hour past `results.retention`.
- A crash loses the detail of the suites that were running. The file holds the run as it was at the last suite end, plus the `error` state and the reason.

## Decided elsewhere

- The run states, the wire shapes, the client, and the CI log: [runs.md](runs.md)
- Cleanups on cancel: [data-safety.md](data-safety.md)
- The namespace, the ServiceAccount, and the Secrets RBAC: [secrets.md](secrets.md)
- N, the caps, the timeouts, the run time limit, and the per-target rate limit: [limits.md](limits.md)
- The chart, the volume size, the grace period, the Ingress, and the release: [How does an organization install surface?](https://github.com/rajnandan1/surface/issues/13)
