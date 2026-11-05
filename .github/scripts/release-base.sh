#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# release-base.sh TAG COMMIT prints the branch the tag may be published from (RELEASING.md),
# or refuses. A minor version (vX.Y.0, vX.Y.0-rc.N) comes from main only; a patch comes from
# main or from its own branch release/X.Y. History that main and the branch share counts as
# main's. Reads the remote-tracking branches of the current repository; tested by
# tools/releasebase.
set -euo pipefail

tag=$1
commit=$2

refuse() {
  echo "::error::$1" >&2
  exit 1
}

# on BRANCH tells whether the commit is on the remote-tracking branch; a branch that does not
# exist has no commits.
on() {
  git rev-parse --quiet --verify "refs/remotes/origin/$1^{commit}" > /dev/null &&
    git merge-base --is-ancestor "$commit" "refs/remotes/origin/$1"
}

if ! [[ "$tag" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)(-rc\.[0-9]+)?$ ]]; then
  refuse "tag $tag is neither vX.Y.Z nor vX.Y.Z-rc.N"
fi
minor="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
patch="${BASH_REMATCH[3]}"

if on main; then
  echo main
elif [ "$patch" = 0 ]; then
  refuse "$tag is a minor version and points to a commit that is not on main"
elif on "release/$minor"; then
  echo "release/$minor"
else
  refuse "$tag points to a commit that is neither on main nor on release/$minor"
fi
