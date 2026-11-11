# Releasing Home-Mandate

The rules are the same for the whole organization:
[RELEASING.md](https://github.com/home-mandate/.github/blob/main/RELEASING.md) covers versions,
release candidates, fixes through `release/X.Y`, and the guards before anything is published.
This page says only what is particular to this repository.

## What is published

A tag runs `.github/workflows/release.yml`, in the version of the tagged commit. It checks
where the tag stands (`.github/scripts/release-base.sh`, tested by `tools/releasebase`), and
that it matches `version` in `app/config.yaml`, which the Supervisor uses as the image tag. It
then builds `ghcr.io/home-mandate/ha-home-mandate:<version>` for `linux/amd64` and
`linux/arm64`, signs it with cosign (keyless), attaches provenance and SBOMs, verifies all of
that, tags the image and creates the GitHub release as a draft (a pre-release for `-rc.N`)
with the SBOM of the sources. `conformance-report.yml` attaches both conformance reports to
the draft, and the draft is published only with the attested reports and the SBOM attached.

Before tagging: update `app/config.yaml` (`version`) and `app/CHANGELOG.md`.

```sh
git switch main && git pull   # for a patch: release/X.Y
git tag -a v0.1.0-rc.3 -m "Home-Mandate 0.1.0-rc.3"
git push origin v0.1.0-rc.3
```

Tags and published releases are immutable, so a failed release needs a new version
(`-rc.N+1`). A run that fails before the release is published leaves a draft; the image tag of
that version exists already.

## How a version is tested

- Before the merge, a change runs on a test installation built from the branch.
- A release candidate is tested as a user would install it. For a patch: a fresh installation,
  and an update from the previous version of `X.Y`.

## A patch and the Home Assistant app

Home Assistant reads the app's version from the default branch. So the pull request to `main`
that adds a patch's changelog entry also sets `version` in `app/config.yaml` to the patch: until
it is merged, the app does not offer the patch.

## Milestones and labels

The milestones of `home-mandate/spec` and `home-mandate/spec-ts` hold what a version needs from
them, so one search shows everything that belongs to it:
[`org:home-mandate milestone:v0.1`](https://github.com/search?q=org%3Ahome-mandate+milestone%3Av0.1&type=issues).
The board [Roadmap](https://github.com/orgs/home-mandate/projects) shows their state.

`v0.1` is the first usable version: it contains fixes and what is required for that. New functions
go to `v0.2`.

Every issue has a label: `bug` for something that does not work as documented, `enhancement` for
something new, `documentation`, `accessibility`. A pull request carries the label of the issue
it closes; the labels of the pull requests sort the release notes (`.github/release.yml`).

## The specification

The specification (`home-mandate/spec`) and its TypeScript implementation have their own
version numbers. Each Home-Mandate version names the exact version of the specification it
implements (`go.mod`); a change of the specification is released there first and then
adopted here in a pull request.
