# Only listed GitHub owners may use the runner, matched by owner ID

Any GitHub repo can get an OIDC token with the runner's URL as the audience, so the token check alone lets a stranger's repo start runs. Such a repo can hold every run slot, fill the results volume, and send the pool's requests to the listed targets. So the runner config gets a required `github.owner_ids` list. An owner is the GitHub org or user account that holds a repo, and its ID is the number GitHub gives that account. On every route, before it reads the body, the runner refuses every token whose `repository_owner_id` claim is not in the list. The list holds IDs, not names, because the ID never changes. A name does: when an org or a user is renamed, GitHub frees the old name at once, and a stranger who registers it gets tokens with the same `repository` claim. The same check closes that gap for the secret group `callers`, which match names ([ADR 0005](0005-secret-groups-in-kubernetes-secrets.md)).

## Considered options

- **A required top-level `callers:` list of `repo` and `ref` globs.** Rejected. It matches names, so a recycled org name passes it, and the secret groups keep the same gap. Control per repo inside the operator's own org adds little, because those repos are the operator's own.
- **A caller may start a run only when it matches some secret group's `callers`.** Rejected. A repo whose tests use no secrets would need an empty group. Every group's `callers` would also silently mean "may start a run". It also matches names.
- **An optional list, where no list means any repo.** Rejected. A fresh install would be open to every GitHub repo.
- **Entries of `{id, name}`, with both checked.** Rejected. A rename of the operator's own org would break the install until the operator edits the name, and the name adds no protection that the ID lacks.
- **A cap on runs per repo.** Rejected for v1. Only the operator's own repos pass the list. A measured case of one repo starving the others is the trigger to add one.

Sources: [GitHub's OIDC reference](https://docs.github.com/en/actions/reference/security/oidc), [Renaming an organization](https://docs.github.com/en/organizations/managing-organization-settings/renaming-an-organization), [the changelog of 2026-04-23 on immutable subject claims](https://github.blog/changelog/2026-04-23-immutable-subject-claims-for-github-actions-oidc-tokens/), and the resolution of [Which callers may start a run?](https://github.com/rajnandan1/surface/issues/14).

## Consequences

- Every install lists at least one owner ID. The operator finds it with `gh api orgs/<name> --jq .id`, and a refused repo's `403` line prints it too.
- The secret group `callers` and the rule for who may read a run stay on names. One gap is left: when one listed owner renames itself, another listed owner can take the old name.
- A scheduled run, later, has no token, so the list does not apply to it. Another CI vendor, later, brings its own allow list in its own block.
- The design is in [docs/design/runs.md](../design/runs.md).
