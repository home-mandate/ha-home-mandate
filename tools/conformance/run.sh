#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Runs mandate-conformance of the pinned version of the specification against Home-Mandate over
# both bindings of the test interface (SPEC-v0 section 10): the process binding for the
# classes Home-Mandate claims, the HTTP binding for the class pdp. Reports go to bin/.
set -eu

classes=evaluator,selection,audit,audit-anchored
# Both reports name the implementation and its release (SPEC-v0 section 10.4); the process
# binding's harness answers capabilities with the same name and version.
name=Home-Mandate
version=${VERSION:-dev}
tool=github.com/home-mandate/spec/cmd/mandate-conformance
mkdir -p bin
go build -ldflags "-X main.version=$version" -o bin/hm-conformance ./tools/conformance
go build -o bin/mandate-conformance "$tool"

bin/mandate-conformance -classes "$classes" -report bin/conformance-process.json -exec bin/hm-conformance

# The HTTP binding takes a fresh random token and listens on loopback only.
HM_CONFORMANCE_TOKEN=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
export HM_CONFORMANCE_TOKEN
out=$(mktemp)
bin/hm-conformance -http 127.0.0.1:0 >"$out" &
pid=$!
trap 'kill "$pid" 2>/dev/null || true; rm -f "$out"' EXIT
for _ in 1 2 3 4 5 6 7 8 9 10; do
	grep -q '^control ' "$out" && break
	sleep 0.5
done
authzen=$(awk '/^authzen /{print $2}' "$out")
control=$(awk '/^control /{print $2}' "$out")
if [ -z "$authzen" ] || [ -z "$control" ]; then
	echo "conformance: the HTTP binding did not start" >&2
	exit 1
fi
bin/mandate-conformance -report bin/conformance-http.json -authzen "$authzen" -control "$control" \
	-header "Authorization: Bearer $HM_CONFORMANCE_TOKEN" -name "$name" -version "$version"

# No report leaves out who made it: each names exactly this implementation and release.
for report in bin/conformance-process.json bin/conformance-http.json; do
	implementation=$(sed -n '/^  "implementation": {/,/^  }/p' "$report")
	if ! printf '%s\n' "$implementation" | grep -qxF "    \"name\": \"$name\"," ||
		! printf '%s\n' "$implementation" | grep -qxF "    \"version\": \"$version\""; then
		echo "conformance: $report does not name $name $version" >&2
		exit 1
	fi
done
