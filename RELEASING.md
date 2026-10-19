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

## Bug fixes

### Which milestone

Every bug gets the label `bug` and exactly one milestone:

| The bug is in | Milestone | Released |
|---|---|---|
| a published version, and it is a security issue (reported as described in `SECURITY.md`, or a row of its threat table no longer holds) | patch milestone of that version (`v0.1.1`) | as soon as the fix has passed its test |
| a published version, otherwise | patch milestone of that version | when the fixes collected in the milestone have passed their test, or earlier if a household loses data, cannot install or cannot update |
| code that is only on `main`, not yet published | the minor milestone `main` is working towards (`v0.2`) | with that version |

Before 1.0, only the newest published minor version gets patches. Once `0.2.0` is out, a
bug in `0.1.x` is fixed by updating to `0.2.x`.

### Branches

- When a version `vX.Y.0` is published, a branch `release/X.Y` is created from its tag.
  From then on, every patch `vX.Y.Z` comes from that branch, no matter whether `main`
  already contains functions of the next minor version.
- A fix goes to `main` first, through a pull request labelled `bug` that closes the issue.
  It is then brought to `release/X.Y` by a second pull request into that branch that
  cherry-picks the commit. That pull request is titled `fix(X.Y): …` and links the first
  one. It runs the same checks, and before the merge it runs on a test installation built
  from it.
- If the bug no longer exists on `main` (the code was replaced), the fix goes straight to
  `release/X.Y` through a pull request that says why it is not on `main`.
- `release/X.Y` takes fixes only, plus the version and changelog of the patch. Functions
  never go there, not even small ones.
- A security fix is prepared in a private
  [security advisory](https://docs.github.com/en/code-security/security-advisories) of this
  repository and becomes public together with the patch.

### Patch releases

1. On `release/X.Y`, a pull request sets `version` in `app/config.yaml` and adds the entry
   to `app/CHANGELOG.md`.
2. Publish a release candidate from `release/X.Y` (`v0.1.1-rc.1`) and test it as a user
   would: a fresh installation, and an update from the previous version of `X.Y`. A
   security fix may shorten this test, never skip it.
3. Findings become issues in the patch milestone; continue with the branches above.
4. When the patch milestone has no open issue left and the last release candidate passed
   its test, publish the patch (`v0.1.1`) from the same commit and close the milestone.
5. A pull request to `main` adds the patch's entry to `app/CHANGELOG.md` there too, so the
   changelog on `main` is complete.

### Until the workflow supports it

`release.yml` accepts only tags on `main` today. Before tags on `release/X.Y` work, it must
check that:

- the tag points to a commit on `release/X.Y`, where `X.Y` matches the tag,
- the commit came from a pull request into that branch whose checks all passed,
- `release/*` is protected by the same rulesets as `main`.

Until then, no function of the next minor version is merged into `main`. Fixes that are
due in the meantime are released from `main` as before.

## Publishing

A tag `vX.Y.Z` or `vX.Y.Z-rc.N` on `main` (and, once the workflow supports it, on
`release/X.Y` for patches) runs `.github/workflows/release.yml`. It checks
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
