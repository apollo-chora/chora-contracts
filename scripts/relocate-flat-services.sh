#!/usr/bin/env bash
# relocate-flat-services.sh
#
# Post-process for `buf generate` (M11.4 codegen).
#
# Why: `paths=source_relative` produces gen/go/{proto-relative-path}/, but
# every .proto declares `option go_package = ".../gen/go/chora/{path}/v1"`
# which does NOT match the proto's filesystem layout. Result: generated Go
# files have correct `option go_package` imports, but live at the WRONG paths.
# Go imports won't resolve.
#
# Two specific issues:
#
# 1) FLAT SERVICE COLLISION — the 7 service protos under proto/services/{name}.proto
#    (agent_executor, closure_orchestrator, guardrail_service, langgraph_orchestrator,
#    model_broker_router, model_broker_gateway, model_broker_classifier) declare 7
#    DIFFERENT Go packages, but with paths=source_relative they all collide at
#    gen/go/services/. Go's "one package per directory" rule rejects this.
#
# 2) PATH-VS-IMPORT MISMATCH — even nested protos like
#    proto/services/sharing/v1/sharing.proto declare
#    `option go_package = ".../gen/go/chora/services/sharing/v1"`. With
#    paths=source_relative, files land at gen/go/services/sharing/v1/ — not
#    gen/go/chora/services/sharing/v1/. Imports won't resolve.
#
# The right long-term fix is to bulk-restructure proto/ to match its declared
# go_package paths (tracked as M11.9b proto cleanup per chora-contracts/buf.yaml
# `lint.except: PACKAGE_DIRECTORY_MATCH`). Until then, this script aligns the
# generated tree:
#
#   - gen/go/services/{flat}.pb.go            -> gen/go/chora/services/{flat}/v1/{flat}.pb.go
#   - gen/go/services/{nested}/v1/*.pb.go     -> gen/go/chora/services/{nested}/v1/*.pb.go
#   - gen/go/events/{domain}/*.pb.go          -> gen/go/chora/{domain}/v1/*.pb.go
#
# Mappings are derived from `option go_package` at the source-of-truth proto
# files via grep + path inversion.
#
# Idempotent. Run:    ./scripts/relocate-flat-services.sh
#                or:  make codegen   (wires this in)

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
CONTRACTS_DIR="$( cd "${SCRIPT_DIR}/.." && pwd )"
GEN_GO="${CONTRACTS_DIR}/gen/go"
PROTO_DIR="${CONTRACTS_DIR}/proto"

if [[ ! -d "${GEN_GO}" ]]; then
  echo "relocate-flat-services: gen/go not present — run 'buf generate' first" >&2
  exit 1
fi

# -----------------------------------------------------------------------------
# Helper: extract option go_package import path from a proto file.
# Strips quotes + ;package_alias suffix. Returns empty on no match.
# -----------------------------------------------------------------------------
extract_go_package() {
  local proto_file="$1"
  local line
  line=$(grep -m1 '^option go_package' "${proto_file}" || true)
  [[ -z "${line}" ]] && return 0
  # line: option go_package = "github.com/.../gen/go/chora/services/foo/v1;fooalias";
  # strip option go_package = "
  line="${line#*\"}"
  # strip "; tail
  line="${line%%\"*}"
  # strip ;alias
  line="${line%%;*}"
  echo "${line}"
}

# -----------------------------------------------------------------------------
# Build the source-relative -> target-relative mapping.
#
# For each proto file:
#   src_rel  = path under proto/ (e.g., services/agent_executor.proto)
#   gp_path  = `option go_package`'s tail under gen/go/ (e.g., chora/services/agent_executor/v1)
#   src_dir  = directory under gen/go/ that paths=source_relative produces
#              (e.g., gen/go/services/ for proto/services/agent_executor.proto)
#   src_base = generated filename basename WITHOUT extension
#   tgt_dir  = gp_path under gen/go/
#
# Move src_dir/src_base.pb.go + src_dir/src_base_grpc.pb.go to tgt_dir/
# -----------------------------------------------------------------------------

GO_PACKAGE_URL_PREFIX="github.com/locoroco-git/Chora-LMS/chora-contracts/gen/go/"

relocate_proto() {
  local proto_file="$1"
  local gp src_rel src_dir src_base src_pb src_grpc tgt_rel tgt_dir tgt_pb tgt_grpc

  gp=$(extract_go_package "${proto_file}")
  [[ -z "${gp}" ]] && return 0

  # Extract path under gen/go/ from gp
  if [[ "${gp}" != ${GO_PACKAGE_URL_PREFIX}* ]]; then
    echo "WARN: ${proto_file} has go_package outside the contracts gen/go tree: ${gp}" >&2
    return 0
  fi
  tgt_rel="${gp#${GO_PACKAGE_URL_PREFIX}}"
  tgt_dir="${GEN_GO}/${tgt_rel}"

  # src_rel under proto/
  src_rel="${proto_file#${PROTO_DIR}/}"
  src_dir="${GEN_GO}/$(dirname "${src_rel}")"
  src_base=$(basename "${src_rel}" .proto)
  src_pb="${src_dir}/${src_base}.pb.go"
  src_grpc="${src_dir}/${src_base}_grpc.pb.go"
  tgt_pb="${tgt_dir}/${src_base}.pb.go"
  tgt_grpc="${tgt_dir}/${src_base}_grpc.pb.go"

  # Already at target — no-op (idempotent).
  if [[ -f "${tgt_pb}" && ! -f "${src_pb}" ]]; then
    return 0
  fi

  if [[ ! -f "${src_pb}" ]]; then
    # Source missing AND target missing — proto wasn't picked up by codegen.
    if [[ ! -f "${tgt_pb}" ]]; then
      echo "WARN: no generated file for ${proto_file}" >&2
    fi
    return 0
  fi

  mkdir -p "${tgt_dir}"

  # Filename-collision guard: two protos may declare the same Go package +
  # share a basename (e.g. proto/chora/consumption/v1/knowledge_graph.proto
  # AND proto/events/consumption/knowledge_graph.proto — both compile to a
  # `chora.consumption.v1` Go package, and the relocator points them at the
  # same gen/go/chora/consumption/v1/knowledge_graph.pb.go destination,
  # silently overwriting the more comprehensive file).
  #
  # When the target already exists AND it was relocated from a DIFFERENT
  # source path during this same run, prefix the second source's
  # source-relative directory into the destination filename so both
  # contribute distinct .pb.go files to the same Go package (proto3 allows
  # multiple .pb.go files per package).
  if [[ "${src_pb}" != "${tgt_pb}" && -f "${tgt_pb}" ]]; then
    # Disambiguate using the source directory under proto/.
    local src_subdir
    src_subdir="$(dirname "${src_rel}")"
    # Convert path slashes to underscores for the filename prefix.
    local prefix="${src_subdir//\//_}"
    local disambig_pb="${tgt_dir}/${prefix}_${src_base}.pb.go"
    local disambig_grpc="${tgt_dir}/${prefix}_${src_base}_grpc.pb.go"
    echo "INFO: collision avoided: ${proto_file} -> ${disambig_pb}" >&2
    mv "${src_pb}" "${disambig_pb}"
    if [[ -f "${src_grpc}" ]]; then
      mv "${src_grpc}" "${disambig_grpc}"
    fi
    return 0
  fi

  if [[ "${src_pb}" != "${tgt_pb}" ]]; then
    mv "${src_pb}" "${tgt_pb}"
  fi
  if [[ -f "${src_grpc}" && "${src_grpc}" != "${tgt_grpc}" ]]; then
    mv "${src_grpc}" "${tgt_grpc}"
  fi
}

count=0
while IFS= read -r -d '' proto_file; do
  relocate_proto "${proto_file}"
  count=$((count + 1))
done < <(find "${PROTO_DIR}" -name "*.proto" -print0)

# Clean up newly-empty source directories.
find "${GEN_GO}" -type d -empty -delete 2>/dev/null || true

# -----------------------------------------------------------------------------
# Python: ensure every directory under gen/python/ has __init__.py AND rewrite
# generated relative imports to use the `chora_contracts_gen.` prefix so the
# wheel installs collision-free under that single namespace.
#
# protoc-gen-python emits sibling-relative imports like:
#   from services import agent_executor_pb2 as services_dot_agent__executor__pb2
#   from chora.common.v1 import envelope_pb2 as ...
# These would only resolve if `services/`, `events/`, `chora/` sat at the top
# of sys.path — which would pollute the global namespace. Rewriting to
#   from chora_contracts_gen.services import agent_executor_pb2 as ...
#   from chora_contracts_gen.chora.common.v1 import envelope_pb2 as ...
# scopes everything cleanly.
# -----------------------------------------------------------------------------
GEN_PY="${CONTRACTS_DIR}/gen/python"
if [[ -d "${GEN_PY}" ]]; then
  py_init_count=0
  while IFS= read -r -d '' dir; do
    if [[ ! -f "${dir}/__init__.py" ]]; then
      : > "${dir}/__init__.py"
      py_init_count=$((py_init_count + 1))
    fi
  done < <(find "${GEN_PY}" -type d -print0)
  echo "relocate-flat-services: backfilled ${py_init_count} __init__.py under gen/python"

  # Top-level packages we expect under gen/python/. Rewrite imports.
  py_rewrite_count=0
  while IFS= read -r -d '' py_file; do
    # macOS sed -i needs a literal '' between -i and the script.
    # Rewrite "from services" -> "from chora_contracts_gen.services" etc.,
    # but only when not already prefixed.
    if grep -qE "^(from (services|events|chora)|import (services|events|chora))" "${py_file}"; then
      # Use perl for in-place edit (portable across macOS / Linux).
      perl -i -pe '
        s/^from (services|events|chora)\b/from chora_contracts_gen.$1/g;
        s/^import (services|events|chora)\b/import chora_contracts_gen.$1/g;
      ' "${py_file}"
      py_rewrite_count=$((py_rewrite_count + 1))
    fi
  done < <(find "${GEN_PY}" -name "*.py" -print0)
  echo "relocate-flat-services: rewrote chora_contracts_gen.* imports in ${py_rewrite_count} python files"
fi

echo "relocate-flat-services: processed ${count} protos; gen/go aligned to declared option go_package paths"
