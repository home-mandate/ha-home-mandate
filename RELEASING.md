# Releasing Home-Mandate

How versions are planned, tested and published. Versions follow
[Semantic Versioning](https://semver.org/): before 1.0, a minor version (`0.2.0`) brings
new functions and may change behaviour; a patch version (`0.1.1`) only fixes.

## Milestones

Every planned version has a milestone in this repository, named after the version
(`v0.1`, `v0.2`, …), without a date. The repositories of the specification
(`home-mandate/spec`, `home-mandate/spec-ts`) use milestones with the same names for what
this version needs from them, so one search shows everything that belongs to a version:
[`org:home-mandate milestone:v0.1`](https://github.com/search?q=org%3Ahome-mandate+milestone%3Av0.1&type=issues).
The board [Roadmap](https://github.com/orgs/home-mandate/projects) shows their state.

- A **minor milestone** (`v0.1`, `v0.2`) holds the fixes and functions of that version.
  `v0.1` is the first usable version: it contains fixes and what is required for that;
  new functions go to `v0.2`.
- A **patch milestone** (`v0.1.1`) is created when a fix for a published version is due.
- Every issue has a label: `bug` for something that does not work as documented,
  `enhancement` for something new, `documentation`, `accessibility`. A pull request
  carries the label of the issue it closes; the labels of the pull requests sort the
  release notes (`.github/release.yml`).

## From milestone to release

1. Fix the issues of the milestone. Every change goes through a pull request; before the
   merge it runs on a test installation built from the branch.
2. When a set of fixes is merged, publish a release candidate from `main`
   (`v0.1.0-rc.3`, `v0.1.0-rc.4`, …) and test it as a whole, as a user would install it.
3. Findings become issues in the milestone; continue with step 1.
4. When the milestone has no open issue left and the last release candidate passed its
   test, publish the version (`v0.1.0`) from the same commit as that release candidate,
   and close the milestone.

## Patch releases

Fixes for a published version are released as `vX.Y.Z+1`:

- While `main` contains only fixes since the release, the patch is tagged on `main`.
- Once `main` contains functions of the next minor version, a branch `release/X.Y` is
  created from the tag of the published version, the fixes are cherry-picked onto it from
  `main` (fix on `main` first), and the patch is tagged on that branch. The release
  workflow accepts tags on `main` and on `release/*` only after this is implemented; until
  then, patches are tagged on `main`.

## Publishing

A tag `vX.Y.Z` or `vX.Y.Z-rc.N` on `main` runs `.github/workflows/release.yml`. It checks
that the tag points to a commit on `main` that came from a pull request whose checks all
passed, and that it matches `version` in `app/config.yaml` (the Supervisor uses that
version as the image tag). It then builds `ghcr.io/home-mandate/ha-home-mandate:<version>`
for `linux/amd64` and `linux/arm64`, signs it with cosign (keyless), attaches provenance
and SBOMs, verifies all of that, tags the image and creates the GitHub release (a
pre-release for `-rc.N`).

Before tagging: update `app/config.yaml` (`version`) and `app/CHANGELOG.md`.

Create the tag with git, not with GitHub's "Draft a new release": that page creates the
release together with the tag, and the workflow refuses to publish when the release exists
already (it never overwrites one). Tags are immutable, so a failed release needs a new
version (`-rc.N+1`).

```sh
git switch main && git pull
git tag -a v0.1.0-rc.3 -m "Home-Mandate 0.1.0-rc.3"
git push origin v0.1.0-rc.3
```

## The specification

The specification (`home-mandate/spec`) and its TypeScript implementation have their own
version numbers. Each Home-Mandate version names the exact version of the specification it
implements (`go.mod`); a change of the specification is released there first and then
adopted here in a pull request.
