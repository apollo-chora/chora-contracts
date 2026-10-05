# Chora Protobuf Contracts

Source-of-truth: `docs/architecture-review-inputs-2026-05-07.md` Tier 2 D8 (Pub/Sub Protobuf Schema Registry) + Tier 1 D2 (5 core + 6 supporting domains).

## Layout

```
proto/
├── common/
│   └── envelope.proto              # EventEnvelope — mandatory wrapper for ALL events
├── events/
│   ├── creation/                   # Content Creation domain
│   ├── consumption/                # Content Consumption domain
│   ├── delivery/                   # Content Delivery domain
│   ├── sharing/                    # Content Sharing domain
│   ├── a2a/                        # Agent-to-Agent domain
│   ├── identity/                   # Identity supporting domain
│   ├── tenancy/                    # Tenancy + Billing supporting domain
│   ├── governance/                 # Governance supporting domain
│   ├── observability/              # Observability supporting domain
│   ├── notifications/              # Notifications supporting domain
│   └── ai_kernel/                  # AI Kernel supporting domain
└── services/
    ├── model_broker_router.proto       # Rules-based routing
    ├── model_broker_gateway.proto      # Model invocation execution
    ├── model_broker_classifier.proto   # Small-model fuzzy classification
    ├── guardrail_service.proto         # Runtime guardrails
    ├── closure_orchestrator.proto      # Federated PII closure saga
    ├── agent_executor.proto            # Go executor for agentic crews
    └── langgraph_orchestrator.proto    # Python LangGraph orchestrator client API
```

## Topic Taxonomy

`chora.{domain}.{aggregate}.{event_type}.v{N}`

- `domain` ∈ 11-domain set (5 core + 6 supporting); see `.claude/rules/ddd-enforcement.md`
- `aggregate` and `event_type` are `snake_case`
- `event_type` is past tense: `created`, `published`, `completed`, `started`, `failed`, `detected`, `recorded`, `logged`
- Major version `v{N}` starts at `v1`. Breaking change → new topic with `v{N+1}`; parallel-publish during migration; drain old subscriptions; deprecate.

## EventEnvelope (mandatory)

Every event message embeds `chora.common.v1.EventEnvelope` as field number 1, named `envelope`. The envelope carries event_id (UUIDv7), idempotency_key, tenant_id, gcid, occurred_at, published_at, traceparent, tracestate, source_project, source_service, schema_version, plus optional correlation_id / causation_id.

```proto
message AtomPublished {
  chora.common.v1.EventEnvelope envelope = 1;
  string atom_id = 2;
  string revision_id = 3;
  // ...
}
```

## Naming Conventions

- **Files**: `{aggregate}.proto` per aggregate root (e.g., `atom.proto`, `session.proto`).
- **Packages**: `chora.{domain}.v1` (e.g., `chora.creation.v1`).
- **Go package**: `github.com/apollo-chora/chora-contracts/gen/go/chora/{domain}/v1;{domain}v1`.
- **Messages**: `PascalCase`, past-tense for events (`AtomPublished`, not `PublishAtom`).
- **Enums**: `SCREAMING_SNAKE_CASE` values; first value MUST be `{ENUM}_UNSPECIFIED = 0`.
- **Field numbers**: 1 reserved for envelope; 2-15 for hot fields (1-byte tag); 16+ for cold fields.

## Schema Evolution

- **Within v1**: additive only (new optional fields, new enum values, deprecation markers)
- **Breaking change** → bump major: new topic + new proto file under `v2/`
- `buf` CLI checks `breaking` against main in CI (per `coding-protobuf` skill)

## gRPC Service Contracts

Inter-service synchronous calls (Python LangGraph orchestrator ↔ Go executor; service-to-service mTLS via the service mesh) use proto definitions in `proto/services/`. Conventions:

- **Service name**: `PascalCase`, named after capability (`ModelBrokerRouter`, `AgentExecutor`)
- **Methods**: `PascalCase` verb-noun (`Route`, `Invoke`, `Classify`, `Execute`, `RunCrew`)
- **Request/Response**: `{Method}Request` / `{Method}Response`
- **Streaming**: prefer server-streaming for token streams; client-streaming for upload/batch

## Codegen

- Go: `protoc-gen-go` + `protoc-gen-go-grpc` → `gen/go/`
- Python: `grpcio-tools` (`protoc-gen-python` + grpc plugin) → `gen/python/`
- TypeScript (frontend GraphQL → REST gateway only; events not consumed by browser): not needed

Codegen happens in CI on contract change. See `.github/workflows/ci.yml`.

## Anti-Patterns

- ❌ Event message without `EventEnvelope` field 1
- ❌ Topic name violating `chora.{domain}.{aggregate}.{event_type}.v{N}` formula
- ❌ Reusing field numbers (forbidden by proto3)
- ❌ Renaming fields (forbidden — wire-incompatible)
- ❌ `enum` without `_UNSPECIFIED = 0` baseline
- ❌ Hand-editing generated files in `gen/`

## References

- `docs/architecture-review-inputs-2026-05-07.md` Tier 2 D8 + Tier 3 D11
- `.claude/rules/ddd-enforcement.md`
- `.claude/skills/coding-protobuf/SKILL.md`
- `.claude/skills/event-driven/SKILL.md`
- `.claude/skills/pub-sub-topology/SKILL.md`
