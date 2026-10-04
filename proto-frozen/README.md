# proto-frozen — the frozen wire

Source protos for event generations whose **topics are still declared in
Terraform and still LIVE in Pub/Sub**, but whose current-generation source has
been renamed or deleted. This tree exists so those flat protos stay **generated**
rather than hand-preserved.

## Why (ruling 44, 2026-09-04)

`protoflatten` used to derive its output from whichever source protos happened to
exist. Terraform derives its `file()` inputs from the **declared topic list**.
Those are not the same set, and nothing checked that they agreed:

| commit | what it did | what broke |
|---|---|---|
| `8f5f2de0a` | ADR-254 Companion rename, 32 sources renamed | `consumption/familiar/*`, `payments/familiar_egg_purchase/*`, `tenancy/familiar_egg/*` stopped being emitted |
| `bca674d98` | deleted the legacy duel sources | `sharing/duel/*` stopped being emitted |

Both were ratified. Neither touched Terraform, which went on declaring those
topics. Because the wrapper wiped the flat tree before regenerating, running the
generator **deleted live wire**, and the dev root module could not plan at all:
39 identical `file()` errors, hand-restored by `27d187ffb`.

## Why these definitions are load-bearing, measured against live

1. **Terraform resolves them.** A missing path fails the WHOLE plan, not one
   resource.
2. **37 of the 39 back a LIVE, BINARY-bound schema.**
3. **The NEW companion topics are bound to the OLD familiar-named schemas.**
   `chora.consumption.companion.created.v1` binds
   `chora-consumption-familiar-created-v1`, exactly as the familiar topic does.
   46 live topics across both generations share 23 schema resources, and there
   are ZERO companion-named schemas live. These definitions are the validating
   wire for **both** generations.

## Rules

- **DO NOT EDIT, RENAME, OR ADD FIELDS.** A field name here is frozen wire. The
  registry treats a field NAME at a fixed tag as part of the frozen wire and
  REFUSES a same-tag rename (ADR-254 line 139, ADR-246 D2). Domain vocabulary is
  Companion; these names are what the live schemas carry.
- **FLATTEN-INPUT ONLY.** `make codegen` runs `buf generate proto`, targeting the
  current module explicitly, so no Go or Python bindings come from here. Pinned
  by `TestGenTreeHasNoFrozenGenerationBindings`.
- **This is a SEPARATE buf module**, not a subdirectory of `proto/`. A frozen file
  may legitimately redeclare a helper the rename never touched:
  `proto-frozen/consumption/ritual.proto` and
  `proto/events/consumption/ritual.proto` both declare `RitualStepStamp`, and one
  module cannot hold both (`RitualStepStamp declared multiple times`).
- **A message is here only if a declared topic needs it.** Events whose topics are
  undeclared or schemaless are deliberately absent, and each absence is noted in
  the file that would otherwise carry it (`FamiliarMatured`,
  `FamiliarAccentUnlocked`, `RitualRunCompleted`, `DuelQuestionSetGenerated`,
  `DuelRoundResolved`).

## Retirement

Deliberate, never a side effect. When a topic is retired from Terraform **and**
its live schema is gone, delete the entry here too. `make flatten` fails loud
naming any declared topic whose source has gone, so the two can never drift
apart silently again.
