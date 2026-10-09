# The runner is one process with one replica, and run results are files on a volume

The runner is one Go process in one Kubernetes Deployment with one replica. Every run is a set of goroutines inside it, and the requests of all runs share one pool of requests in flight. A run in flight lives in memory, and the runner writes the run result as one JSON file per run on a PersistentVolume, so finished runs survive a restart and the core needs no database. On SIGTERM the runner cancels the runs in flight, so their cleanups run, and marks them `error` with the reason `runner stopped`. Run result files are deleted after 7 days by default.

## Considered options

- **One Kubernetes Job per run, started by an API pod.** Rejected. A test is one HTTP request, so one Go process holds thousands in flight, and a suite can only send HTTP requests ([ADR 0001](0001-suites-are-untrusted-input.md)), so a run needs no process of its own for safety. A Job per run needs RBAC to create Jobs, the Secrets RBAC on every Job pod, two components to install, and 5 to 30 seconds of pod start per run.
- **Memory only.** Rejected. A restart loses every finished run, and the client gets a `404` with no reason. A file per run costs one write per suite end and gives the operator a run history with `ls`.
- **A database, with several replicas.** Rejected for v1. It adds an install dependency to the open-source core for a scale that a single-tenant runner does not reach. A second replica is the trigger to add a shared store.
- **Drain on SIGTERM: finish the runs in flight before exit.** Rejected. With one replica and one volume, no new run can start until the old pod exits, so a deploy would block runs for up to the run time limit. A cancel fails the interrupted runs with a clear reason, and their cleanups still run.
- **A run queue: at most M runs at once, the rest wait in `queued`.** Rejected. One semaphore over all requests in flight needs no scheduler and shares the pool fairly. A queued run would also wait against the client's 30-minute wait limit. The state `queued` stays in the run states and unused.
- **Reject a run request with `503` when the runner is busy.** Rejected as the first response. Under load every run gets slower instead. `503` is kept for one case, the cap on runs held in memory, and the client retries it every 5 seconds.

Sources: the resolution of [How is the runner deployed, and where do run results live?](https://github.com/rajnandan1/surface/issues/11).

## Consequences

- A deploy of the runner fails the CI runs in flight, with `runner stopped` in the CI log, and the developer re-runs the job.
- The run result gets a `reason` field, present only on `state: error`.
- The runner needs a volume. The chart mounts a PersistentVolumeClaim, and `emptyDir` works for an install that keeps nothing.
- The Secrets RBAC in [ADR 0005](0005-secret-groups-in-kubernetes-secrets.md) stays as it is, because one process reads the Secrets.
- The pool size, the cap on runs in memory, and the run time limit are decided in [What limits does the runner put on a run?](https://github.com/rajnandan1/surface/issues/12).
- The design is in [docs/design/runner.md](../design/runner.md).
