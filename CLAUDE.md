# Chora Contracts — CLAUDE.md

> Agent instructions for the `chora-contracts` repository.
> This package is the **API contract boundary** for the entire Chora platform.
> Aligned with Architecture Review locked 2026-05-07. Source-of-truth: `../docs/architecture-review-inputs-2026-05-07.md` Tier 2 D8.

---

## 1. Repository Purpose

This is the **leaf package** in the dependency graph — it has NO internal dependencies. All Chora services depend on `chora-contracts` for their API contracts.

### Contains

- **Protobuf** in `proto/`:
  - `proto/common/envelope.proto` — mandatory `EventEnvelope` for all events
  - `proto/events/{domain}/*.proto` — domain event payloads (source-of-truth)
  - `proto/services/*.proto` — gRPC inter-service contracts (Python orchestrator ↔ Go executor; service mesh internal calls)
- **OpenAPI 3.2 YAML** in `openapi/` — REST admin CRUD per service
- **AsyncAPI 3.0** in `asyncapi/` — human-readable event documentation (one file per topic)
- **GraphQL SDL** in `graphql/` — learner-facing read schemas (Creation/Consumption/Sharing/A2A)
- **Pydantic v2 models** in `src/chora_contracts/` — shared request/response types (Python service consumers; refactor deferred — services may also import from `gen/python/`)
- **Meilisearch index definitions** in `meilisearch/` — full-text search index schemas

### Rules (AP-01: API-First Design — non-negotiable)

1. **Protobuf** in `proto/events/{domain}/{aggregate}.proto` MUST exist BEFORE any event publisher
2. **AsyncAPI** in `asyncapi/{domain}/{topic}.yaml` MUST exist BEFORE event publisher (human docs companion)
3. **OpenAPI** in `openapi/{service}.yaml` MUST exist BEFORE FastAPI / Chi routes
4. **gRPC** proto in `proto/services/*.proto` MUST exist BEFORE gRPC server / client codegen
5. **GraphQL SDL** in `graphql/{domain}.graphql` MUST exist BEFORE Strawberry / gqlgen resolvers
6. Breaking changes → major version bump (new topic `v{N+1}`, new package `v{N+1}`, parallel-publish during migration)
7. NEVER add implementation code — this is specs and generated models only

### Dependency Position

```
chora-contracts  ← NO dependencies (leaf)
        ↑
all services    ← depend directly on chora-contracts/gen/* + Pydantic models
```

---

## 2. Topic Taxonomy (Tier 2 D8)

`chora.{domain}.{aggregate}.{event_type}.v{N}`

- **domain** — one of 11: `creation`, `consumption`, `delivery`, `sharing`, `a2a` (5 core) + `identity`, `tenancy`, `governance`, `observability`, `notifications`, `ai_kernel` (6 supporting)
- **aggregate** — `snake_case` aggregate root name
- **event_type** — `snake_case`, past tense: `created`, `published`, `completed`, `started`, `failed`, `detected`
- **major version** — `v1`, `v2` (breaking change → new topic; never reuse)

Examples: `chora.creation.atom.published.v1`, `chora.consumption.session.completed.v1`, `chora.delivery.cert.issued.v1`, `chora.a2a.contract.invoked.v1`, `chora.identity.account.lifecycle_changed.v1`, `chora.observability.token_usage.recorded.v1` (canonical LLM-cost-emission topic; per ADR-163 the M15 Model Gateway reuses this rather than coining `chora.ai_kernel.model_invoked.v1`).

See `proto/README.md` for full conventions.

---

## 3. Domain Vocabulary

| Canonical Term | What It Means | NEVER Say |
|---|---|---|
| `LearningAtom` | Smallest unit of knowledge — primary aggregate root | question, item, lesson |
| `AtomRevision` | Append-only revision of an atom | atom edit, atom version |
| `LearningPath` | Ordered/queried sequence of atoms (collection aggregate) | course, curriculum |
| `KnowledgeGraph` | Atom-centric graph for discovery mode | topic tree, taxonomy |
| `AtomicSession` | Learner's session over atoms | quiz attempt, study session |
| `Familiar` | RPG learning companion **(domain entity, NOT an AI agent)** | bot, chatbot |
| `GCID` / `GlobalChoraID` | Cross-tenant user identity (UUIDv7, opaque) | user ID, account |
| `AGID` | Agent identity (distinct from GCID; agents CANNOT hold TenantMembership) | agent user, agent account |
| `TenantEntitlement` | Tenant's active add-on config | subscription, tier |
| `AddOnPlan` | Composable feature toggle | tier, subscription |
| `DigitalSkin` | Earned visual reward (NEVER purchasable) | badge |
| `Course` / `Class` | Content Delivery aggregate (admin construct) | training, session |
| `Booking` | Class enrolment / scheduling | reservation |
| `Certification` | Issued credential | badge, achievement |
| `SkillsFutures` | SG SkillsFuture Singapore funding integration | subsidy, grant |

**Familiar discipline**: see memory `feedback_familiar_vs_agent`. Familiar is the in-game RPG companion (a domain entity in Content Consumption); the AI agent powering it is a separate adapter concern (in AI Kernel). Don't conflate.

---

## 4. Domain → Database → Topic Map

| Domain | Owns DB | Topic prefix | Owning team |
|---|---|---|---|
| Content Creation | `chora_creation` | `chora.creation.*` | Team 1 |
| Content Consumption | `chora_consumption` | `chora.consumption.*` | Team 1 |
| Content Delivery | `chora_delivery` | `chora.delivery.*` | Team 2 |
| Content Sharing | `chora_sharing` | `chora.sharing.*` | Team 1 |
| Agent-to-Agent | `chora_a2a` | `chora.a2a.*` | Team 3 |
| Identity | `chora_identity` | `chora.identity.*` | Team 3 |
| Tenancy + Billing | `chora_tenancy` | `chora.tenancy.*` | Team 3 |
| Governance | `chora_governance` | `chora.governance.*` | Team 3 |
| Observability | `chora_observability` | `chora.observability.*` | Team 3 |
| Notifications | `chora_notifications` | `chora.notifications.*` | Team 3 |
| AI Kernel | `chora_ai_kernel` | `chora.ai_kernel.*` | Team 3 |

**Cross-DB queries FORBIDDEN** (no `dblink`). Inter-domain communication via NATS JetStream events ONLY. See `.claude/rules/ddd-enforcement.md`.

---

## 5. Protocol Strategy (per CLAUDE.md §4)

| Query Type | Protocol | Use Case |
|---|---|---|
| Learner-facing reads | **GraphQL** | Knowledge graph traversal, atom queries, persona, discovery, social feeds |
| Admin CRUD | **REST** (OpenAPI) | Atom mgmt, tenant config, add-ons, rostering, certification, IdP |
| Service-to-service sync | **gRPC** (Protobuf services) | Internal calls (Python orchestrator ↔ Go executor; service mesh mTLS) |
| Inter-service async | **NATS JetStream events** | Cross-domain side effects |
| External A2A | **REST** via the API gateway → A2A Gateway service | External agents per ADR-132 |
| Webhooks | **REST** | Partner event subscriptions |

---

## 6. Codegen

- **Go** (services): `protoc-gen-go` + `protoc-gen-go-grpc` + `oapi-codegen` → `gen/go/`
- **Python** (orchestrators / FastAPI services): `grpcio-tools` + `datamodel-code-generator` → `gen/python/`
- **TypeScript / Angular**: not generated from contracts directly — frontend talks GraphQL/REST through gateway

Codegen runs in CI on contract change (`.github/workflows/ci.yml`). Hand-edits to `gen/` rejected.

---

## 7. Python Conventions (legacy `src/chora_contracts/` Pydantic models)

- Python 3.15+, type hints on ALL signatures
- `ruff` for linting/formatting, `mypy --strict` for type checking
- `pydantic` v2
- Naming: `snake_case` files, `PascalCase` classes, `snake_case` functions

(Models in `src/chora_contracts/` are being progressively replaced by Protobuf-generated types; coexistence acceptable during migration.)

---

## 8. Versioning + Compatibility

- **Within v{N}**: additive changes only (optional fields, new enum values, deprecation markers)
- **Breaking change** → new topic with `v{N+1}`, new proto package, parallel-publish during migration, drain old subscriptions, deprecate
- **`buf` CLI** checks `breaking` against main in CI
- **AsyncAPI / OpenAPI**: bump info.version on any change (semver)

---

## 9. References

- `../docs/architecture-review-inputs-2026-05-07.md` (canonical, Tier 1-6 + addenda)
- `../CLAUDE.md` (parent monorepo CLAUDE.md)
- `../.claude/rules/{ddd-enforcement, development-execution, git-workflow}.md` (always-loaded)
- `../.claude/skills/{coding-protobuf, event-driven, pub-sub-topology}/SKILL.md`
- `proto/README.md` (proto-specific conventions)
