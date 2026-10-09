# Within a major version the API only grows, and the runner rejects what it does not know

The GitHub Action at `@v1` always gets the newest 1.x client, and each operator upgrades their runner on their own schedule. So a new client often meets an old runner. Within 1.x, the `/v1/` routes, the run request, the run result, the suite format `surface/v1`, and the runner config only gain fields and values. Nothing is removed or renamed. The client ignores unknown fields in the run result, so an old client works against a new runner. The runner rejects an unknown field in the run request and an unknown field or value in a suite, and the error line ends with the runner's version, for example `(runner 1.1.0)`. So a suite that uses a newer feature fails loudly against an old runner, and the line tells the developer to ask the operator to upgrade.

## Considered options

- **Version negotiation: the client and the runner agree on a feature set before the run.** Rejected. It needs a handshake and a feature list on both sides. The only case that can break is a new feature against an old runner, and a strict runner already fails that case with a clear line.
- **The runner publishes a minimum client version and refuses older clients.** Rejected. An old client works against a new runner, because new fields are additive. A minimum version would fail CI jobs that work.
- **The runner ignores unknown fields.** Rejected. A suite that uses a newer feature, for example a new match mode, would run without it on an old runner and pass or fail for the wrong reason.

Sources: the resolution of [How does an organization install surface?](https://github.com/rajnandan1/surface/issues/13).

## Consequences

- A field or a value, once released in 1.x, stays until 2.0.
- The run request and every suite are decoded strictly. The run result is decoded leniently by the client.
- `surface check` in a newer client can pass a suite that an older runner rejects. The runner's line names its version.
- A 2.x runner that no longer serves `/v1/` makes an old client print `surface run: runner at <url> has no /v1/runs: it may be a different major version`.
- The design is in [docs/design/install.md](../design/install.md).
