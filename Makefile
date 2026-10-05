# Chora Contracts — codegen Makefile
#
# WHY THIS EXISTS (reproducibility debt fix, 2026-05-29):
# The README referenced `make codegen` but no Makefile existed. People ran
# `buf generate` directly against a DIRTY gen/ tree, which made
# scripts/relocate-flat-services.sh false-detect the already-present
# (committed) relocation targets as same-run collisions and emit
# `events_*` / `services_*`-prefixed duplicate Go files (~130-file phantom
# diff). The fix is a CLEAN-FIRST recipe: wipe the generated subtrees before
# `buf generate` so relocation starts from empty and produces the canonical
# short-name layout deterministically.
#
# Verified 2026-05-29: `make codegen` from a clean checkout reproduces the
# committed gen/ tree BYTE-FOR-BYTE (0 modified, 0 deleted, 0 prefixed).
#
# Hand-edits to gen/ are rejected in review — regenerate via `make codegen`.

.PHONY: regen codegen flatten lint breaking clean-gen verify-reproducible

# ---------------------------------------------------------------------------
# regen — full reproducible regeneration: Go/Python bindings + flat schema
# tree. Run this on any proto change.
# ---------------------------------------------------------------------------
regen: codegen flatten

# ---------------------------------------------------------------------------
# codegen — the canonical Go + Python generation recipe (CLEAN-FIRST).
# This is what CI and contributors run on any proto change.
# ---------------------------------------------------------------------------
# `buf generate proto` targets the proto module EXPLICITLY. proto-frozen/ is
# FLATTEN-INPUT ONLY (ruling 44): it carries source for event generations still
# live on the wire whose current-generation source was renamed or deleted.
# Generating from it would add bindings for a retired generation that nothing
# consumes, and it cannot even share a module with proto/ (both declare
# RitualStepStamp). gen/ therefore describes the CURRENT generation only.
# Pinned by TestGenTreeHasNoFrozenGenerationBindings.
codegen: clean-gen
	buf generate proto
	bash scripts/relocate-flat-services.sh

# Wipe ONLY the generated subtrees (preserve gen/go/go.mod, which is
# hand-maintained, not generated). Clean-first is what makes relocation
# deterministic.
clean-gen:
	rm -rf gen/go/chora gen/python

lint:
	buf lint

breaking:
	buf breaking --against '.git#branch=main'

# ---------------------------------------------------------------------------
# flatten — regenerate proto/events-flat/, the self-contained flattened-schema
# artifact consumed by downstream tooling.
#
# Reproducible as of 2026-05-29 (debt D-B fixed): the script materialises the
# Path-A atom v2 schema aliases (*.v2.proto) deterministically from the
# V2_FLAT_ALIASES list, so it no longer drops them.
# ---------------------------------------------------------------------------
flatten:
	bash scripts/flatten-event-schemas.sh

# verify-reproducible — CI guard: full regen, then assert the working tree is
# clean under gen/ AND proto/events-flat/ (no modified AND no untracked files).
# Any output = drift between committed artifacts and the generators.
verify-reproducible: regen
	@if [ -n "$$(git status --porcelain -- gen/ proto/events-flat/)" ]; then \
		echo "FAIL: generated artifacts drifted after 'make regen' — committed tree is stale:"; \
		git status --porcelain -- gen/ proto/events-flat/; \
		exit 1; \
	fi
	@echo "OK: gen/ + proto/events-flat/ reproduce the committed tree byte-for-byte"
