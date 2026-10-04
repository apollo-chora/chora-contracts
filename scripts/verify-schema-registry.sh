#!/usr/bin/env bash
# verify-schema-registry.sh — CI guard (CHO-2155).
#
# Fails the build when an event proto declares a field that the topic's
# COMMITTED Pub/Sub schema revision does not have.
#
# WHY THIS EXISTS
# ---------------
# GCP Pub/Sub's schema validator is STRICT. A binary proto message carrying a
# field absent from the committed revision is REJECTED AT PUBLISH with
# HTTP 400 "Message failed schema validation" — protobuf's usual unknown-field
# tolerance does NOT apply. The message never enters the topic, so it never
# dead-letters. The only trace is the Cloud Monitoring metric
# `pubsub.googleapis.com/topic/send_request_count` with
# response_code=invalid_argument.
#
# Worse, the break is DATA-DEPENDENT. Encoders skip zero-valued fields, so a
# newly-added field only hits the wire once a producer populates it. The topic
# looks healthy for weeks, then starts rejecting a subset of messages.
# chora.delivery.submission.graded.v1 lived exactly this: `hint_count` (field
# 12) was written only `if v != 0`, so submissions with no hints published fine
# and submissions WITH hints were silently rejected.
#
# WHY THE REST OF THE PIPELINE DOESN'T CATCH IT
# ---------------------------------------------
# `make verify-reproducible` already proves  source proto -> flat proto ->
# generated Go  are in lockstep. The one link nothing verified is the last one:
#
#     proto/events-flat/**.proto   ->   the COMMITTED schema revision in GCP
#
# That link was only ever closed by `terraform apply` on m10-data-plane — which
# this project has a standing rule NEVER to run (TF state is ~228 resources
# behind; Pub/Sub is provisioned out-of-band). So the last mile of the schema
# pipeline is disconnected by policy, and protos drift ahead of the registry
# until a publish starts 400ing.
#
# USAGE
# -----
#   bash scripts/verify-schema-registry.sh              # hermetic: vs the checked-in snapshot
#   bash scripts/verify-schema-registry.sh --live       # vs the LIVE registry (needs GCP creds)
#   bash scripts/verify-schema-registry.sh --refresh    # re-ground the snapshot from live
#
# CI runs the hermetic form (no credentials). --live is the re-grounding pass:
# run it whenever you touch an event proto, and after committing a revision.
#
# EXIT CODES
#   0  every event proto is covered by its committed schema revision
#   1  FATAL drift — a publish WILL be rejected with HTTP 400
#   2  the guard could not run (never a silent pass)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${ROOT_DIR}"

PROJECT="${CHORA_PUBSUB_PROJECT:-chora-489812}"
SNAPSHOT="schema-registry/committed-schemas.json"
FDS_PATH="${TMPDIR:-/tmp}/chora-schemaguard-fds-$$.binpb"
trap 'rm -f "${FDS_PATH}"' EXIT

MODE_ARGS=()
case "${1:-}" in
  --live)    MODE_ARGS=(-live -project "${PROJECT}") ;;
  --refresh) MODE_ARGS=(-live -refresh -project "${PROJECT}") ;;
  "")        MODE_ARGS=() ;;
  *)         echo "usage: $0 [--live|--refresh]" >&2; exit 2 ;;
esac

command -v buf >/dev/null 2>&1 || { echo "verify-schema-registry: 'buf' not found on PATH" >&2; exit 2; }
command -v go  >/dev/null 2>&1 || { echo "verify-schema-registry: 'go' not found on PATH" >&2; exit 2; }

# The repo side is derived from the GENERATED descriptor — never a hand-written
# list of events, which is precisely the artifact that drifts silently.
buf build proto --as-file-descriptor-set --output "${FDS_PATH}"

# protoflatten and schemaguard are standalone modules, deliberately outside
# go.work so they don't pollute the published gen/go module path. Their go.sum
# is gitignored, so a FRESH WORKTREE has none and the build dies with
# "missing go.sum entry" — which reads exactly like a broken guard, but isn't.
# Materialise it once, here, so nobody has to know that. (The sibling
# protoflatten script does NOT do this, which is why `make regen` dies at
# flatten in a fresh worktree.)
cd internal/schemaguard
if [ ! -f go.sum ]; then
  echo "verify-schema-registry: fresh worktree — materialising go.sum (gitignored by convention)..."
  GOWORK=off go mod tidy
fi

# ${MODE_ARGS[@]+...} — expand only if the array is non-empty. A bare
# "${MODE_ARGS[@]}" on an empty array trips `set -u` on bash 3.2 (macOS), which
# would abort the wrapper and leave the guard silently not guarding.
GOWORK=off go run ./cmd/schemaguard \
  -fds "${FDS_PATH}" \
  -flat "${ROOT_DIR}/proto/events-flat" \
  -snapshot "${ROOT_DIR}/${SNAPSHOT}" \
  ${MODE_ARGS[@]+"${MODE_ARGS[@]}"}
