# Surface

Surface runs end-to-end tests that live in product repos, outside the product's CI, at high parallelism. It reports what failed and why.

## Language

**Suite**:
One YAML file in a product repo that holds many tests.
_Avoid_: Spec, spec file, test file

**Test**:
One HTTP request plus the response it must get back.
_Avoid_: Case, check, spec

**Runner**:
The surface service that receives suites and runs their tests.
_Avoid_: Executor, worker, engine

**Run**:
One execution of a set of suites, started by one CI job.
_Avoid_: Job, execution, build

**Target**:
The base URL a run calls, such as production or an ephemeral environment.
_Avoid_: Host, environment, base URL
