#!/usr/bin/env bash
# test_events_flat_up_to_date.sh — CI consistency test for Path C codegen.
#
# Asserts that proto/events-flat/ is up-to-date with proto/events/. If any
# canonical event proto changes without re-running scripts/flatten-pubsub-schemas.sh,
# this test fails — preventing terraform plan drift between source-of-truth
# and Schema Registry artifact.
#
# Run from chora-contracts/ root.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

OUT_DIR="proto/events-flat"
SHADOW_DIR="${TMPDIR:-/tmp}/chora-events-flat-shadow-$$"
trap 'rm -rf "${SHADOW_DIR}"' EXIT

mkdir -p "${SHADOW_DIR}"

# Build FileDescriptorSet via buf (excludes events-flat per buf.yaml excludes).
FDS_PATH="${TMPDIR:-/tmp}/chora-fds-test-$$.binpb"
trap 'rm -f "${FDS_PATH}"; rm -rf "${SHADOW_DIR}"' EXIT
buf build proto --as-file-descriptor-set --output "${FDS_PATH}"

# Run protoflatten into a shadow directory. SHADOW_DIR is absolute (mktemp-style)
# so do not prefix with ROOT_DIR.
(cd internal/protoflatten && GOWORK=off go run . -fds "${FDS_PATH}" -out "${SHADOW_DIR}" -quiet)

# Materialise the Path-A v2 aliases into the shadow exactly as
# scripts/flatten-pubsub-schemas.sh does for the committed tree. Without this
# the diff below flags the 4 committed .v2.proto aliases as stale on EVERY
# run (the test predates debt D-B's alias step, 2026-05-29). Keep this list
# identical to V2_FLAT_ALIASES in scripts/flatten-pubsub-schemas.sh.
for alias in "creation/atom/created" "creation/atom/updated" \
             "creation/atom/published" "creation/atom/archived"; do
  cp "${SHADOW_DIR}/${alias}.proto" "${SHADOW_DIR}/${alias}.v2.proto"
done

# Diff committed flat tree against shadow.
if ! diff -urN "${OUT_DIR}" "${SHADOW_DIR}" > /tmp/events-flat-diff.$$; then
  echo "FAIL: proto/events-flat/ is OUT OF DATE." >&2
  echo "Run \`./scripts/flatten-pubsub-schemas.sh\` and commit the updated tree." >&2
  echo "" >&2
  echo "Diff:" >&2
  cat /tmp/events-flat-diff.$$ >&2
  rm -f /tmp/events-flat-diff.$$
  exit 1
fi
rm -f /tmp/events-flat-diff.$$
echo "OK: proto/events-flat/ is up-to-date with proto/events/."
