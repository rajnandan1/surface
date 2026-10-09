# Suite format

A suite is one YAML file in a product repo, and it holds many tests (see [GLOSSARY.md](../../GLOSSARY.md)). This doc defines the format for `kind: API` at version `surface/v1`. The decisions come from [What does a suite file look like?](https://github.com/rajnandan1/surface/issues/5).

## Example

```yaml
apiVersion: surface/v1          # The runner rejects an unknown version.
kind: API                       # HTTP tests. Later kinds add UI and MCP tests.

defaults:                       # Applies to every test. A test can override it.
  headers:
    Authorization: Bearer ${{ secrets.production.api_token }}  # Key api_token of secret group production. See secrets.md.
  timeout: 10s                  # Connect + send + wait + read. The runner default is 30s.

tests:
  # An independent test. It has no `needs`, so it runs at the same time as the others.
  - name: search-person         # A slug [a-z0-9-], unique in this suite.
    method: POST
    path: /person/search        # Relative to the run's target.
    query:
      limit: 10                 # A number or a bool becomes text. A list repeats the key.
    json: {                     # Pasted JSON is valid YAML. Sets Content-Type if it is not set.
      "professional_network_profile_urls": ["https://www.linkedin.com/in/<example>/"]
    }
    timeout: 30s                # Overrides defaults.timeout.
    expect:
      status: 200
      headers:
        Content-Type: application/json   # Listed headers must match. Other headers are ignored.
      match: types              # values | types | keys. The default is values.
      body: {                   # The example body, pasted from a real response.
        "profiles": [
          {"name": "Ada Lovelace", "title": "Engineer", "location": null}
        ]
      }
      equal:
        body.profiles[0].name: Ada Lovelace

  - name: no-token-is-rejected
    method: POST
    path: /person/search
    headers:
      Authorization: null       # Removes the default header.
    expect:
      status: 401

  # A create-then-update pair.
  - name: create-user
    method: POST
    path: /users
    form:                       # A form-encoded body. Sets Content-Type if it is not set.
      email: u-${{ run.id }}@example.com   # Form values are text and are URL-escaped.
      plan: free
    expect:
      status: 201
    save:
      user_id: body.data.id     # If the path finds nothing, the test fails.
      location: headers.Location
    cleanup:                    # Removes what this test created. Runs after the suite's tests end. See data-safety.md.
      method: DELETE
      path: /users/${{ needs.create-user.user_id }}
      expect:
        status: 204

  - name: update-user
    needs: [create-user]        # Waits for create-user, and can read its saved values.
    method: PATCH
    path: /users/${{ needs.create-user.user_id }}   # Text, URL-escaped.
    json:
      user_id: ${{ needs.create-user.user_id }}     # The whole value: it stays a number.
      note: "updated by ${{ run.id }}"              # Inside text: it becomes text.
    expect:
      status: 200
      equal:
        body.id: ${{ needs.create-user.user_id }}
```

## The file

- A suite has four top-level fields. `apiVersion` must be `surface/v1`. `kind` must be `API`. `defaults` is optional. `tests` holds one or more tests.
- The file path in the repo names the suite. A suite has no `name`, `metadata`, or `labels` field.
- The runner rejects a suite that has an unknown field at any level, so a typo like `staus:` fails at load.
- The runner rejects a file that has more than one YAML document (`---`).
- The runner rejects a suite that has a YAML anchor (`&name`) or alias (`*name`). An alias can expand a small file into a very large document. `defaults` covers reuse.
- Any value can be written in YAML or as pasted JSON, because YAML reads JSON. Inside pasted JSON, a `${{ }}` must be in quotes, for example `"user_id": "${{ needs.create-user.user_id }}"`. The type rules below still apply to it.

## Variables

- A variable is `${{ <name> }}`. Inside the braces, only a name lookup is allowed: no operators and no functions.
- v1 has three kinds of names:
  - `secrets.<group>.<name>` reads the key `<name>` of the secret group `<group>`. The value is always text. The references are the suite's declaration of the groups it uses. Where groups live and who may use them is in [secrets.md](secrets.md).
  - `run.id` is a unique ID for each run. Its format is in [runs.md](runs.md).
  - `needs.<test>.<saved value>` reads a saved value from a test listed in `needs`.
- A suite has no `vars:` block. Repetition across tests is acceptable.
- The runner checks every name at load, before it sends any request.
- **Types.** When a JSON value is exactly one `${{ }}`, the value keeps its JSON type: the number `42` stays a number. When `${{ }}` is inside longer text, the value becomes text. An object or an array inside longer text makes the test fail.
- In `path`, `query`, `headers`, and `form`, a value is always text. The runner URL-escapes values in `path`, `query`, and `form`, so a value like `a/b` cannot change the path.

## Defaults

- `defaults` holds only `headers` and `timeout`.
- A test header replaces the default header with the same name. The name match ignores case. Other default headers stay.
- A test header set to `null` removes the default header.
- There is no `auth:` block. Auth is a header.

## A test

| Field | Required | What it holds |
| --- | --- | --- |
| `name` | Yes | A slug `[a-z0-9-]`, unique in the suite. Later tests and the report use it. |
| `method` | Yes | The HTTP method, for example `GET` or `POST`. |
| `path` | Yes | The path, relative to the run's target. It starts with `/`. A test has no scheme and no host. |
| `query` | No | A map of query parameters. A number or a bool becomes text. A list repeats the key. |
| `headers` | No | A map of request headers. They merge with `defaults.headers`. |
| `json` | No | A body that the runner sends as JSON. The runner sets `Content-Type: application/json` if the test does not set it. |
| `form` | No | A form-encoded body, with the same value rules as `query`. The runner sets `Content-Type: application/x-www-form-urlencoded` if the test does not set it. |
| `body` | No | A raw text body. |
| `timeout` | No | A Go duration, for example `500ms` or `10s`. It covers connect, send, wait, and read. It overrides `defaults.timeout`. The runner default is 30s. The maximum is 5m ([limits.md](limits.md)). |
| `needs` | No | A list of test names that this test waits for. |
| `save` | No | A map from a saved value's name to a path. |
| `cleanup` | No | One request, with its own `expect`, that removes the data this test created. It runs after every test in the suite has ended. The rules are in [data-safety.md](data-safety.md). |
| `expect` | Yes | The response the test must get back. `expect.status` is required. |

- A test has one of `json`, `form`, `body`, or none.
- v1 has no multipart bodies and no file uploads, because a file upload reads files on the runner ([ADR 0001](../adr/0001-suites-are-untrusted-input.md)).

## Order

Tests in a suite run in parallel unless they declare `needs` ([ADR 0003](../adr/0003-tests-run-in-parallel-unless-they-declare-needs.md)).

- A test with `needs` waits for every test in its list.
- A test can read only the saved values of the tests in its `needs`.
- A test whose `needs` test failed or was skipped is skipped ([data-safety.md](data-safety.md)).
- A cycle in `needs` is a load error. A name in `needs` that is not in the suite is a load error.

## Saved values and paths

- `save` maps a saved value's name to a path. If the path finds nothing, the test fails, and the message names the path.
- A saved value is visible only inside its own suite.
- A path starts at `body` or `headers`. It allows keys, array indexes, and quoted keys only: `body.profiles[0].name`, `body["full.name"]`, `headers.Location`. It has no wildcards and no filters ([ADR 0004](../adr/0004-own-path-syntax-not-jsonpath.md)).

## Matching the response

- `expect.status` is one exact number.
- Each header under `expect.headers` must be in the response with the same value. The name match ignores case. The runner ignores other response headers.
- `expect.body` is an example body. `expect.match` sets the match mode: `values`, `types`, or `keys`. When a test sets `expect.body` without `expect.match`, the mode is `values`.

| Case | `values` | `types` | `keys` |
| --- | --- | --- | --- |
| The response has a key that the example does not have. | The test passes. | The test passes. | The test passes. |
| The response does not have a key that the example has. | The test fails. | The test fails. | The test fails. |
| Same type, different value. | The test fails. | The test passes. | The test passes. |
| Different type. | The test fails. | The test fails. | The test passes. |
| `null` on one side. | The test passes only if both sides are `null`. | The test passes. | The test passes. |
| Arrays. | The lengths, the order, and each item must be equal. | Each response item must match the first example item. An empty array passes. | The same as `types`. |

- Numbers are compared by value, so `1` and `1.0` are equal. Large integers, for example IDs above 2^53, are compared exactly.
- `expect.equal` maps a path to an expected value. The runner compares them with the `values` rules. v1 has equality only: no regex, no "greater than", and no "contains".
- An `expect.equal` path must start with `body`. The runner rejects a `headers.` path there with a message that points to `expect.headers`.

## Parser

The runner parses suites with `go.yaml.in/yaml/v3` at v3.0.5, the maintained fork of the archived `gopkg.in/yaml.v3`. On 2026-10-08 it passed the library rule of [ADR 0002](../adr/0002-own-suite-format-and-engine.md).

- The decoder runs with `KnownFields(true)`, so it rejects unknown fields.
- The library cannot turn anchors off. The runner walks the node tree and rejects any node with an anchor or of kind alias.
- Each node carries a line and a column. Failure reports use them to point at the line of the suite.

## Decided elsewhere

- Secret groups, who may use them, and redaction: [secrets.md](secrets.md)
- Where the target comes from, and how the runner builds the request URL: [targets.md](targets.md)
- How a run starts, who the caller is, and how the result comes back: [runs.md](runs.md)
- What a failed test prints: [failure-text.md](failure-text.md)
- Skipped tests, cleanup, and collisions between runs: [data-safety.md](data-safety.md)
- The maximum `timeout`: [limits.md](limits.md)
