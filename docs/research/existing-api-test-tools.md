# Existing API test tools as surface's suite format or engine

Three tools read YAML, are written in Go, and give Go code a package to call in the same process: Venom, runn, and scenarigo. These three are the candidates for the runner's engine. Four other tools (Step CI, Hurl, Tavern, Karate) need Node.js, Rust, Python, or Java. The runner can only start them as a separate program, so they are useful as references for the suite format, not as an engine.

This note answers issue [#3](https://github.com/rajnandan1/surface/issues/3) on map [#1](https://github.com/rajnandan1/surface/issues/1). It does not choose a tool. Issue [#4](https://github.com/rajnandan1/surface/issues/4) ("Build on an existing tool or write our own runner?") makes that decision.

I read each tool's docs and source code on 2026-10-08. I did not run any tool. Every claim links to the doc page or the source line it comes from. Source links point to a fixed commit, so the line numbers stay correct.

## Names in this note

Surface uses five names. They come from map #1.

- **Suite**: one YAML file in a product repo. It holds many tests.
- **Test**: one HTTP request plus the response it must get back.
- **Runner**: the surface service that runs tests. It is written in Go and runs on Kubernetes.
- **Run**: one execution of a set of suites. One CI job starts it.
- **Target**: the base URL that a run calls.

Each tool has its own words for these ideas. This table maps them. A "group" is an ordered list of requests that share captured values.

| Tool | Name of one file | Name of one group | Name of one request plus its checks |
| --- | --- | --- | --- |
| Venom | The file is a `testsuite`. | A group is a `testcase`. | A request is a `step` with `type: http`. |
| runn | The file is a `runbook`. | The runbook itself is the only group. | A request is a `step`. |
| scenarigo | The file holds scenarios. | A group is a `scenario` (one YAML document). | A request is a `step`. |
| Step CI | The file is a `workflow`. | A group is an entry under `tests:`. | A request is a `step`. |
| Hurl | The file is a Hurl file. | The file itself is the only group. | A request is an `entry`. |
| Tavern | The file is a `test_*.tavern.yaml` file. | A group is a test document with a `test_name`. | A request is a `stage`. |
| Karate | The file is a `.feature` file. | A group is a `Scenario`. | A request is one `method` line plus its `match` lines. |

Other terms in this note:

- **YAML**: a text format for structured data. Surface suites use it.
- **JSONPath**: a query language that picks values out of a JSON document, for example `$.user.id`.
- **JMESPath**: a different JSON query language, for example `user.id`.
- **JSON Schema**: a standard JSON document that describes which keys and types another JSON document must have.
- **OpenAPI v3**: a standard file that describes an HTTP API: its paths, its requests, and the shape of its responses.
- **Gherkin**: the `Given` / `When` / `Then` text format that Cucumber made popular. Karate uses it.
- **pytest**: the standard Python test program. Tavern runs inside it.
- **expr-lang**: a small expression language for Go programs. runn uses it for checks.
- **JUnit XML** and **TAP**: two standard test report formats. Most CI systems can read JUnit XML.
- **Embed as a library**: the runner imports the tool's Go package and calls it in the same process.
- **Separate program**: the runner starts the tool as a child process and reads its output.
- **Apache-2.0** and **MIT**: permissive licenses. They allow use in an open-source project like surface.
- **MPL-2.0**: a file-level copyleft license. If you change a file of the tool, you must publish that file under MPL-2.0.

## Comparison table

The ticket asks 8 questions per tool. Table A answers questions 1 to 4. Table B answers questions 5 to 8. The sections after the tables give the details and the sources.

### Table A: format, variables, captures, matching

| Tool | Language and file format | 1. Does one file hold many tests? | 2. How does it declare variables and secrets? | 3. Can a test reuse a value from an earlier response? | 4. How does it match responses? |
| --- | --- | --- | --- | --- | --- |
| Venom | Venom is a Go program and Go package. It reads YAML. | Yes. One file holds many testcases, and each testcase holds many steps. | `vars:` declares variables. `--var`, `--var-from-file`, and `VENOM_VAR_<name>` set them. `secrets:` hides the listed values in output. | Yes. A step extracts a value with `vars: <name>: from:`. A later testcase reads it as `{{.<testcase>.<name>}}`. | Keywords such as `ShouldEqual` and `ShouldJSONEqual` check values. Venom uses its own dotted path, not JSONPath. Venom has no JSON Schema keyword. |
| runn | runn is a Go program and Go package. It reads YAML. | Partly. One file holds one runbook. The runbook holds many steps, and each step can carry a `test:` check. | `vars:` declares variables. `${NAME}` and `env` read environment variables. `secrets:` masks the listed values. | Yes. runn stores every response. A later step reads `steps[1].res.body.id`. | `test:` expressions check any value. `compare` and `diff` check whole objects. An OpenAPI v3 file can validate every response. runn has no JSON Schema check. |
| scenarigo | scenarigo is a Go program and Go package. It reads YAML. | Yes. One file holds many scenarios as YAML documents. Each scenario holds many steps. | `vars:` declares variables at three levels. `{{env.NAME}}` reads environment variables. `secrets:` values are masked in output. | Yes, inside one scenario. `bind:` saves a value for the later steps. | `expect:` lists exact values per key. Template expressions and `assert.*` functions check types, patterns, and ranges. scenarigo has no JSON Schema check. |
| Step CI | Step CI is a Node.js program and Node.js library. It reads YAML. | Yes. One workflow holds many `tests` entries, and each entry holds many steps. | `env:` and `--env` set variables. `--secret` sets secrets. The docs do not say that output hides secrets. | Yes, inside one `tests` entry. `captures` use JSONPath, headers, or regex. Captures from `before` reach every entry. | `check.json` compares the whole body. `check.schema` uses JSON Schema. `check.jsonpath` and matchers such as `isNumber` check parts. |
| Hurl | Hurl is a Rust program and Rust library. It reads its own plain-text format, not YAML. | Yes. One file holds many entries. | `--variable`, `--variables-file`, and `HURL_VARIABLE_<name>` set variables. `--secret` sets values that Hurl redacts from logs and reports. | Yes. `[Captures]` stores a value for the later entries in the same file. | A body block compares the exact body. `[Asserts]` uses JSONPath with predicates such as `==`, `exists`, and `isInteger`. Hurl has no JSON Schema predicate. |
| Tavern | Tavern is a Python pytest plugin and Python library. It reads YAML. | Yes. One file holds many test documents, and each document holds many stages. | Config files hold variables. `{tavern.env_vars.NAME}` reads the environment. Tavern has no masking feature. | Yes, inside one test document. `save:` uses JMESPath. | `json:` matches exact values, and strict key checking is on by default. Type tokens such as `!anyint` check types. A helper checks pykwalify schemas, not JSON Schema. |
| Karate | Karate is a Java 21 program and Java library. It reads Gherkin `.feature` files, not YAML. | Yes. One feature file holds many scenarios. | `karate-config.js` returns variables. It reads the environment with `java.lang.System.getenv`. `configure logging = { mask: … }` masks values in logs. | Yes, inside one scenario. `def id = response.id` saves a value. `callonce` shares setup results across scenarios. | `match ==` compares exact values. Markers such as `#string`, `#notnull`, and `#regex` check types and patterns. Karate's own markers replace JSON Schema. |

### Table B: cleanup, Go use, license, failure report

| Tool | 5. Does it run setup and cleanup, also when a test fails? | 6. Can Go code embed it as a library? | 7. What is its license, and is it maintained? | 8. What does a failure report look like? |
| --- | --- | --- | --- | --- |
| Venom | Venom has no setup or cleanup block. A failed `Should` check does not stop later steps or testcases, so a last cleanup testcase still runs. `Must` checks and `--stop-on-failure` stop the rest. | Yes, with limits. Go code can call `venom.New`, `Parse`, and `Process`. `Process` runs suites one after another. `InitLogger` changes the global logger. | Apache-2.0. The last stable release is v1.3.0 (2026-01-06). Betas continue to v1.4.0-beta.4 (2026-10-01). | Each failed step prints `Testcase "<name>", step #<n>-<i>: Assertion "<check>" failed. <reason> (<file>:<line>)`. Reports are xUnit XML, JSON, YAML, TAP, or HTML. |
| runn | A step with `defer: true` always runs at the end, also after a failure. The docs mark `defer` as an experiment. `needs:` runs other runbooks first. | Yes. Go code can call `runn.Load` and `RunN`. `runn.RunConcurrent` runs runbooks in parallel. | MIT. Releases are v1.11.1 (2026-09-28), v1.11.0 (2026-09-18), and v1.10.0 (2026-09-01). | runn prints the file, `Failure/Error:` with the check and its values (`404 == 200 => false`), and the YAML of the failed step. `--debug-on-failure` adds the request and the response. |
| scenarigo | A failed step stops its scenario by default. `continueOnError` and `if:` can run a step after a failure. An always-run cleanup needs a Go plugin. | Yes. Go code can call `scenarigo.NewRunner` and `Run`. scenarigo runs each file in parallel. It can load YAML from memory. | Apache-2.0. Releases are v0.27.0 (2026-09-29), v0.26.2 (2026-06-11), and v0.26.1 (2026-05-12). Version 0.x still makes breaking changes. | scenarigo prints the request, the response, each failed check as `expected "hello" but got "bye"`, and the suite lines with a caret under the failing key. Reports are JSON or JUnit XML. |
| Step CI | `before` and `after` sections run once. The source starts `after` when all `tests` entries end, pass or fail. A failed step skips the rest of its entry. | No. The library needs Node.js. Go can only start the CLI as a separate program. | MPL-2.0. The last release is 2.8.2 (2024-06-10). The last commit in `stepci/runner` is from 2025-04-05. | Step CI prints `FAIL <name>`, the request, the response, and an "Expected" block and a "Given" block for each failed check. |
| Hurl | Hurl has no setup or cleanup feature. It stops a file at the first failed assert, unless `--continue-on-error` is set. | No. The library is a Rust crate. Go can only start `hurl` as a separate program. | Apache-2.0. Releases are 8.0.1 (2026-04-29), 8.0.0 (2026-04-27), and 7.1.0 (2025-11-20). Commits land almost daily. | Hurl prints a compiler-style error: the file, the line, the source text, a caret, and `actual value is <...>`. Reports are HTML, JSON, JUnit XML, or TAP. |
| Tavern | A `finally:` block runs its stages after the test document, pass or fail. | No. Tavern needs Python. Go can only start `pytest` as a separate program. | MIT. PyPI releases are 3.7.0 (2026-09-25), 3.6.3 (2026-08-31), and 4.0.0rc1 (2026-08-25). | pytest prints the stage YAML, the formatted request, and `Key mismatch: (expected[...] = ..., actual[...] = ...)`. The docs example also prints a bearer token in clear text. |
| Karate | `Background` and `callonce` run setup. `configure afterScenario` runs after each scenario, pass or fail. | No. Karate needs Java 21. Go can only start the `karate` CLI as a separate program. | MIT. Releases are v2.1.3 (2026-09-27), v2.1.2 (2026-08-14), and v2.1.1 (2026-07-15). | Karate prints `match failed: EQUALS` and one line per wrong path, for example `$.name | not equal (STRING:STRING)`. The HTML report shows full requests and responses. |

## Tools that could be adopted

This list does not rank the tools for a decision. Issue #4 decides.

1. **Venom** could be the engine or the format. Its file shape is closest to a surface suite: one YAML file holds many testcases, and captured values flow to later testcases. The gaps are: suites run one after another inside `Process`, the logger is global, there is no JSON Schema check, and there is no cleanup block.
2. **runn** could be the engine. It has a real Go API with parallel runs and an always-run cleanup step. The gaps are: one file holds one runbook, the cleanup step is still an experiment, and there is no JSON Schema check outside OpenAPI v3.
3. **scenarigo** could be the engine. It has a Go API, parallel files, YAML loading from memory, and the clearest failure report of the Go tools. The gaps are: it is still at version 0.x, it has no cleanup step in YAML, and it has no JSON Schema check.
4. **Tavern** and **Step CI** could be references for the suite format only. The original suite sketch on map #1 has a `matchTypeBody` field with three levels: `strict` (schema, types, and values), `standard` (schema and types), and `loose` (schema only). The matching rules of Tavern and Step CI come closest to these levels. Tavern has strict key checking and a `finally:` block. Step CI has exact, JSON Schema, and JSONPath checks side by side. Step CI looks unmaintained.

Hurl and Karate do not read YAML and do not run in Go. They are weaker fits for both roles.

## Details and sources

### Venom (ovh/venom)

Repo: https://github.com/ovh/venom. Commit read: [`b3af240`][v-root].

1. **Many tests per file.** A testsuite is one YAML file with a list of testcases. Each testcase is an ordered list of steps. Venom runs the testcases in a suite one after another. [README][v-suite]
2. **Variables and secrets.** The suite declares each variable under `vars:`. `--var`, `--var-from-file`, and environment variables override them. [README][v-vars] An environment variable `VENOM_VAR_<name>` sets the variable `<name>`. [README][v-env] Values of variables listed under `secrets:` do not appear in the console, `venom.log`, or dump files. [README][v-secrets]
3. **Captures.** A step extracts a value with `vars: <name>: from: <path>`, with an optional regex. A later testcase reads it as `{{.<testcase>.<name>}}`. [README][v-capture] The code adds each testcase's values to the suite under the testcase name. [process_testsuite.go][v-addvars]
4. **Matching.** Assertions are lines such as `result.statuscode ShouldEqual 200`. Keywords include `ShouldEqual`, `ShouldContainKey`, `ShouldMatchRegex`, `ShouldJSONEqual`, and `ShouldJSONContain`. [README][v-assert] The HTTP executor exposes the JSON body as `result.bodyjson`. Paths use dots and an index suffix, for example `result.bodyjson.apis.apis1.path`. Venom lowercases JSON keys. `__Type__` and `__Len__` check type and length. [HTTP executor README][v-bodyjson] I found no JSON Schema keyword in the keyword list or the Go code.
5. **Setup and cleanup.** Venom has no setup or cleanup block. A failed `Should` check marks the step as failed, and the next step still runs. A failed `Must` check, or `--stop-on-failure`, skips the remaining steps. [process_testcase.go][v-stepfail] With `--stop-on-failure`, Venom also skips the remaining testcases. [process_testsuite.go][v-stop] So a cleanup testcase at the end of a suite runs after a `Should` failure, but not after a `Must` failure in its own testcase.
6. **Go library.** The CLI calls `venom.New()`, `v.Parse(ctx, paths)`, `v.Process(ctx, paths)`, and `v.OutputResult()`. [cmd.go][v-cmd] [venom.go][v-new] Go code can call the same functions. `Process` runs the parsed suites in a plain loop, one after another. [process.go][v-process] `InitLogger` sets the level, output, and formatter of the global logrus logger. [process.go][v-logger] `Parse` takes file paths, so the runner would write suites to disk first. Parallel suites would need one `Venom` value per goroutine, and they would share the global logger.
7. **License and maintenance.** The license is Apache-2.0. [LICENSE][v-license] The latest stable release is v1.3.0 (2026-01-06). Pre-releases v1.4.0-beta.1 (2026-03-31), v1.4.0-beta.2 (2026-09-07), and v1.4.0-beta.4 (2026-10-01) followed. The last commit is from 2026-10-01. The repo has about 1,200 stars. [releases][v-releases]
8. **Failure report.** Each failure reads `Testcase %q, step #%d-%d: Assertion %q failed. %s (%v:%d)`. The last part is the suite file and line. [types.go][v-failure] Without `-v`, the console prints the failed steps under each failed testcase. [process_testsuite.go][v-print] Venom writes xUnit XML by default. It also writes JSON, YAML, TAP, and HTML. [README][v-reports]

[v-root]: https://github.com/ovh/venom/tree/b3af240286ab2c24e6c5d6b7360d968112453599
[v-suite]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/README.md?plain=1#L300-L353
[v-vars]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/README.md?plain=1#L495-L531
[v-env]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/README.md?plain=1#L218-L231
[v-secrets]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/README.md?plain=1#L640-L657
[v-capture]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/README.md?plain=1#L579-L606
[v-addvars]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/process_testsuite.go#L204
[v-assert]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/README.md?plain=1#L659-L726
[v-bodyjson]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/executors/http/README.md?plain=1#L151-L180
[v-stepfail]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/process_testcase.go#L381-L398
[v-stop]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/process_testsuite.go#L188-L203
[v-cmd]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/cmd/venom/run/cmd.go#L327-L400
[v-new]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/venom.go#L43-L76
[v-process]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/process.go#L182-L196
[v-logger]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/process.go#L18-L56
[v-license]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/LICENSE
[v-releases]: https://github.com/ovh/venom/releases
[v-failure]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/types.go#L292-L318
[v-print]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/process_testsuite.go#L174-L187
[v-reports]: https://github.com/ovh/venom/blob/b3af240286ab2c24e6c5d6b7360d968112453599/README.md?plain=1#L835-L848

### runn (k1LoW/runn)

Repo: https://github.com/k1LoW/runn. Commit read: [`651af61`][r-root]. runn is not in the ticket. I added it because it is a Go tool that reads YAML and has a Go API.

1. **Many tests per file.** One file is one runbook. A runbook is one scenario: an ordered list of steps. Each step can send an HTTP request and carry a `test:` check. [README][r-runbook] The loader parses one runbook per file. [book.go][r-loadbook] So a suite with many independent groups would need many files, or one long runbook.
2. **Variables and secrets.** `vars:` declares variables, and a value can read the environment with `${NAME}`. [README][r-vars] The `env` object also holds the environment, as a snapshot from the start of the runbook. [README][r-stored] `secrets:` lists variable names whose values runn masks. [README][r-vars] Go code can pass `runn.Var` and `runn.Secret` options. [option.go][r-var-opt]
3. **Captures.** runn stores the result of every step. A later step reads it as `steps[<index>].res.body...` or `steps.<key>.res.body...`. [README][r-steps] `needs:` runs another runbook first and exposes its bound values. [README][r-needs]
4. **Matching.** The `test` runner evaluates an expression on stored values, for example `steps[1].res.status == 200`. [README][r-test] The expression language is expr-lang. Built-in `compare` and `diff` compare two whole values and can ignore paths given in jq syntax. [README][r-expr] An HTTP runner can take an OpenAPI v3 file and then validate every request and response against it. [README][r-openapi] I found no JSON Schema check outside the OpenAPI v3 feature. The only JSON Schema code validates the runbook file itself. [README][r-schema]
5. **Setup and cleanup.** A step with `defer: true` runs at the end of the runbook. Deferred steps always run, even if an earlier step fails. They run in last-in, first-out order. The docs label this feature "[THIS IS EXPERIMENT]". `force: true` makes a step run even after a failure. [README][r-defer]
6. **Go library.** Go code calls `runn.Load(pattern, opts...)` and then `RunN(ctx)`. `runn.Runner("req", url)` sets the base URL of a runner, which fits a surface target. [README][r-go] [operator.go][r-load] `runn.RunConcurrent(enable, max)` runs runbooks in parallel. [option.go][r-concurrent] `concurrency:` in a runbook stops two runbooks with the same key from running at the same time. [README][r-concurrency-key]
7. **License and maintenance.** The license is MIT. [README][r-license] Releases are v1.11.1 (2026-09-28), v1.11.0 (2026-09-18), and v1.10.0 (2026-09-01). [CHANGELOG][r-changelog] The last commit is from 2026-09-28. The repo has about 650 stars. [releases][r-releases]
8. **Failure report.** For each failed runbook, runn prints `<n>) <file> <id>`, then `Failure/Error: <message>`, then `Failure step (<file>):` and the YAML of the failed step. [result.go][r-result] For a failed `test:`, the message is `condition is not true` plus the expression tree with values, for example `current.res.status == 200` then `=> 404 == 200` then `=> false`. [test.go][r-testgo] [golden file][r-golden] `--debug-on-failure` adds the request and response of failed steps. [README][r-debug]

[r-root]: https://github.com/k1LoW/runn/tree/651af61c6e8d976c2a81f4cab550fdda999bf8a5
[r-runbook]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L285-L330
[r-loadbook]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/book.go#L103-L107
[r-vars]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L447-L468
[r-stored]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L810-L828
[r-var-opt]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/option.go#L680-L730
[r-steps]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L606-L640
[r-needs]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L594-L604
[r-test]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L1848-L1859
[r-expr]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L2212-L2221
[r-openapi]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L962-L976
[r-schema]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L371-L379
[r-defer]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L775-L808
[r-go]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L141-L178
[r-load]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/operator.go#L1546
[r-concurrent]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/option.go#L1169-L1182
[r-concurrency-key]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L578-L592
[r-license]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L2540-L2545
[r-changelog]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/CHANGELOG.md?plain=1#L1-L30
[r-releases]: https://github.com/k1LoW/runn/releases
[r-result]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/result.go#L207-L231
[r-testgo]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/test.go#L30
[r-golden]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/testdata/result_out_json_4.golden#L21
[r-debug]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L478-L485

### scenarigo (scenarigo/scenarigo)

Repo: https://github.com/scenarigo/scenarigo. Commit read: [`4c291b4`][s-root]. scenarigo is not in the ticket. I added it because it is a Go tool that reads YAML and has a Go API. The runn README names it as the alternative to runn. [runn README][r-alt]

1. **Many tests per file.** A scenario is a list of steps that run from top to bottom. Each step is one request and its expected response. [README][s-steps] The loader reads every YAML document in a file as a separate scenario. [schema/load.go][s-load]
2. **Variables and secrets.** `vars:` declares variables in a scenario, in a step, or globally in `scenarigo.yaml`. [README][s-vars] `secrets:` works like `vars:`, and scenarigo masks the values in output. [README][s-secrets] The `env` object holds environment variables. [README][s-predef]
3. **Captures.** `bind: vars:` saves a value from the response for the later steps of the same scenario, for example `msg: '{{response.body.text}}'`. `bind: secrets:` saves a masked value. [README][s-vars] [README][s-secrets]
4. **Matching.** `expect:` takes `code`, `header`, and `body`. A plain value means exact equality. A template expression with `$` checks a condition, for example `'{{int($) > 0}}'`. `assert.*` functions check `notZero`, `regexp`, `length`, ranges, `contains`, `and`, and `or`. [README][s-expect] The code builds one check per key listed under `expect:`. [assert/assertion.go][s-build] I read this to mean that keys not listed in `expect:` are not checked. I found no JSON Schema check.
5. **Setup and cleanup.** By default, a failed step stops the rest of its scenario. `continueOnError: true` lets the scenario go on, and `if:` can run a step only when an earlier step failed. [README][s-continue] A Go plugin can register a setup function with `plugin.RegisterSetup`. Its returned function runs after all tests. `plugin.RegisterSetupEachScenario` runs before each scenario. [README][s-setup] I found no YAML step that always runs at the end of a scenario.
6. **Go library.** Go code calls `scenarigo.NewRunner(opts...)` and then `Run(ctx)`. [runner.go][s-newrunner] `WithScenariosFromReader` loads scenarios from an `io.Reader`, so the runner does not need files on disk. [runner.go][s-reader] `Run` starts each file as a parallel subtest. The scenarios inside one file run one after another. [runner.go][s-run] The CLI wraps `Run` in `reporter.Run`. [cmd/run.go][s-cli] `reporter.WithMaxParallel` sets the parallel limit. [reporter/context.go][s-maxpar]
7. **License and maintenance.** The license is Apache-2.0. [LICENSE][s-license] Releases are v0.27.0 (2026-09-29), v0.26.2 (2026-06-11), and v0.26.1 (2026-05-12). The last commit is from 2026-10-08. The repo has about 370 stars. [releases][s-releases] v0.27.0 contains a breaking change marked `feat!`. [CHANGELOG][s-changelog]
8. **Failure report.** The output follows Go test style: `--- FAIL: <file>/<scenario>/<step>`. Under the failed step, scenarigo prints the full request, the full response, and each failed check. A failed check shows `expected "hello" but got "bye"` and the suite lines around the key, with a caret under it. [e2e golden output][s-failure] `scenarigo.yaml` can also write JSON and JUnit XML reports. [README][s-config]

[s-root]: https://github.com/scenarigo/scenarigo/tree/4c291b4991ef6adc93063ba5d72f5fa056a61568
[r-alt]: https://github.com/k1LoW/runn/blob/651af61c6e8d976c2a81f4cab550fdda999bf8a5/README.md?plain=1#L2530-L2532
[s-steps]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/README.md?plain=1#L214-L216
[s-load]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/schema/load.go#L185-L201
[s-vars]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/README.md?plain=1#L1016-L1083
[s-secrets]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/README.md?plain=1#L1085-L1158
[s-predef]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/README.md?plain=1#L1840-L1852
[s-expect]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/README.md?plain=1#L286-L465
[s-build]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/assert/assertion.go#L97-L134
[s-continue]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/README.md?plain=1#L1279-L1327
[s-setup]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/README.md?plain=1#L2097-L2193
[s-newrunner]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/runner.go#L50
[s-reader]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/runner.go#L138-L146
[s-run]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/runner.go#L277-L320
[s-cli]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/cmd/scenarigo/cmd/run.go#L77-L121
[s-maxpar]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/reporter/context.go#L16
[s-license]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/LICENSE
[s-releases]: https://github.com/scenarigo/scenarigo/releases
[s-changelog]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/CHANGELOG.md?plain=1#L3-L9
[s-failure]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/test/e2e/testdata/testcases/stdout/assert/assert.txt
[s-config]: https://github.com/scenarigo/scenarigo/blob/4c291b4991ef6adc93063ba5d72f5fa056a61568/README.md?plain=1#L116-L148

### Step CI (stepci/stepci and stepci/runner)

Repos: https://github.com/stepci/stepci (CLI and docs, commit [`e9af898`][c-root]) and https://github.com/stepci/runner (engine, commit [`01b236f`][cr-root]).

1. **Many tests per file.** A workflow holds many `tests` entries. Each entry has many steps. The entries run at the same time. The steps of one entry run in order. [concepts.md][c-concepts]
2. **Variables and secrets.** `env:` declares variables in the workflow. `--env` adds more on the command line. `--secret` supplies secrets. [templating.md][c-templ] [cli.md][c-cli] The docs do not say that Step CI hides secret values in its output.
3. **Captures.** A step captures values with `jsonpath`, `header`, `regex`, `cookie`, and other sources. [workflow-syntax.md][c-captures] Captures stay inside one `tests` entry. Captures from the `before` section reach all entries and `after`. [setup-teardown.md][c-setup]
4. **Matching.** `check.json` compares the whole body with deep equality. `check.schema` validates the body against a JSON Schema. `check.jsonpath` checks single values. [workflow-syntax.md][c-checks] [matcher.ts][cr-matcher] Matchers such as `eq`, `gt`, `match`, `isNumber`, and `isString` check types and ranges. [matcher.ts][cr-matcher]
5. **Setup and cleanup.** `before` runs before all `tests` entries, and `after` runs after them. Their steps count as tests in the results. [setup-teardown.md][c-setup] The engine awaits all entries and then runs `after`, whatever their results. [index.ts][cr-index] Inside one entry, a failed step skips the remaining steps. [concepts.md][c-concepts]
6. **Go library.** The library `@stepci/runner` is a Node.js package with `run` and `runFromFile`. [using-library.md][c-lib] Go code can only start the `stepci` CLI as a separate program. `config.concurrency` limits parallel entries, and the default is no limit. [workflow-syntax.md][c-config] [index.ts][cr-index]
7. **License and maintenance.** Both repos use MPL-2.0. [LICENSE][c-license] The last release is 2.8.2 (2024-06-10). The last commit in `stepci/stepci` is from 2024-08-03. The last commit in `stepci/runner` is from 2025-04-05, and it bumps vulnerable dependencies. [releases][c-releases] [runner commits][cr-commits] The CLI collects anonymous usage data unless `STEPCI_DISABLE_ANALYTICS` is set. [cli.md][c-analytics]
8. **Failure report.** The CLI prints `FAIL <name>` with timing and byte counts. For each failed step, it prints the request, the response, and each check. A failed check shows an "Expected" block and a "Given" block. A summary counts failed and passed entries and steps. [render.ts][c-render] [index.ts (CLI)][c-cli-index]

[c-root]: https://github.com/stepci/stepci/tree/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50
[cr-root]: https://github.com/stepci/runner/tree/01b236fa2b19b5ce98b027259ed5311975861609
[c-concepts]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/docs/guides/concepts.md?plain=1#L7-L13
[c-templ]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/docs/reference/templating.md?plain=1#L25-L30
[c-cli]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/docs/reference/cli.md?plain=1#L68-L69
[c-captures]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/docs/reference/workflow-syntax.md?plain=1#L831-L912
[c-setup]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/docs/guides/setup-teardown.md
[c-checks]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/docs/reference/workflow-syntax.md?plain=1#L973-L1006
[cr-matcher]: https://github.com/stepci/runner/blob/01b236fa2b19b5ce98b027259ed5311975861609/src/matcher.ts#L32-L75
[cr-index]: https://github.com/stepci/runner/blob/01b236fa2b19b5ce98b027259ed5311975861609/src/index.ts#L252-L273
[c-lib]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/docs/guides/using-library.md
[c-config]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/docs/reference/workflow-syntax.md?plain=1#L42-L52
[c-license]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/LICENSE
[c-releases]: https://github.com/stepci/stepci/releases
[cr-commits]: https://github.com/stepci/runner/commits/main
[c-analytics]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/docs/reference/cli.md?plain=1#L6
[c-render]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/src/lib/render.ts#L85-L175
[c-cli-index]: https://github.com/stepci/stepci/blob/e9af898c60711fca6d2c0b5e0003e4cfd63d7e50/src/index.ts#L22

### Hurl (Orange-OpenSource/hurl)

Repo: https://github.com/Orange-OpenSource/hurl. Commit read: [`738db67`][h-root]. Official docs: https://hurl.dev.

1. **Many tests per file.** A Hurl file is a list of entries. Each entry is one request, optionally followed by one expected response. [entry.md][h-entry] The format is plain text with a `.hurl` extension, not YAML. [hurl-file.md][h-file]
2. **Variables and secrets.** `--variable`, `--variables-file`, `HURL_VARIABLE_<name>`, an `[Options]` section, or a config file set variables. [templates.md][h-vars] `--secret`, `--secrets-file`, or `HURL_SECRET_<name>` set secrets. Hurl redacts secret values from logs and reports, but not from the HTTP response that it prints on standard output. [templates.md][h-secrets] [manual.md][h-manual]
3. **Captures.** A `[Captures]` section stores values from the response, for example with `jsonpath` or `xpath`. Later entries in the same run read them as `{{name}}`. [capturing-response.md][h-captures]
4. **Matching.** A JSON body block after the status line compares the body. [asserting-response.md][h-body] `[Asserts]` checks queries such as `jsonpath "$.id"` with predicates such as `==`, `exists`, `isString`, and `isInteger`. Each predicate can be negated with `not`. [asserting-response.md][h-predicates] [asserting-response.md][h-jsonpath] I found no JSON Schema predicate in the docs.
5. **Setup and cleanup.** I found no setup or cleanup feature in the docs. By default, Hurl stops a file after the first failed assert. `--continue-on-error` runs the remaining entries. [manual.md][h-manual]
6. **Go library.** The `hurl` package has a Rust library target. [Cargo.toml][h-crate] Go code can only start the `hurl` CLI as a separate program. `--test` mode runs files in parallel, and `--jobs` sets the limit. [manual.md][h-manual]
7. **License and maintenance.** The license is Apache-2.0. [Cargo.toml][h-crate] Releases are 8.0.1 (2026-04-29), 8.0.0 (2026-04-27), and 7.1.0 (2025-11-20). The last commit is from 2026-10-08. The repo has about 19,000 stars. [releases][h-releases]
8. **Failure report.** Hurl prints `error: Assert body value`, then `--> <file>:<line>:<column>`, then the request line and the failing source line with carets under it, then `actual value is <...>`. A summary counts files, requests, and failures. [integration test output][h-failure] `--report-html`, `--report-json`, `--report-junit`, and `--report-tap` write reports. [manual.md][h-reports]

[h-root]: https://github.com/Orange-OpenSource/hurl/tree/738db67fffd2b5c7703c7778229d08174f44c792
[h-entry]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/entry.md?plain=1#L1-L9
[h-file]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/hurl-file.md?plain=1#L1-L12
[h-vars]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/templates.md?plain=1#L147-L155
[h-secrets]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/templates.md?plain=1#L216-L240
[h-manual]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/manual.md?plain=1#L238-L250
[h-captures]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/capturing-response.md?plain=1#L1-L30
[h-body]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/asserting-response.md?plain=1#L183-L230
[h-predicates]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/asserting-response.md?plain=1#L384-L415
[h-jsonpath]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/asserting-response.md?plain=1#L772-L790
[h-crate]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/packages/hurl/Cargo.toml#L1-L14
[h-releases]: https://github.com/Orange-OpenSource/hurl/releases
[h-failure]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/integration/hurl/tests_ok/test/test.err.pattern
[h-reports]: https://github.com/Orange-OpenSource/hurl/blob/738db67fffd2b5c7703c7778229d08174f44c792/docs/manual.md?plain=1#L259-L262

### Tavern (taverntesting/tavern)

Repo: https://github.com/taverntesting/tavern. Commit read: [`26e41cd`][t-root].

1. **Many tests per file.** A test document has a `test_name` and a list of stages. Each stage has a request and an expected response. The file name must match `test_*.tavern.yaml`. [basics.md][t-anatomy] One file can hold many test documents, and a document with `is_defaults: true` shares config with the documents after it. [basics.md][t-multi]
2. **Variables and secrets.** `{tavern.env_vars.NAME}` reads an environment variable. [basics.md][t-env] Config files can include shared `key: value` variables. [config.md][t-include] I found no feature that masks secret values. The debugging example in the docs prints a bearer token in clear text. [debugging.md][t-debug]
3. **Captures.** `save:` stores values from the JSON body, headers, or redirect query. It uses JMESPath queries. Later stages of the same test document read them as `{name}`. [basics.md][t-save]
4. **Matching.** The `json:` block of a response checks values. Strict key checking is on by default for JSON, so extra keys in the response fail the stage. It is off by default for headers. [types.md][t-strict] Type tokens such as `!anyint`, `!anystr`, and `!anything` match any value of a type. [types.md][t-tokens] Helper functions check a pykwalify schema, JMESPath comparisons, or a pydantic model. [external_code.md][t-ext] [helpers.py][t-helpers] pykwalify schemas are a different format from JSON Schema.
5. **Setup and cleanup.** A `finally:` block holds stages that always run after the test document, pass or fail. If a `finally` stage fails, the rest of the `finally` stages do not run. [flow.md][t-finally]
6. **Go library.** Tavern is a pytest plugin, a command-line tool, and a Python library. [README][t-readme] Go code can only start `pytest` or `tavern-ci` as a separate program. The docs suggest `pytest-xdist` for parallel runs. [cookbook.md][t-xdist]
7. **License and maintenance.** The license is MIT. [LICENSE][t-license] The repo has no GitHub releases. PyPI lists 3.7.0 (2026-09-25), 3.6.3 (2026-08-31), and the pre-release 4.0.0rc1 (2026-08-25). [PyPI][t-pypi] The last commit is from 2026-09-25.
8. **Failure report.** pytest prints the format variables, the source stage YAML with its line number, the formatted stage, and then `Errors:` with `Key mismatch: (expected["data"] = ..., actual["data"] = ...)`. A captured log and a Python traceback follow. [debugging.md][t-debug]

[t-root]: https://github.com/taverntesting/tavern/tree/26e41cd1573e5562d87c663f990aafdcd23a7701
[t-anatomy]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/basics.md?plain=1#L3-L30
[t-multi]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/basics.md?plain=1#L275-L280
[t-env]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/basics.md?plain=1#L242-L273
[t-include]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/core_concepts/config.md?plain=1#L204-L215
[t-debug]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/debugging.md?plain=1#L120-L175
[t-save]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/basics.md?plain=1#L120-L170
[t-strict]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/core_concepts/types.md?plain=1#L1-L17
[t-tokens]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/core_concepts/types.md?plain=1#L285-L295
[t-ext]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/core_concepts/external_code.md?plain=1#L100-L116
[t-helpers]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/tavern/helpers.py#L103-L240
[t-finally]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/core_concepts/flow.md?plain=1#L91-L123
[t-readme]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/README.md?plain=1#L7-L17
[t-xdist]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/docs/source/cookbook.md?plain=1#L13-L16
[t-license]: https://github.com/taverntesting/tavern/blob/26e41cd1573e5562d87c663f990aafdcd23a7701/LICENSE
[t-pypi]: https://pypi.org/project/tavern/#history

### Karate (karatelabs/karate)

Repo: https://github.com/karatelabs/karate. Commit read: [`d67ecf7`][k-root]. Official docs: https://docs.karatelabs.io. This note describes Karate v2.

1. **Many tests per file.** A `.feature` file is written in Gherkin and holds many `Scenario` blocks. [feature files][k-features] The test file is not YAML.
2. **Variables and secrets.** `karate-config.js` returns the variables for every scenario. `karate.env` selects an environment. The config reads secrets with `java.lang.System.getenv('NAME')`. [configuration][k-config] `configure logging = { mask: … }` masks headers, JSON paths, or regex patterns in logs and reports. [CLI.md][k-mask]
3. **Captures.** `def` saves a value from the response, for example `def total = response.total`. Later steps of the same scenario read it. [variables][k-vars] `callonce` and `karate.callSingle()` run setup once and share its result. [hooks][k-hooks]
4. **Matching.** `match response == {...}` compares values. Markers such as `#string`, `#number`, `#notnull`, `#present`, `#uuid`, `#regex`, and `#? <expression>` check types and patterns. [fuzzy matching][k-fuzzy] Karate calls its marker-based checks "schema validation". The docs present them as a replacement for JSON Schema. [schema validation][k-schema]
5. **Setup and cleanup.** `Background` runs before every scenario. `callonce` runs once per feature. `configure afterScenario` runs after each scenario, on both the pass path and the fail path. `configure afterFeature` runs after all scenarios of a feature. [hooks][k-hooks]
6. **Go library.** Karate v2 needs Java 21 or newer. [README_V2.md][k-v2] Java code calls `Runner.path(...).parallel(n)`. [Java API][k-java] The `karate` binary is a Rust launcher that hands test runs to the JVM. [CLI.md][k-cli] Go code can only start the CLI as a separate program.
7. **License and maintenance.** The license is MIT. [LICENSE][k-license] Releases are v2.1.3 (2026-09-27), v2.1.2 (2026-08-14), and v2.1.1 (2026-07-15). The last commit is from 2026-10-07. The repo has about 9,000 stars. [releases][k-releases]
8. **Failure report.** A failed match reports `match failed: EQUALS` and one line per wrong path, in the form `<path> | <reason> (<actual type>:<expected type>)`, for example `not equal (STRING:STRING)`. [Operation.java][k-op] [KarateExecutorTest.java][k-optest] The HTML report shows each request and response in full. Cucumber JSON and JUnit XML reports are optional. [test reports][k-reports]

[k-root]: https://github.com/karatelabs/karate/tree/d67ecf783286fde376d7919acb202c5f0f664622
[k-features]: https://docs.karatelabs.io/core-syntax/feature-files
[k-config]: https://docs.karatelabs.io/core-syntax/configuration
[k-mask]: https://github.com/karatelabs/karate/blob/d67ecf783286fde376d7919acb202c5f0f664622/docs/CLI.md?plain=1#L215-L230
[k-vars]: https://docs.karatelabs.io/core-syntax/variables
[k-hooks]: https://docs.karatelabs.io/advanced/hooks
[k-fuzzy]: https://docs.karatelabs.io/assertions/fuzzy-matching
[k-schema]: https://docs.karatelabs.io/assertions/schema-validation
[k-v2]: https://github.com/karatelabs/karate/blob/d67ecf783286fde376d7919acb202c5f0f664622/README_V2.md?plain=1#L20-L31
[k-java]: https://docs.karatelabs.io/advanced/java-api
[k-cli]: https://github.com/karatelabs/karate/blob/d67ecf783286fde376d7919acb202c5f0f664622/docs/CLI.md?plain=1#L9-L25
[k-license]: https://github.com/karatelabs/karate/blob/d67ecf783286fde376d7919acb202c5f0f664622/LICENSE
[k-releases]: https://github.com/karatelabs/karate/releases
[k-op]: https://github.com/karatelabs/karate/blob/d67ecf783286fde376d7919acb202c5f0f664622/karate-core/src/main/java/io/karatelabs/match/Operation.java#L885-L915
[k-optest]: https://github.com/karatelabs/karate/blob/d67ecf783286fde376d7919acb202c5f0f664622/karate-gatling/src/test/java/io/karatelabs/gatling/KarateExecutorTest.java#L91-L95
[k-reports]: https://docs.karatelabs.io/running-tests/test-reports

## Tools left out

- **Dredd** (apiaryio/dredd) is archived on GitHub. [repo](https://github.com/apiaryio/dredd)
- **Strest** (eykrehbein/strest) is archived on GitHub, and its last push was in 2020. [repo](https://github.com/eykrehbein/strest)
- **Bruno** (usebruno/bruno) and **Newman** (postmanlabs/newman) are active JavaScript programs. They do not use YAML suites as their main format, and Go cannot embed them. I did not study them in depth. [Bruno](https://github.com/usebruno/bruno) [Newman](https://github.com/postmanlabs/newman)

## Limits of this research

- I did not run any tool. Every behavior comes from the docs or from reading the source.
- I did not test two `Venom` values in parallel goroutines. The shared global logger is a risk that only a test can confirm.
- The statement that scenarigo ignores keys not listed in `expect:` comes from reading the code, not from the docs.
- I found no statement on whether Step CI hides secret values in its output.
- runn marks `defer` as an experiment, so its behavior can change.
- Release dates and star counts come from the GitHub API and PyPI on 2026-10-08.
