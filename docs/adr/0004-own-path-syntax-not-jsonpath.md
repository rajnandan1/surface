# Surface has its own path syntax, not JSONPath

A path points at one value in a response, for `save` and for `expect.equal`. It allows keys, array indexes, and quoted keys only, for example `body.profiles[0].name`, `body["full.name"]`, and `headers.Location`. It has no wildcards and no filters. A test that needs a filter is usually two tests, and a path to one value is about 60 lines of Go with no library.

## Considered options

- **JSONPath through `ohler55/ojg`.** Rejected. On 2026-10-08 it was the only Go JSONPath library that passed the library rule of [ADR 0002](0002-own-suite-format-and-engine.md) (v1.28.7, 2026-09-28), and it does not claim RFC 9535. Every Go library that claims RFC 9535 was still at v0.x.

## Consequences

- Contributors will ask for filters and wildcards.
- A path never starts with `$`. So a later JSONPath option can use the `$.` prefix without a clash with old suites.
