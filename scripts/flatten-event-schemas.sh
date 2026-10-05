#!/usr/bin/env bash
# flatten-event-schemas.sh — generate proto/events-flat/ from proto/events/.
#
# Path C codegen pipeline (locked 2026-05-10). Each per-aggregate event
# proto under proto/events/{domain}/{aggregate}.proto is rewritten as a
# SELF-CONTAINED proto under proto/events-flat/{domain}/{aggregate}.proto:
#   - inlines chora.common.v1.EventEnvelope (no `import "chora/common/v1/envelope.proto"`)
#   - inlines google.protobuf.Timestamp (no `import "google/protobuf/timestamp.proto"`)
#   - drops language-specific options (the flat consumer doesn't read them)
#
# The flat tree is a self-contained flattened-schema artifact: consumers that
# cannot resolve `import` statements read each file directly. It was introduced
# for a legacy managed schema registry, which rejected `import` statements
# (history — the platform now uses NATS JetStream); the artifact is retained
# broker-neutral. The canonical proto/events/ tree is unchanged and remains the
# source-of-truth for Go/Python codegen via `buf generate`.
#
# Run from chora-contracts/ root.
#
# Why bash + buf + Go (vs just Go embedding bufbuild/protocompile):
#   - buf is already a build dependency; FileDescriptorSet is its native output.
#   - Keeping protoflatten as a tiny standalone Go module avoids polluting
#     chora-contracts/gen/go (which is the published codegen module path).
#   - Pre-generated tree committed to git -> deterministic diffs.

set -euo pipefail

# Resolve script dir + cd to chora-contracts root.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

OUT_DIR="proto/events-flat"
# ⚠ Build into a STAGING sibling and swap only on success (ruling 44). This
# script used to `rm -rf` the live tree before generating, so ANY later failure
# (a missing go.sum, a parse error, a validation miss) left ZERO flat protos on
# disk. A sibling directory keeps the swap on one filesystem.
STAGE_DIR="proto/.events-flat.staging"
FDS_PATH="${TMPDIR:-/tmp}/chora-fds-$$.binpb"
FDS_FROZEN_PATH="${TMPDIR:-/tmp}/chora-fds-frozen-$$.binpb"
trap 'rm -f "${FDS_PATH}" "${FDS_FROZEN_PATH}"; rm -rf "${STAGE_DIR}"' EXIT

if ! command -v buf >/dev/null 2>&1; then
  echo "flatten-event-schemas: \`buf\` not found on PATH. Install via 'brew install bufbuild/buf/buf' or see https://buf.build." >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "flatten-event-schemas: \`go\` not found on PATH." >&2
  exit 1
fi

echo "flatten-event-schemas: building FileDescriptorSet via buf..."
buf build proto --as-file-descriptor-set --output "${FDS_PATH}"

# proto-frozen/ is a SEPARATE buf module (ruling 44): it holds the source for
# event generations still live on the wire whose current-generation source was
# renamed or deleted. It cannot share a module with proto/ because a frozen file
# may redeclare a helper the rename never touched (RitualStepStamp), so it gets
# its own descriptor set and protoflatten merges the two.
echo "flatten-event-schemas: building FROZEN FileDescriptorSet via buf..."
buf build proto-frozen --as-file-descriptor-set --output "${FDS_FROZEN_PATH}"

echo "flatten-event-schemas: staging a fresh tree at ${STAGE_DIR} (the live tree is untouched until the swap)..."
rm -rf "${STAGE_DIR}"
mkdir -p "${STAGE_DIR}"

# Run protoflatten outside the workspace (its module is intentionally
# isolated from go.work to avoid drift on the published gen/go module).
echo "flatten-event-schemas: running protoflatten..."
ABS_OUT="${ROOT_DIR}/${STAGE_DIR}"
(cd internal/protoflatten && GOWORK=off go run . -fds "${FDS_PATH}" -fds-frozen "${FDS_FROZEN_PATH}" -out "${ABS_OUT}" -quiet)

# ---------------------------------------------------------------------------
# Path-A v2 schema aliases (reproducibility — fixes debt D-B 2026-05-29).
#
# Schema revisions are immutable once published. Commit d84681bd (Path A,
# docs/m13/atom-schema-compat-decision-2026-05-17.md) bumped the 4 atom event
# schemas to a -v2 revision so a binary-compatible field rename (atom_type ->
# question_type, same tag) could land as a NEW immutable schema. The v2 wire
# bytes are IDENTICAL to v1, so the v2 flat proto is a byte-for-byte copy of
# the v1 flat proto at a `.v2.proto` filename.
#
# When an aggregate's v2 schema ever DIVERGES from v1 (real payload change),
# remove it from here and emit a distinct flat proto instead of a copy.
# ---------------------------------------------------------------------------
V2_FLAT_ALIASES=(
  "creation/atom/created"
  "creation/atom/updated"
  "creation/atom/published"
  "creation/atom/archived"
)
echo "flatten-event-schemas: materialising ${#V2_FLAT_ALIASES[@]} Path-A v2 aliases..."
for alias in "${V2_FLAT_ALIASES[@]}"; do
  src="${STAGE_DIR}/${alias}.proto"
  dst="${STAGE_DIR}/${alias}.v2.proto"
  if [ ! -f "${src}" ]; then
    echo "flatten-event-schemas: FAIL — v2 alias source missing: ${src}" >&2
    echo "  (V2_FLAT_ALIASES is out of sync with the emitted flat tree)" >&2
    exit 1
  fi
  cp "${src}" "${dst}"
done

# Sanity check: every flat file must NOT contain any `import` statement.
echo "flatten-event-schemas: validating no imports in flat tree..."
if grep -rE '^\s*import\s+"' "${STAGE_DIR}" >/dev/null; then
  echo "flatten-event-schemas: FAIL — found import statements in flat tree:" >&2
  grep -rEn '^\s*import\s+"' "${STAGE_DIR}" >&2
  exit 1
fi

# Sanity check: every flat file declares a chora.{domain}.v1 package + a
# local EventEnvelope + Timestamp.
flat_count=$(find "${STAGE_DIR}" -name '*.proto' | wc -l | tr -d ' ')
echo "flatten-event-schemas: emitted ${flat_count} flat protos into ${STAGE_DIR}"

# Confirm each schema parses standalone (no missing imports). Each file is
# self-contained, so we copy it into a tmp dir with a minimal buf.yaml and
# build there. Doing this in-place under proto/events-flat/ would conflict
# with the parent buf.yaml at proto/.
echo "flatten-event-schemas: validating each flat proto parses standalone..."
VALIDATE_DIR="${TMPDIR:-/tmp}/chora-flatten-validate-$$"
trap 'rm -f "${FDS_PATH}" "${FDS_FROZEN_PATH}"; rm -rf "${VALIDATE_DIR}"; rm -rf "${STAGE_DIR}"' EXIT
mkdir -p "${VALIDATE_DIR}"
cat > "${VALIDATE_DIR}/buf.yaml" <<'EOF'
version: v2
modules:
  - path: .
EOF

fail=0
while IFS= read -r f; do
  rel="${f#${STAGE_DIR}/}"
  dest="${VALIDATE_DIR}/${rel}"
  mkdir -p "$(dirname "${dest}")"
  cp "${f}" "${dest}"
  if ! (cd "${VALIDATE_DIR}" && buf build --path "${rel}" --output /dev/null 2>/tmp/buf-err.$$); then
    echo "flatten-event-schemas: FAIL — ${f}" >&2
    cat /tmp/buf-err.$$ >&2
    fail=$((fail + 1))
  fi
  rm -f "${dest}"
done < <(find "${STAGE_DIR}" -name '*.proto')
rm -f /tmp/buf-err.$$
if [ "${fail}" -gt 0 ]; then
  echo "flatten-event-schemas: ${fail} flat proto(s) failed standalone parse" >&2
  exit 1
fi

echo "flatten-event-schemas: OK — ${flat_count} self-contained protos validated"

# ---------------------------------------------------------------------------
# SWAP. Everything above ran against the staging tree, so the live tree is
# replaced only once the new one is known good.
# ---------------------------------------------------------------------------
rm -rf "${OUT_DIR}"
mv "${STAGE_DIR}" "${OUT_DIR}"
echo "flatten-event-schemas: swapped ${flat_count} protos into ${OUT_DIR}"
