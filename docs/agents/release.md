# Releases and workflow files

## Cut a release

1. Create the branch `release/vX.Y.Z` from `main`.
2. Bump the version in one commit. Tests compare exact output text, including the runner version, so the bump changes `VERSION`, `charts/surface/Chart.yaml`, the helm lines in `docs/install.md`, and every `1.0.1` in test expectations. Commit 46a6c22 is the model.
3. Merge the branch into `main` through a pull request.
4. Push the tag `vX.Y.Z` on the merge commit, where `X.Y.Z` equals `VERSION`. `.github/workflows/release.yml` then builds the 6 binaries, the image, and the chart.

## Change `action.yml` or a workflow

- After a change to `.github/workflows/`, run `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`.
- Pass a value into a `run:` script through `env:`. `release_test.go` fails on a `${{ }}` expression inside a `run:` script of `action.yml` or `release.yml`.
