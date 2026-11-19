# Releasing Home-Mandate

The rules are the same for the whole organization:
[RELEASING.md](https://github.com/home-mandate/.github/blob/main/RELEASING.md) covers versions,
release candidates, fixes through `release/X.Y`, and the guards before anything is published.
This page says only what is particular to this repository.

## What is published

A tag runs `.github/workflows/release.yml`, in the version of the tagged commit. It checks the
tag with the organization's guard (`home-mandate/.github/actions/release-verify`), and that
it matches `version` in `app/config.yaml`, that `app/CHANGELOG.md` has the entry and that a
version requires a version of the specification. It then builds
`ghcr.io/home-mandate/ha-home-mandate:<version>` for `linux/amd64` and `linux/arm64`, signs it
with cosign (keyless), attaches provenance and SBOMs, verifies all of that, tags the image and
creates the GitHub release as a draft (a pre-release for `-rc.N`) with the SBOM of the sources.
`conformance-report.yml` attaches both conformance reports to the draft, and the draft is
published only with the attested reports and the SBOM attached.

`app/config.yaml` carries the version without a suffix. The image of a release candidate gets
`X.Y.Z-rc.N` from the tag, so a release candidate needs no pull request, and the version is
tagged on the commit of a release candidate.

```sh
# the release candidate, on the release branch
gh workflow run release.yml --ref release/1.0 -f tag=v1.0.0-rc.1   # dry run, publishes nothing
git switch release/1.0 && git pull
git tag -a v1.0.0-rc.1 -m "Home-Mandate 1.0.0-rc.1"
git push origin v1.0.0-rc.1

# the version, on the commit of the release candidate, after its gate and the test
gh workflow run release.yml --ref release/1.0 -f tag=v1.0.0
git tag -a v1.0.0 -m "Home-Mandate 1.0.0" v1.0.0-rc.1^{commit}
git push origin v1.0.0
```

Tags and published releases are immutable, so a failed release needs a new version
(`-rc.N+1`). A run that fails before the release is published leaves a draft; the image tag of
that version exists already.

## How a version is tested

- Before the merge, a pull request runs the jobs of what it changes (`.github/scope.rules`):
  a change to the gateway runs the Go checks, conformance, fuzzing and every E2E scenario, a
  change to the UI its checks and the browser tests, a change to text nothing. Every night
  every job runs on `main` if `main` changed.
- After a release candidate is published, `release.yml` runs the release gate on its commit:
  - every job of `ci.yml` and `integration.yml`, whatever the last pull request changed;
  - every fuzz target for 20 minutes, on top of the corpus the scheduled runs have collected;
  - the E2E scenarios with the browser (`make e2e-ui`) against the signed image of the release
    candidate, not against a build of the sources;
  - an update: the release candidate starts on the data of the previous version
    (`make e2e-update`).

  The job `Release gate` succeeds only if all of it passed, and a version can only be tagged
  on a commit whose release candidate has it green.
- What the gate can't know is tested by hand on a test installation with the release
  candidate's image.

## Where the version is set

Home Assistant reads the app's version, its documentation and its changelog from the default
branch, and offers that version as an update. So `main` must not name a version before its
image exists, and this repository differs from the organization's steps in one place:

- For a minor version, `release/X.Y` is created from `main` first. A pull request into
  `release/X.Y` then sets `version` in `app/config.yaml` and writes the entry in
  `app/CHANGELOG.md`. It changes text and the version line only, so it runs no test; the
  release candidate's gate runs everything on its commit.
- For a patch, the first fix into `release/X.Y` sets the version and starts the entry.
- When the version is published, a pull request to `main` sets `version` there and adds the
  changelog entry. Until it is merged, the app does not offer the version.

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
