# Event Contract Pruning Proposal — 2026-10-06

**Repository:** `chora-contracts` (source of truth for AsyncAPI + Protobuf event contracts)
**Scope:** every contract subject (NATS topic `chora.{domain}.{aggregate}.{event_type}.v{N}`) with **no producer and no consumer in the live stack**.
**Action:** NONE — this is a report only. No contract was deleted.

## Method

1. Enumerated **256** contract subjects from `asyncapi/**` channel addresses.
2. Built a producer/consumer index over the 19 live-stack services (`chora-stack/compile.prod.yml`) using static analysis:
   - literal topic-string references co-occurring with `Publish`/`Subscribe`/`bind`/`bindEventbus`/`emit`/`InTopic:`/`PublishedEvent{`/`topic=`/`PublishInTx`/`PublishWithError`/`jetBus.*` patterns;
   - topic-constant indirection (`TopicX = "chora..."` resolved through publish/subscribe call sites);
   - path-based role hints (files under `publisher|pubsub|outbox|emit` → producer; `subscriber|consumer|handler|binding|inbox|projector` → consumer).
3. Manually reviewed the "neither" set to remove false negatives (dynamic topic construction in chora-payments `topicForEvent`, function-indirect topics in chora-delivery `EventTopicFor`, Python `topic=TOPIC` emits in chora-ai-kernel-orchestrator, `PublishInTx` in chora-consumption).

**Result:** 131 subjects have a producer and/or consumer; **125 have neither**.

**Limitations:** static analysis only. Topics built by runtime string concatenation or reflection may be under-counted. The platform is pre-M12 (NATS JetStream bus not yet wired — services use in-memory buses), so "no producer/consumer" partly reflects immature event infrastructure, not dead contracts.

## Summary

| Classification | Count | Meaning |
|---|---|---|
| **PRUNE** | 4 | Dead — legacy/superseded. Candidate for removal. |
| **DEFER** | 121 | No producer/consumer found. Keep the contract; decide when the feature ships or is cancelled. |
| **IMPLEMENT** | 0 | — |

> No subject was classified IMPLEMENT: a clear consumer-demand or active emit path was required, and none was found. The 125 "neither" subjects are contracts ahead of the implementation.

## PRUNE (4) — legacy `domain/path` aggregate

All four belong to the legacy `domain/path` aggregate, explicitly consolidated onto `domain/learning_path` (see `asyncapi/consumption/learning-path-completed-v1.yaml`: *"legacy topic retained for backward compatibility but new emits go here"*). No producer/consumer in the live stack.

- **PRUNE** `chora.consumption.path.abandoned.v1` — legacy `domain/path` aggregate; superseded by the `learning_path.*` family per asyncapi.
- **PRUNE** `chora.consumption.path.completed.v1` — legacy `domain/path` aggregate; superseded by the `learning_path.*` family per asyncapi.
- **PRUNE** `chora.consumption.path.enrolled.v1` — legacy `domain/path` aggregate; superseded by the `learning_path.*` family per asyncapi.
- **PRUNE** `chora.consumption.path.step_completed.v1` — legacy `domain/path` aggregate; superseded by the `learning_path.*` family per asyncapi.

## DEFER (121)

### Coordinated-change needed: ADR-254 Companion rename not landed in services (13)

The contract uses the Companion vocabulary (`companion_egg_purchase`, `companion_egg`); the services still emit the pre-rename `familiar_egg_purchase` / `familiar_egg` topic names. The contract is canonical; the services need a coordinated rename before these topics go live.

- **DEFER** `chora.payments.companion_egg_purchase.checkout_started.v1` — contract uses Companion name; service emits the `familiar_*` pre-rename topic (ADR-254). Coordinated rename needed in the service.
- **DEFER** `chora.payments.companion_egg_purchase.expired.v1` — contract uses Companion name; service emits the `familiar_*` pre-rename topic (ADR-254). Coordinated rename needed in the service.
- **DEFER** `chora.payments.companion_egg_purchase.payment_captured.v1` — contract uses Companion name; service emits the `familiar_*` pre-rename topic (ADR-254). Coordinated rename needed in the service.
- **DEFER** `chora.payments.companion_egg_purchase.payment_failed.v1` — contract uses Companion name; service emits the `familiar_*` pre-rename topic (ADR-254). Coordinated rename needed in the service.
- **DEFER** `chora.payments.companion_egg_purchase.refunded.v1` — contract uses Companion name; service emits the `familiar_*` pre-rename topic (ADR-254). Coordinated rename needed in the service.
- **DEFER** `chora.tenancy.companion_egg.checkout_started.v1` — contract uses Companion name; service emits the `familiar_*` pre-rename topic (ADR-254). Coordinated rename needed in the service.
- **DEFER** `chora.tenancy.companion_egg.payment_failed.v1` — contract uses Companion name; service emits the `familiar_*` pre-rename topic (ADR-254). Coordinated rename needed in the service.
- **DEFER** `chora.tenancy.companion_egg.payment_succeeded.v1` — contract uses Companion name; service emits the `familiar_*` pre-rename topic (ADR-254). Coordinated rename needed in the service.
- **DEFER** `chora.tenancy.companion_egg.refunded.v1` — contract uses Companion name; service emits the `familiar_*` pre-rename topic (ADR-254). Coordinated rename needed in the service.

### Coordinated-change needed: `session.*` vs `atom_session.*` mismatch (3)

`session.proto` (aggregate `AtomicSession`, prefix `chora.consumption.session.*`) and `atom_session.proto` (aggregate `AtomSession` — documented alias of `AtomicSession`, prefix `chora.consumption.atom_session.*`) are two contract families for the same aggregate. chora-consumption emits `atom_session.{started,completed,abandoned}`; the `session.*` names have no producer/consumer. (Separate contract gap: `atom_session.started/abandoned` are emitted but have no message in `atom_session.proto`.)

- **DEFER** `chora.consumption.session.abandoned.v1` — same `AtomicSession` aggregate as `atom_session.*` (emitted by chora-consumption); align service emit topic with contract.
- **DEFER** `chora.consumption.session.completed.v1` — same `AtomicSession` aggregate as `atom_session.*` (emitted by chora-consumption); align service emit topic with contract.
- **DEFER** `chora.consumption.session.started.v1` — same `AtomicSession` aggregate as `atom_session.*` (emitted by chora-consumption); align service emit topic with contract.

### No matching event type in the owning service (3)

chora-payments builds topics dynamically (`"chora.payments."+agg+"."+ev+".v1"`). These events have no `EventType` constant in the service, so the (agg, ev) combo is never emitted.

- **DEFER** `chora.payments.user_subscription.created.v1` — no such `EventType` in chora-payments dispatcher.
- **DEFER** `chora.payments.user_subscription.paused.v1` — no such `EventType` in chora-payments dispatcher.
- **DEFER** `chora.payments.user_subscription.renewed.v1` — no such `EventType` in chora-payments dispatcher.

### Unique events with no equivalent (2)

- **DEFER** `chora.consumption.session.answer_validated.v1` — unique `session.proto` message; no `atom_session.*` equivalent; no producer/consumer.
- **DEFER** `chora.consumption.session.atom_interaction_recorded.v1` — unique `session.proto` message; no `atom_session.*` equivalent; no producer/consumer.

### No producer/consumer found; owning service has the aggregate (104)

These subjects have no emit/handle code in the live stack. The owning service may have the aggregate in domain code (candidate for IMPLEMENT when the feature ships) — flagged per domain below.

| Domain | Count | Note |
|---|---|---|
| ai_kernel | 13 | owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found |
| consumption | 4 | owning service chora-consumption emits atom_session/learning_path/kg_* topics but not these; no emit code found |
| delivery | 32 | owning service chora-delivery has the aggregate in domain code but no emit path for these events |
| governance | 9 | owning service chora-governance emits audit/policy/imda topics but not these; no emit code found |
| identity | 21 | owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found |
| notifications | 4 | owning service chora-notifications emits email.*/in_app.created but not these; no emit code found |
| observability | 2 | owning service chora-observability emits token_usage/agent_decision but not these; no emit code found |
| sharing | 6 | owning service chora-sharing emits atom/duel.completed/post.created but not these; no emit code found |
| tenancy | 13 | owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found |

<details><summary>Full list of the 104 DEFER subjects</summary>

- **DEFER** `chora.ai_kernel.broker_budget.exhausted.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.broker_route.decided.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.classifier.intent_classified.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.closure_orchestrator.crypto_shred_orchestrated.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.closure_orchestrator.pseudonymization_orchestrated.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.closure_orchestrator.saga_started.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.crew.atoms_ready.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.crew.member_swapped.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.guardrail.bypassed.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.guardrail.triggered.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.lora.adapter_deployed.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.lora.adapter_retired.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.ai_kernel.lora.adapter_trained.v1` — no producer/consumer in live stack (owning service chora-ai-kernel-orchestrator emits agent/crew/guardrail/invocation topics but none of these; no emit code found).
- **DEFER** `chora.consumption.companion.leveled_up.v1` — no producer/consumer in live stack (owning service chora-consumption emits atom_session/learning_path/kg_* topics but not these; no emit code found).
- **DEFER** `chora.consumption.knowledge_graph.curiosity_score_updated.v1` — no producer/consumer in live stack (owning service chora-consumption emits atom_session/learning_path/kg_* topics but not these; no emit code found).
- **DEFER** `chora.consumption.knowledge_graph.edge_followed.v1` — no producer/consumer in live stack (owning service chora-consumption emits atom_session/learning_path/kg_* topics but not these; no emit code found).
- **DEFER** `chora.consumption.knowledge_graph.node_traversed.v1` — no producer/consumer in live stack (owning service chora-consumption emits atom_session/learning_path/kg_* topics but not these; no emit code found).
- **DEFER** `chora.delivery.booking.cancelled.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.booking.created.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.booking.no_show.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.campusops.equipment_reserved.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.campusops.incident_reported.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.campusops.room_booked.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.certification.expiring_soon.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.certification.revoked.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.course.cancelled.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.course.completed.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.course.scheduled.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.course.updated.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.exam.administered.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.exam.graded.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.exam.proctored.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.exam.scheduled.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.project_group.formed.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.project_group.graded.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.project_group.submitted.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.rostering.finalized.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.rostering.learner_rostered.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.rostering.learner_unrostered.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.skillsfutures.approved.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.skillsfutures.claimed.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.skillsfutures.disbursed.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.skillsfutures.rejected.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.survey.closed.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.survey.distributed.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.survey.response_submitted.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.wbl.placement_completed.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.wbl.placement_created.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.delivery.wbl.placement_started.v1` — no producer/consumer in live stack (owning service chora-delivery has the aggregate in domain code but no emit path for these events).
- **DEFER** `chora.governance.audit.cross_tenant_exam_rollup_viewed.v1` — no producer/consumer in live stack (owning service chora-governance emits audit/policy/imda topics but not these; no emit code found).
- **DEFER** `chora.governance.audit.tenant_import_executed.v1` — no producer/consumer in live stack (owning service chora-governance emits audit/policy/imda topics but not these; no emit code found).
- **DEFER** `chora.governance.closure.saga_failed.v1` — no producer/consumer in live stack (owning service chora-governance emits audit/policy/imda topics but not these; no emit code found).
- **DEFER** `chora.governance.closure.saga_step_completed.v1` — no producer/consumer in live stack (owning service chora-governance emits audit/policy/imda topics but not these; no emit code found).
- **DEFER** `chora.governance.imda.dimension_attested.v1` — no producer/consumer in live stack (owning service chora-governance emits audit/policy/imda topics but not these; no emit code found).
- **DEFER** `chora.governance.imda.evidence_gathered.v1` — no producer/consumer in live stack (owning service chora-governance emits audit/policy/imda topics but not these; no emit code found).
- **DEFER** `chora.governance.policy.published.v1` — no producer/consumer in live stack (owning service chora-governance emits audit/policy/imda topics but not these; no emit code found).
- **DEFER** `chora.governance.policy.retired.v1` — no producer/consumer in live stack (owning service chora-governance emits audit/policy/imda topics but not these; no emit code found).
- **DEFER** `chora.governance.policy.updated.v1` — no producer/consumer in live stack (owning service chora-governance emits audit/policy/imda topics but not these; no emit code found).
- **DEFER** `chora.identity.account.activated.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.account.created.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.account.lifecycle_changed.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.account.reactivated.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.account.suspended.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.agid.issued.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.agid.revoked.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.gcid.issued.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.gcid.linked.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.gcid.merged.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.kyc.submitted.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.role.revoked.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.tenancy_membership.granted.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.tenancy_membership.revoked.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.tenant_import.completed.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.tenant_import.started.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.user_mana.snapshot_taken.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.user_subscription.cancelled.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.user_subscription.created.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.user_subscription.plan_changed.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.identity.user_subscription.renewed.v1` — no producer/consumer in live stack (owning service chora-identity emits account.pseudonymised/gcid.resolved/profile_updated but not these; no emit code found).
- **DEFER** `chora.notifications.in_app.dismissed.v1` — no producer/consumer in live stack (owning service chora-notifications emits email.*/in_app.created but not these; no emit code found).
- **DEFER** `chora.notifications.push.failed.v1` — no producer/consumer in live stack (owning service chora-notifications emits email.*/in_app.created but not these; no emit code found).
- **DEFER** `chora.notifications.push.queued.v1` — no producer/consumer in live stack (owning service chora-notifications emits email.*/in_app.created but not these; no emit code found).
- **DEFER** `chora.notifications.push.sent.v1` — no producer/consumer in live stack (owning service chora-notifications emits email.*/in_app.created but not these; no emit code found).
- **DEFER** `chora.observability.metric.kpi_recorded.v1` — no producer/consumer in live stack (owning service chora-observability emits token_usage/agent_decision but not these; no emit code found).
- **DEFER** `chora.observability.trace.span_finalized.v1` — no producer/consumer in live stack (owning service chora-observability emits token_usage/agent_decision but not these; no emit code found).
- **DEFER** `chora.sharing.duel_round_atoms.completed.v1` — no producer/consumer in live stack (owning service chora-sharing emits atom/duel.completed/post.created but not these; no emit code found).
- **DEFER** `chora.sharing.duel_round_atoms.requested.v1` — no producer/consumer in live stack (owning service chora-sharing emits atom/duel.completed/post.created but not these; no emit code found).
- **DEFER** `chora.sharing.post_moderation.completed.v1` — no producer/consumer in live stack (owning service chora-sharing emits atom/duel.completed/post.created but not these; no emit code found).
- **DEFER** `chora.sharing.post_moderation.requested.v1` — no producer/consumer in live stack (owning service chora-sharing emits atom/duel.completed/post.created but not these; no emit code found).
- **DEFER** `chora.sharing.profile_tags.completed.v1` — no producer/consumer in live stack (owning service chora-sharing emits atom/duel.completed/post.created but not these; no emit code found).
- **DEFER** `chora.sharing.profile_tags.requested.v1` — no producer/consumer in live stack (owning service chora-sharing emits atom/duel.completed/post.created but not these; no emit code found).
- **DEFER** `chora.tenancy.billing.invoice_failed.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.billing.invoice_issued.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.billing.invoice_paid.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.billing.refund_issued.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.subscription.cancelled.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.subscription.created.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.subscription.renewed.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.tenant.closed.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.tenant.member_added.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.tenant.payment_captured.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.tenant.reactivated.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.tenant.suspended.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).
- **DEFER** `chora.tenancy.tenant.whitelabel_configured.v1` — no producer/consumer in live stack (owning service chora-tenancy emits tenant_addon/familiar_egg/tenant.bootstrapped but not these; no emit code found).

</details>

## Coordinated changes needed outside this repo

1. **chora-payments**: rename aggregate `familiar_egg_purchase` → `companion_egg_purchase` (`internal/domain/webhook_event/webhook_event.go:34`, `internal/adapter/outbox/payload.go`) so the 5 `chora.payments.companion_egg_purchase.*` contract topics gain a producer.
2. **chora-tenancy**: rename aggregate `familiar_egg` → `companion_egg` so the 4 `chora.tenancy.companion_egg.*` contract topics gain a producer.
3. **chora-consumption**: align `session.*` vs `atom_session.*` emit names (or add `atom_session.started/abandoned` messages to `atom_session.proto`).
4. **M12 platform**: wire the NATS JetStream bus — until then, even "emitted" topics use in-memory buses and static producer/consumer analysis under-counts live flow.
