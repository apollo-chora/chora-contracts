# chora-contracts CHANGELOG

All notable changes to the Chora API contracts. Aligned with **Architecture
Review locked 2026-05-07** + **2026-05-15 consolidation**. Canonical
architecture doc: `docs/architecture.md` (replaces deleted
`architecture-review-inputs-2026-05-07.md`).

Versioning: SemVer at the package level (this CHANGELOG); per-domain proto
packages carry their own `v1`, `v2` suffix and evolve independently. Major
bumps cluster cross-domain breaking changes into a single release.

## 2026-10-05 — legacy cloud tooling retirement + NATS documentation reconciliation

Follow-up to the event-transport migration below: removed the remaining
non-generated legacy cloud tooling and corrected the documentation inventory.

### Removed

- **`internal/schemaguard/`** (module + tests) and **`scripts/verify-schema-registry.sh`** —
  CI tooling that validated event protos against the legacy managed schema registry
  (`google.golang.org/api/pubsub`; live API plus a checked-in snapshot of committed
  revisions). The platform no longer uses that registry, so the guard had no
  subject. The Makefile targets `verify-schema-registry`,
  `verify-schema-registry-live`, and `refresh-schema-snapshot` are removed with it.
- **`internal/protoflatten/requiredset.go`** (+ tests) and the `-require-topics` /
  `-require-estate` / `-check-only` flags — the required-set check mirrored the
  legacy cloud Terraform `google_pubsub_schema` resources and `pubsub_topics` map. With
  `chora-infra` retired, the mirror had nothing to mirror. The flatten generator
  itself is broker-neutral and is kept.

### Renamed

- The event-schema flatten wrapper is now **`scripts/flatten-event-schemas.sh`**
  (broker-neutral name; it previously carried a broker-specific one). Every
  reference was updated (Makefile, tests, README, tool comments), and the script
  no longer invokes the removed required-set check.

### Documentation

- **`README.md`** — corrected the AsyncAPI count (218 → 302) and the per-domain
  coverage table (13 domains; adds `payments`, drops the retired `support` /
  `closure_orchestrator` rows); refreshed the artefact-inventory counts; rewrote
  the event narrative around NATS JetStream (the
  `chora.{domain}.{aggregate}.{event_type}.v{N}` subjects are unchanged) and
  described `events-flat` as a broker-neutral flattened-schema artifact.
- **`CLAUDE.md`**, **`buf.yaml`**, **`Makefile`** — event-transport wording moved
  from Pub/Sub / Schema Registry to NATS JetStream.

### Notes

- `schema-registry/committed-schemas.json` is left on disk (out of scope for this
  change); it is now an orphaned snapshot with no consumer.
- `openapi/` auth specs are rewritten to the current username/password
  session-mint flow (see the auth entry above).

## 2026-10-05 — Event transport migration: the legacy managed pub/sub → NATS JetStream

The platform event transport is moving from the legacy managed pub/sub to NATS
JetStream. The contracts now describe NATS.

### Changed

- **AsyncAPI** (`asyncapi/`, 302 specs): the `servers` block now describes the
  NATS transport — `nats://localhost:4222`, protocol `nats` — instead of
  `pubsub.googleapis.com` / `googlepubsub` (including the production/emulator
  variants, which collapse to the single local NATS server). Channel addresses
  are unchanged: the `chora.{domain}.{aggregate}.{event_type}.v{N}` taxonomy
  is already valid NATS subject syntax, so no event was renamed.
- **Protobuf media type**: `application/vnd.google.protobuf` →
  `application/x-protobuf` in `defaultContentType` and per-message
  `contentType`/`schemaFormat` (991 occurrences).
- **Descriptions**: legacy cloud product mentions in event descriptions reworded to
  neutral platform terminology — analytics-warehouse consumers → "the analytics
  warehouse" / "analytics streaming", Vertex AI training/deployment targets →
  "model training job" / "model serving endpoint", and the
  `CHORA_OUTBOX_DISPATCH_PUBSUB` flag reference → "outbox dispatch enabled".

### Unchanged

- Protobuf wire format, field numbers, and the `proto/` source-of-truth.
- `proto-frozen/` (frozen wire for retired event generations).
- The `proto/events-flat/` tree and its generators — still generated, but no
  longer describes the platform's event transport.

## 2026-05-23 — Documentation refresh + consistency audit + closure_orchestrator decommission

Plan: `~/.claude/plans/transient-hugging-dewdrop.md` (Phase G + reconciliation B2).
Agent: README + CHANGELOG refresh + audit cross-reference + DEPRECATED closure_orchestrator AsyncAPI removal.

### Reconciliation B2 — DEPRECATED `closure_orchestrator/` AsyncAPI removed

Per the v2.0.0 (2026-05-09) deprecation announcement and the 2026-05-23 audit, the 4 stranded
DEPRECATED AsyncAPI specs under `asyncapi/closure_orchestrator/` have been removed:

- `asyncapi/closure_orchestrator/saga-initiated-v1.yaml` (replaced by `closure/requested-v1.yaml`)
- `asyncapi/closure_orchestrator/saga-advanced-v1.yaml` (replaced by per-state events in `closure/`)
- `asyncapi/closure_orchestrator/saga-completed-v1.yaml` (replaced by `closure/closed-v1.yaml` + `closure/crypto_shred_complete-v1.yaml`)
- `asyncapi/closure_orchestrator/saga-cancelled-v1.yaml` (replaced by `closure/cancelled-v1.yaml`)

The `asyncapi/closure_orchestrator/` directory has also been removed. The v2.0.0 one-release
migration window has long passed (release was 2026-05-09; decommission deferred to v3.0/M14
per original plan, but the audit and plan triage approved Apply for B2). Cross-domain consumers
that still subscribe to `chora.closure_orchestrator.saga.*.v1` will continue to receive events
while producers are migrated; this CHANGELOG change is documentation-only and does not
alter wire-level topic state.

### Documentation

- **`README.md`** — rewritten to reflect the canonical post-2026-05-15
  consolidation. Replaces stale reference to deleted
  `architecture-review-inputs-2026-05-07.md` with `docs/architecture.md`.
  Adds:
  - Artefact inventory (37 OpenAPI + 218 AsyncAPI + 23 gRPC service proto +
    67 events proto + 270 events-flat proto + 8 GraphQL + 114 Go stubs +
    224 Python stubs, audited 2026-05-23).
  - Per-domain coverage matrix highlighting AsyncAPI vs Protobuf drift in
    Creation (+17 Protobuf) / Consumption (+19 Protobuf) /
    Delivery (+20 Protobuf) / Sharing (+3 Protobuf); AsyncAPI surplus in
    AI Kernel (+3) and DEPRECATED `closure_orchestrator/` (+4 to be
    decommissioned per v2.0.0).
  - Pending reconciliation actions (B1-B5 + C1-C3) per audit.
  - Surface-mapped scope (C.H.O.R.A. solid/dotted convention) for the
    full-deployment intention.
  - Guardrails section noting `chora-guardrail` is **DECOMMISSIONED** per
    ADR-152; `guardrail_service.proto` retained on disk for codegen
    continuity, marked deprecated, archive tag
    `archive/chora-guardrail-pre-armor-ga`.

### Audit findings carried into the README (no contract changes in this
  refresh — pending user triage of plan §4 reconciliation actions)

- **6 publisher-without-contract topic families** identified:
  - `chora.wbl.*` (6 active topics from `services/chora-wbl/` — pending
    B3 decision on contract domain naming)
  - `chora.parent.*`, `chora.gamification.events`, `chora.iam.events`
    (likely legacy pre-consolidation residue — pending B4 decommission
    decision)
  - `chora.test.x.v1` (intentionally unversioned test topic)
- **2 ADR-declared topics missing contracts**:
  - `chora.ai_kernel.crew.hitl_paused.v1` (ADR-148 — B5 deferred)
  - `chora.ai_kernel.crew.hitl_resumed.v1` (ADR-148 — B5 deferred)
- **gRPC Wave-2 BFF gap narrowed**: arch.md §5.2.1 estimated ~25-30
  missing RPCs; audit narrowed to **8-12 learner-facing mutation RPCs**.
  Decision needed (C3) on whether arch.md is stale or audit method
  under-counts.
- **`PreviewEggOdds` RPC** appears duplicated / malformed in
  `proto/services/` grep — flagged for quick fix (C2).

### No wire-format changes

This is a documentation refresh only. Source contracts unchanged.
Go/Python codegen unchanged. Topic taxonomy unchanged. Reconciliation
actions are queued in the plan for triage.

## ADR-156 Phase 1 — 2026-05-17 (rename `atom_type` → `question_type` + new fields)

Plan: `docs/m13/atom-phase1-execution-plan-2026-05-17.md` §0 + §1.
Agent: A1 (chora-contracts lane in the ATOM Phase 1 parallel dispatch).

**SIT pre-MVP: in-place edits, NO version bump per Decision #8.** Wire format
preserved (proto field number 3 unchanged); only the *identifier* `atom_type`
renamed to `question_type` across OpenAPI + protobuf + JSON wire-name. The
proto3 enum `chora.creation.v1.AtomType` (10-value) keeps its name in proto
because `QuestionType` is already taken in the same package by the 16-value
FE-facing enum in `proto/events/creation/question.proto` from the M14
Question Authoring CR.

### OpenAPI (`openapi/creation-admin.yaml` + `openapi/creation-questions.yaml`)

- **RENAMED**: schema `AtomType` → `QuestionType`; property `atom_type` →
  `question_type` on `LearningAtom`, `CreateAtomRequest`, `BatchAtomEntry`,
  `AtomWithQuestionProjection`. Old `AtomType` schema retained as
  `deprecated: true` alias for one release cycle; old `atom_type` property
  accepted as a deprecated input synonym (when both supplied, `question_type`
  wins). Removed in next minor release.

- **RELAXED**: `title` is no longer required on `LearningAtom` /
  `CreateAtomRequest` / `BatchAtomEntry` / `AtomWithQuestionProjection`
  (ADR-156 Decision #1). Capped at 256 chars when supplied.

- **NEW REQUIRED**: `stem` (string ≤4096) on `LearningAtom` /
  `CreateAtomRequest` / `BatchAtomEntry` / `AtomWithQuestionProjection` per
  ADR-156 Decision #1. Backfilled in the 0014 migration from
  `mcq_payload.prompt` / `oe_payload.prompt`.

- **NEW OPTIONAL** on the LearningAtom envelope (also surfaced on
  `CreateAtomRequest` / `BatchAtomEntry` / `UpdateAtomRequest` /
  `AtomWithQuestionProjection`):

  - `subject` (string ≤64) — ADR-156 Decision #4 free-text initially
  - `cognitive_level` (enum: `knowledge` / `comprehension` / `application` /
    `analysis` / `synthesis` / `evaluation`) — ADR-156 Decision #3
    Bloom's taxonomy
  - `imda_dimension_tags` (array of enum: `accountability` / `transparency` /
    `safety_robustness` / `fairness_human_oversight`, ≤4 entries) —
    ADR-156 Decision #6 IMDA governance dimensions
  - `author_note` (string) — private author free-text note
  - `media_assets` (array ≤1, Phase 1 image only: `{type, url, alt_text,
    mime, size_bytes}`) — ADR-156 Decision #5 atom-media

- **NEW SCHEMAS**: `QuestionType`, `CognitiveLevel`, `ImdaDimensionTag`,
  `MediaAssetType`, `AtomMediaAsset`.

### Protobuf (`proto/events/creation/atom.proto`)

- **RENAMED**: field `atom_type` (number 3) → `question_type` (number 3
  PRESERVED) on `AtomCreated`, `AtomUpdated`, `AtomPublished`, `AtomArchived`.
  Wire format unchanged — `buf breaking` reports this as a JSON-name change
  only (`atomType` → `questionType`); proto3 binary stays compatible because
  field numbers are the source of identity, not names.

- **NEW MESSAGES + ENUMS**: `AtomMediaAsset`, `AtomMediaAssetType`,
  `CognitiveLevel`, `ImdaDimensionTag`.

- **NEW OPTIONAL FIELDS** on `AtomCreated` / `AtomUpdated` / `AtomPublished`
  (high field numbers 20-25, never reuse):

  ```
  string stem = 20;
  string subject = 21;
  CognitiveLevel cognitive_level = 22;
  repeated ImdaDimensionTag imda_dimension_tags = 23;
  string author_note = 24;
  repeated AtomMediaAsset media_assets = 25;
  ```

### Migration note (consumer impact)

- Go consumers of `chora.creation.v1.AtomCreated.atom_type` MUST migrate to
  `GetQuestionType()` accessor. The Go enum TYPE remains `AtomType` (B-lane
  domain code can introduce `type QuestionType = creationv1.AtomType` as a
  domain alias for ergonomics).
- Python consumers of `chora.creation.v1.AtomCreated.atom_type` MUST migrate
  to `.question_type` attribute.
- OpenAPI consumers (FE / BFF) MUST migrate to `question_type` property name;
  the old `atom_type` request property is accepted as a deprecated synonym
  for one release cycle.
- Cross-domain protos (`proto/services/creation/v1/creation.proto`,
  `proto/services/consumption/v1/consumption.proto`,
  `proto/events/consumption/session.proto`, `proto/events/ai_kernel/crew.proto`)
  declare their OWN `AtomType` enums in different packages — those are NOT
  touched here and will retain `atom_type` field names until their owning
  domain lands its own ATOM-Phase-1 rename (out of scope for ATOM-3).

### Codegen status

- `proto/events-flat/creation/atom/*.proto` regenerated via the event-schema
  flatten script — 266 self-contained protos validated.
- `gen/go/chora/creation/v1/atom.pb.go` regenerated via `buf generate` +
  `scripts/relocate-flat-services.sh` — includes new fields + renamed
  accessors (`GetQuestionType`, `GetStem`, `GetCognitiveLevel`,
  `GetImdaDimensionTags`, `GetAuthorNote`, `GetMediaAssets`).
- `gen/python/events/creation/atom_pb2.py` regenerated via `buf generate` —
  serialized descriptor includes the rename + new fields.

### Wave-1+ consumer impact (expected per dispatch graph)

`services/chora-creation` / `services/chora-delivery` consumer code will fail
to compile against these regenerated stubs until B1 (creation domain +
migration + repo) + B3 (delivery snapshot consumer) + B5 (creation HTTP
handlers) + B6 (creation gRPC AUTHOR-SAFE projection) land in Wave 1+2 of the
ATOM Phase 1 dispatch. That is the intended sequencing.

## v2.1.0 — 2026-05-10 (Path C: self-contained event schemas)

> **Historical (legacy cloud Pub/Sub era).** This release introduced the flattening step
> while the platform's event transport was the legacy managed pub/sub. The platform now
> uses NATS JetStream (see the 2026-10-05 entries); the `proto/events-flat/`
> artifact and its broker-neutral generator are retained.

### Added

1. **`proto/events-flat/`** — generated tree of self-contained per-aggregate
   protos, introduced for the legacy managed schema registry consumption. Each file
   inlines `chora.common.v1.EventEnvelope` + `google.protobuf.Timestamp` (that
   registry rejected schemas with `import` statements).
2. **`internal/protoflatten/`** — Go codegen tool that walks the buf-built
   FileDescriptorSet and emits the flat tree. Field numbers preserved
   exactly to maintain schema-evolution resilience.
3. The event-schema flatten wrapper — runs buf + protoflatten + per-file
   standalone-parse validation.
4. **`tests/test_events_flat_up_to_date.sh`** — CI consistency test
   asserting the committed flat tree matches what protoflatten would emit.
5. **`buf.yaml` `excludes: [proto/events-flat]`** — keeps the flat tree
   outside the buf module so duplicate-symbol errors don't surface during
   `buf lint`/`buf build`/codegen.

### Why

The legacy managed schema registry hard-blocked `import` resolution
(error: `INVALID_PROTO_SCHEMA: "chora.common.v1.EventEnvelope" is not defined`).
Path C generates a parallel self-contained tree while preserving the canonical
`proto/events/` source-of-truth for Go/Python codegen. chora-infra
m10-data-plane read from `proto/events-flat/` for
`google_pubsub_schema.aggregate.definition`; that Terraform wiring is retired
with legacy cloud. See the m10-data-plane variable description
(`enable_pubsub_schema_registry`) for the 4-path comparison + decision rationale.

### No breaking changes

Source contracts unchanged. Go/Python codegen unchanged. Topic taxonomy
unchanged. This is purely additive — a new generated artifact + tooling.

## v2.0.0 — 2026-05-09 (M13 Stage 0.2 Contracts Reconciliation)

Big-bang reconciliation against the 4 audit deltas
(`docs/m13/audit-{identity,content,aikernel,platform}-fillgaps.md`),
the Phyllis MVP spec (`docs/m13/phyllis-mvp-2026-05-08.md`), and the
locked architecture. Reconciliation report:
`docs/m13/contracts-reconciliation-2026-05-09.md`.

### BREAKING CHANGES (require consumer migration)

1. **IMDA dimension labels — wire-breaking enum reuse** (per ADR-141).
   `chora.governance.v1.ImdaDimension` field numbers 1..4 are RE-BOUND
   from the v1 labels (`TRANSPARENCY`/`EXPLAINABILITY`/`REPEATABILITY`/
   `SECURITY_SAFETY`) to the canonical labels
   (`ACCOUNTABILITY`/`TRANSPARENCY`/`SAFETY_AND_ROBUSTNESS`/
   `FAIRNESS_AND_HUMAN_OVERSIGHT`). Producers MUST emit canonical;
   consumers MUST adopt new identifiers. chora-governance Go domain
   already migrated per ADR-141 backwards-alias mode (one release).
2. **Closure saga topic family — namespace consolidation**.
   The legacy `chora.closure_orchestrator.saga.{initiated,advanced,
   completed,cancelled}.v1` topic family + `chora.ai_kernel.closure_orchestrator.{saga_started,
   pseudonymization_orchestrated,crypto_shred_orchestrated}.v1` are
   DEPRECATED in favour of canonical `chora.closure.{requested,
   grace_started,pseudonymise_per_domain_complete,
   crypto_shred_complete,closed,cancelled}.v1`. Per
   `.claude/skills/account-closure-saga/SKILL.md` topic conventions.
   Producers MUST migrate to new topics within one release.

### ADDED

#### Envelope (additive, backwards-compatible)
- `EventEnvelope.chora_imda_dimension` (field 14) — ADR-141 canonical
  IMDA dimension tag for evidence routing.
- `EventEnvelope.imda_lifecycle_stage` (field 15) — 4-stage lifecycle
  classifier (ci_pre_merge / pre_deploy / runtime / post_deploy).

#### Protobuf event definitions
- `proto/events/closure/saga.proto` (NEW) — canonical 6 closure saga
  lifecycle events (ClosureRequested / ClosureGraceStarted /
  ClosurePseudonymisePerDomainComplete / ClosureCryptoShredComplete /
  ClosureClosed / ClosureCancelled). Replaces the deprecated
  `closure_orchestrator.proto` + `governance/closure.proto` clusters.
- `proto/events/identity/role.proto` (NEW) — CourseRole projection
  (`chora.identity.role.granted.v1` + `revoked.v1`). Materialises the
  Phyllis Comic Ch4 P3 dual-card invariant via subscriber to
  `chora.delivery.enrollment.created.v1`.
- `proto/events/delivery/application.proto` (NEW) — Course Application
  aggregate with 7 lifecycle events (submitted / offer_made / accepted /
  withdrawn / paid / enrolled / rejected) + FundingLine breakdown for
  tax-invoice generation.
- `proto/events/tenancy/tenant.proto` — added 3 messages: `TenantGoLive`
  (Phyllis MVP §5.2 endpoint), `TenantMemberAdded`, `TenantPaymentCaptured`.

#### Protobuf service definitions
- `proto/services/guardrail_service.proto` — added `ScreenContent` RPC
  with Phyllis 3-state verdict (`SCREEN_VERDICT_APPROVED` / `REMEDIATED`
  / `BLOCKED`) and inline ContentPolicyViolation event payload for outbox
  publishing. Per audit-aikernel §5 + Phyllis MVP §3 step 4.
- `proto/services/agent_executor.proto` — added documentation block
  enumerating the 6-agent content gate role names
  (`creation-validator` / `creation-classifier` /
  `creation-web-researcher` / `creation-q-and-a-generator` /
  `creation-evaluator` / `creation-reporter`) per audit-aikernel §3.

#### AsyncAPI specs
- `asyncapi/closure/{requested,grace_started,
  pseudonymise_per_domain_complete,crypto_shred_complete,closed,
  cancelled}-v1.yaml` (6 NEW)
- `asyncapi/identity/role-{granted,revoked}-v1.yaml` (2 NEW)
- `asyncapi/delivery/application.{submitted,offer_made,accepted,
  withdrawn,paid,enrolled,rejected}.v1.yaml` (7 NEW)
- `asyncapi/tenancy/tenant-{golive,member-added,payment-captured}-v1.yaml`
  (3 NEW)

#### OpenAPI specs
- `openapi/course-application.yaml` (NEW) — 7-screen Course Application
  REST surface (chora-delivery service). Mounted at `/v1/applications/...`
  per OpenAPI 3.2 convention.
- `openapi/tenancy-admin.yaml` — added `POST /api/v1/admin/tenants/{tenantId}/golive`
  endpoint + `GoLiveTenantRequest` schema.
- `openapi/creation-admin.yaml` — added `POST /atoms/batch` endpoint per
  Phyllis MVP §5.3 (with `idempotency_key` + Gatekeeper screening_decision
  fields per atom).

### CHANGED (additive, no breaking)

- `proto/events/observability/token_usage.proto` — header doc block
  promoted to RECONCILIATION NOTICE: canonical topic is
  `chora.observability.token_usage.recorded.v1`. Three deprecated
  spellings are documented and superseded.
- `proto/events/governance/imda.proto` — header doc block updated to
  reference ADR-141; v2.0 BREAKING note added; deprecated v1 enum
  identifiers documented.
- `proto/events/governance/closure.proto` — header note added pointing
  to canonical `chora.closure.*` family; existing event types preserved
  for governance-domain audit projections.
- `proto/events/ai_kernel/closure_orchestrator.proto` — DEPRECATION
  banner added at top; preserved AS-IS for one release migration window.
- AsyncAPI files for the deprecated closure topics
  (`asyncapi/closure_orchestrator/*.yaml`,
  `asyncapi/ai_kernel/closure-orchestrator-*.yaml`) — title prefixed
  with `[DEPRECATED v2.0]`; descriptions point at canonical replacements.

### DEPRECATED (will be removed in v3.0 — M14)

- `chora.closure_orchestrator.saga.initiated.v1` -> `chora.closure.requested.v1`
- `chora.closure_orchestrator.saga.advanced.v1` -> per-state events at
  `chora.closure.{grace_started,pseudonymise_per_domain_complete,
  crypto_shred_complete,closed}.v1`
- `chora.closure_orchestrator.saga.completed.v1` -> `chora.closure.closed.v1`
  (with `chora.closure.crypto_shred_complete.v1`)
- `chora.closure_orchestrator.saga.cancelled.v1` -> `chora.closure.cancelled.v1`
- `chora.ai_kernel.closure_orchestrator.saga_started.v1` -> `chora.closure.requested.v1`
- `chora.ai_kernel.closure_orchestrator.pseudonymization_orchestrated.v1`
  -> `chora.closure.pseudonymise_per_domain_complete.v1`
- `chora.ai_kernel.closure_orchestrator.crypto_shred_orchestrated.v1`
  -> `chora.closure.crypto_shred_complete.v1`
- v1 IMDA dimension enum identifiers (`IMDA_DIMENSION_TRANSPARENCY` -> D1
  semantics) — superseded by canonical labels per ADR-141.

### REMOVED

(none — backwards-compat preserved for one release)

### Migration notes

Consumer-side migration tickets (CHO-NEW-* placeholders — to be filed
under `M13.B` milestone with `migration` label):
1. **chora-closure-orchestrator**: rewrite publishers to emit on
   `chora.closure.*`; add backwards-compat double-publish path (publish
   to both old + new topics) for one release.
2. **chora-identity**: add subscribers for canonical `chora.closure.*`
   topics; mirror saga state on `closure.Saga` aggregate.
3. **chora-governance**: subscribe to `chora.closure.crypto_shred_complete.v1`
   for IMDA D1 evidence ledger entries (replaces subscriber on
   `chora.ai_kernel.closure_orchestrator.crypto_shred_orchestrated.v1`).
4. **chora-guardrail**: implement `ScreenContent` RPC; ensure existing
   `EvaluateInput` + `EvaluateOutput` clients are unaffected.
5. **chora-creation**: add `POST /atoms/batch` handler routing to the
   existing AI Assist pipeline. Keep the legacy `/atoms:generate` route
   as an alias for one release.
6. **chora-delivery**: implement `Application` aggregate + the 7
   lifecycle events emitters + 11 OpenAPI endpoints for Course
   Application UX. CHO-NEW-1457 tracking ticket.
7. **chora-tenancy**: implement `POST /v1/admin/tenants/{tenantId}/golive`
   endpoint + `TenantGoLive` event publisher.
8. All services: update `ImdaDimension` enum imports to use canonical
   identifiers; consume only canonical labels on the wire (alias mode
   in chora-governance handles legacy producers for one release).
9. All services emitting events: optionally populate
   `EventEnvelope.chora_imda_dimension` + `imda_lifecycle_stage` for
   IMDA evidence routing per ADR-141.

### Codegen verification

- Protobuf: each modified / added `.proto` file passes
  `protoc --proto_path=proto --descriptor_set_out=/dev/null
  proto/<file>.proto` (manual compilation check). Buf lint + buf breaking
  CI pipeline will validate against this baseline at the next CI run.
- AsyncAPI: each new `.yaml` file is AsyncAPI 3.0.0 compliant (validated
  against the schema referenced in `defaultContentType`).
- OpenAPI: `course-application.yaml` is OpenAPI 3.2.0 compliant; existing
  specs unchanged at the schema-engine level (additions only).

---

## v0.1.0 — 2025-12-15 (initial release)

- Pre-architecture-review baseline contracts.
