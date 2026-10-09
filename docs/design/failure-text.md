# Failure text

The failure text is the text the runner records for a failed or skipped test, and the client prints it in the CI log. A check is one comparison a test makes on its response (see [GLOSSARY.md](../../GLOSSARY.md)). This doc defines the failure text line by line: line one, the request, the response with its marks, the cut for a big body, and the one line of a skipped test. The decisions come from [What does a failed test print?](https://github.com/rajnandan1/surface/issues/9) and [ADR 0008](../adr/0008-the-failure-text-is-the-request-and-the-response-with-the-checks-marked.md). The prototype that settled the shape is on the branch [`prototype/failed-test-output`](https://github.com/rajnandan1/surface/blob/prototype/failed-test-output/docs/prototype/failed-test-output.html).

## Example

The client prints each failed test as one line with the suite path and the test name, then the failure text indented by two spaces ([runs.md](runs.md)). A test that fails on two body values:

```
surface/api/users.yaml create-user
  body.data.plan: expected "free", got "trial" (+1 more)
  > POST https://pr-123.preview.crustdata.com/users
  > Authorization: Bearer ${{ secrets.production.api_token }}
  > Content-Type: application/x-www-form-urlencoded
  > User-Agent: surface/1.2.0
  >
  > email=u-01j9z3k4m5n6p7q8r9s0t1u2v3%40example.com&plan=free
  < 201 Created in 212ms                  ✓
  < Content-Type: application/json
  < Location: /users/8812
  <
  < {
  <   "data": {
  <     "id": 8812,
  <     "email": "u-01j9z3k4m5n6p7q8r9s0t1u2v3@example.com",
  <     "plan": "trial",                  ✗ expected "free"
  <                                       ✗ "status" is missing, expected "active"
  <   }
  < }
```

The test that produced it:

```yaml
  - name: create-user
    method: POST
    path: /users
    form:
      email: u-${{ run.id }}@example.com
      plan: free
    expect:
      status: 201
      body: {"data": {"plan": "free", "status": "active"}}
```

A test that fails on the status, and a test that is skipped:

```
surface/api/search.yaml no-token-is-rejected
  status: expected 401, got 200
  > POST https://pr-123.preview.crustdata.com/person/search
  > User-Agent: surface/1.2.0
  < 200 OK in 84ms                        ✗ expected 401
  < Content-Type: application/json
  <
  < {
  <   "profiles": []
  < }

surface/api/users.yaml update-user
  skipped: needs create-user, which failed
```

The `User-Agent` value above is a placeholder. The text the runner appends is not yet specified (the map's "Not yet specified").

## Where it shows

- The `failure` field of a failed or skipped test in `GET /v1/runs/<id>` holds the text with no indent ([runs.md](runs.md)).
- The CI log prints it under the line with the suite path and the test name, indented by two spaces.
- The GitHub annotation of a failed test is the test name, `: `, and line one. A skipped test gets no annotation.
- The runner redacts secret values before it stores the text ([secrets.md](secrets.md)).
- v1 keeps no other record of the request and the response. The run result holds the text and nothing more. The text holds what a reader needs to send the request again by hand, except the secret values.

## Checks

The checks of a test, in this order:

1. `expect.status`.
2. Each header in `expect.headers`.
3. `expect.body` against the example, in the match mode ([suite-format.md](suite-format.md)). Each key, value, or item that does not match is one check.
4. Each path in `expect.equal`.
5. Each path in `save`.

The runner runs every check and reports every failed one. It does not stop at the first failed check, because a `500` with a JSON error body is common, and the body says why.

## Line one

- Line one is the first failed check, in the order above, as `<what>: expected <X>, got <Y>`. When more checks failed, it ends with ` (+N more)`, where N is the count of the other failed checks.
- Line one stands alone. It names the check and both values, so it reads without the lines below. It is the GitHub annotation's message.
- The forms:

```
status: expected 401, got 200
headers.Content-Type: expected "application/json", got "text/html"
headers.Location: expected "/users/1", header is missing
body.data.plan: expected "free", got "trial"
body.data.plan: expected a string, got a number
body.data.status: expected "active", key is missing
body.items: expected 3 items, got 2
body: not JSON
save user_id: body.data.id finds nothing
no response: timeout after 10s
no response: connection refused
```

- The "expected" part shows what the match mode compares: the value in `values`, the type in `types`, and nothing in `keys`. So a missing key reads `expected "active", key is missing` in `values`, `expected a string, key is missing` in `types`, and `key is missing` in `keys`. `expect.equal` always shows the value.
- A type is `a string`, `a number`, `a bool`, `null`, `an object`, or `an array`.
- A value prints as compact JSON: `"free"`, `8812`, `null`, `{"a":1}`. A value longer than 80 characters is cut with `…`.
- `body: not JSON` is one check that stands for every body check, when a test has `expect.body`, `expect.equal`, or `save` and the body does not parse as JSON.
- `no response: <reason>` is the one check when the runner got no response. The reason is `timeout after <the test's timeout>` or the Go error text, for example `connection refused`. The lines below hold the request only.

## The request

- `> <METHOD> <URL>`: the method and the full URL the runner called, with the query string.
- One `> <Name>: <value>` per request header the runner sent, sorted by name. The headers the runner added, such as `Content-Type` and `User-Agent`, are in the list.
- When the request has a body: a `>` line alone, then the body with `> ` in front of each line. A `json` body is pretty-printed with two spaces of indent. A `form` or `body` body prints as sent. A request with no body ends after the headers.

## The response

- `< <code> <reason> in <N>ms`. This line always carries a mark, because every test checks the status: `✓` when the status check passed, or `✗ expected 401` when it failed.
- One `< <Name>: <value>` per response header, sorted by name. A header in `expect.headers` with the wrong value gets the mark `✗ expected "<value>"`. A header in `expect.headers` that the response does not have gets a `<` line of its own after the headers, with the mark `✗ "Location" is missing, expected "/users/1"`.
- When the response has a body: a `<` line alone, then the body with `< ` in front of each line. A body that parses as JSON is pretty-printed with two spaces of indent, keys in the order received. Any other body prints as received, line by line. A body that is not valid UTF-8 prints as one line, `< <binary, 23412 bytes>`.
- A mark is `✓` or `✗` plus text. A mark starts at column 40 of the line, counted from the `<`, or two spaces after the content when the content is longer.
- Each failed body check marks the line of the value it looked at. The mark text is `✗ expected <X>`, with `<X>` as in line one.
- A missing key gets a line of its own, `<` and the mark only, right before the closing bracket of the object that should hold it: `✗ "status" is missing, expected "active"`.
- An array with the wrong length marks the array's opening line: `✗ expected 3 items, got 2`.
- A `save` path that finds nothing marks the deepest object or array on the path that exists, placed as a missing key: `✗ "id" is missing, needed by save user_id`, or `✗ [3] is missing, needed by save first_item`.
- When a line has two failed checks, the second mark goes on a line of its own below it, placed as a missing key.
- `body: not JSON` marks the first body line: `✗ not JSON`.
- The marks of `expect.body` and `expect.equal` have the same text. The reader does not need to know which one failed.

## The cut

- A request body or a response body whose printed form is over 4 KiB is cut at 4 KiB, on a line boundary. One line follows, `> … 1204312 more bytes not shown` or `< … 1204312 more bytes not shown`.
- A failed check whose line is past the cut is not lost. It prints after the cut line, with its path: `< ✗ body.items[412].price: expected 10, got 12`.
- The limit is fixed in v1. The runner config has no setting for it.

## A skipped test

- The rule for when a test is skipped is decided in [How do tests that change data run safely?](https://github.com/rajnandan1/surface/issues/10). This doc fixes only what a skipped test prints.
- A skipped test has the state `skipped` and a failure text of one line: `skipped: needs create-user, which failed` or `skipped: needs update-user, which was skipped`.
- The client prints it in the failures section, like a failed test, so a reader sees why the counts do not add up to the suite's test count.

## The counts

- The suite line and the last line of the log count `passed`, `failed`, and `skipped`, in that order. `skipped` is left out when it is zero: `surface/api/users.yaml: 0 passed, 1 failed, 1 skipped` and `surface/api/search.yaml: 2 passed`.
- A passed test prints nothing.

## Decided elsewhere

- Redaction of secret values: [secrets.md](secrets.md)
- The log layout, the `failure` field, the job summary, and the annotations: [runs.md](runs.md)
- The match modes and the path syntax: [suite-format.md](suite-format.md)
- When a test is skipped, and what happens to the data a failed test created: [How do tests that change data run safely?](https://github.com/rajnandan1/surface/issues/10)
- The text the runner appends to `User-Agent`: the map's "Not yet specified"
