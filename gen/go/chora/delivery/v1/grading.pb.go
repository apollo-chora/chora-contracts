// =============================================================================
// chora.delivery.v1 — Assessment grading lifecycle events
// =============================================================================
//
// Source-of-truth: docs/architecture/adrs/adr-155-assessment-grading-dispatch.md
// + docs/architecture/qgen-crew-composition-2026-05-12.md §2.4 + §8.5
// + .claude/rules/ddd-enforcement.md (Pub/Sub is the ONLY cross-domain mechanism)
// + .claude/skills/event-driven/SKILL.md
//
// Owning domain  : Content Delivery (chora_delivery database)
// Owning team    : Team 2 (Delivery)
// Topic prefix   : chora.delivery.grading.*
//
// -----------------------------------------------------------------------------
// LOCKED ARCHITECTURAL RULE — agentic dispatch is event-driven, not RPC
// -----------------------------------------------------------------------------
//
// Any LLM/agent invocation in Chora goes through Pub/Sub events,
// orchestrator-managed. Direct sync RPC to a reasoning engine is anti-pattern.
// This is the canonical pattern across qgen_question (ADR-153), Companion chat
// (ADR-154), and grading (this ADR). The orchestrator (Python LangGraph on
// Vertex AI Agent Engine) is responsible for: invoking the agent, managing
// retries, capturing observability, emitting completion events.
//
// -----------------------------------------------------------------------------
// Two distinct grading paths
// -----------------------------------------------------------------------------
//
// 1. MCQ → DETERMINISTIC (in-process Go inside chora-delivery)
//    - Zero LLM calls. Sub-200ms p99.
//    - chora-delivery's submission handler scores MCQs in-process when the
//      submission finalises.
//    - Emits `mcq_completed.v1` as an observability/audit marker so downstream
//      subscribers (R+ monitoring, IMDA D2 evidence trail) can react.
//
// 2. OE → BATCHED LLM EVALUATOR (event-driven; orchestrator-managed)
//    - chora-delivery emits `oe_batch_requested.v1` with full batch payload
//      (rubric + model_answer + per-submission learner_responses[]).
//    - `grading_orchestrator` (Python LangGraph on Vertex AI Agent Engine)
//      subscribes to `oe_batch_requested.v1`. It invokes the `oe_grader`
//      reasoning engine (NEW ADK Go agent, M14.x deliverable — P1 single-agent
//      crew per qgen-crew-composition §8.5; T1 gemini-3.1-pro-preview for
//      LLM-as-judge).
//    - One LLM call per batch — NOT per learner. chora-delivery accumulates
//      ALL OE answers for a given (assessment × question) into one batched
//      payload, dispatched on submission-finalise OR on a batch saturation
//      timer (configurable; default 50 submissions or 60s window).
//    - Orchestrator emits `oe_batch_completed.v1` with per-submission scores
//      + LLM provenance (model_id, response_id) for IMDA D2 transparency.
//    - chora-delivery subscribes to `oe_batch_completed.v1`, persists grades
//      to `chora_delivery.grading_jobs.per_question_grades_jsonb`, and emits
//      `finalized.v1` once both MCQ + OE grades for a submission are persisted.
//
// -----------------------------------------------------------------------------
// Five topics defined here
// -----------------------------------------------------------------------------
//
// chora.delivery.grading.requested.v1
//   Fired by chora-delivery when a submission finalises (the SubmissionSubmitted
//   handler creates a grading job + immediately publishes this event). Marks
//   the start of the grading lifecycle. Consumers: Observability + O+
//   (per-tenant grading-volume metric), R+ instructor monitoring dashboard.
//
// chora.delivery.grading.mcq_completed.v1
//   Fired by chora-delivery's in-process deterministic MCQ scorer after MCQ
//   answers have been graded (the FAST path; usually within ~1s of
//   .requested.v1). Consumers: R+ monitoring (partial-progress UI),
//   Observability (latency histogram bucket).
//
// chora.delivery.grading.oe_batch_requested.v1
//   Fired by chora-delivery's batched dispatcher when an OE batch is ready
//   for the evaluator agent. Carries the full batched payload (rubric +
//   model_answer + per-submission learner_responses[]). Subscribed by the
//   `grading_orchestrator` (Python LangGraph on Vertex AI Agent Engine).
//
// chora.delivery.grading.oe_batch_completed.v1
//   Fired by the `grading_orchestrator` after `oe_grader` returns per-
//   submission scores + feedback. Carries per-submission scoring detail +
//   LLM provenance (model_id, response_id). Subscribed by chora-delivery
//   (to persist grades) + Observability + O+ + chora-governance (IMDA D2
//   evidence — every LLM grade is auditable).
//
// chora.delivery.grading.finalized.v1
//   Fired by chora-delivery after BOTH MCQ + OE grades for a submission are
//   persisted to grading_jobs.per_question_grades_jsonb. Marks the
//   submission's grading job as terminal. Consumers: chora-consumption
//   (XP credit pending release), Notifications (instructor "grading complete"
//   email), R+ monitoring (release-results CTA enabled). The grade itself
//   is NOT released to the learner until the instructor calls
//   `POST /assessments/{id}/release-results` (delivery-assessments.yaml) OR
//   if `grading_config.auto_release=true`.
//
// chora.delivery.grading.failed.v1
//   Fired by chora-delivery when a grading job reaches terminal FAILED state
//   that cannot be auto-recovered. Today the load-bearing producer is the
//   OE-batch completion-event subscriber (services/chora-delivery/internal/
//   adapter/subscribers/grading_inbox.go): when the orchestrator returns
//   batch_outcome != "ok" the subscriber records the failure here so
//   downstream subscribers can react. Consumers: O+ governance (IMDA D1
//   accountability — every grading failure is auditable with full failure
//   category + provenance), R+ monitoring (display "Re-run grading" CTA on
//   the dashboard), Observability (alerting on failure-rate spikes). NOT a
//   replay of the OE batch input — this is the *grading-job-level* failure
//   marker independent of the batch-level oe_batch_completed.v1 (which
//   already carries failure_category for the BATCH dispatch path).
//
// EVERY event carries the full chora.common.v1.EventEnvelope as field 1 per
// chora-contracts CLAUDE.md §2 + envelope.proto.
// =============================================================================

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.11
// 	protoc        (unknown)
// source: events/delivery/grading.proto

package deliveryv1

import (
	v1 "github.com/apollo-chora/chora-contracts/gen/go/chora/common/v1"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	reflect "reflect"
	sync "sync"
	unsafe "unsafe"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// -----------------------------------------------------------------------------
// GradingDispatch — discriminator for per-question grading path
// -----------------------------------------------------------------------------
//
// Mirrors GradingDispatch in delivery-grading-results read-models. Lives here
// in the proto layer so subscribers can fan-out per-dispatch.
type GradingDispatch int32

const (
	GradingDispatch_GRADING_DISPATCH_UNSPECIFIED   GradingDispatch = 0
	GradingDispatch_GRADING_DISPATCH_DETERMINISTIC GradingDispatch = 1 // MCQ — in-process Go scorer
	GradingDispatch_GRADING_DISPATCH_LLM_EVALUATOR GradingDispatch = 2 // OE — batched evaluator agent
)

// Enum value maps for GradingDispatch.
var (
	GradingDispatch_name = map[int32]string{
		0: "GRADING_DISPATCH_UNSPECIFIED",
		1: "GRADING_DISPATCH_DETERMINISTIC",
		2: "GRADING_DISPATCH_LLM_EVALUATOR",
	}
	GradingDispatch_value = map[string]int32{
		"GRADING_DISPATCH_UNSPECIFIED":   0,
		"GRADING_DISPATCH_DETERMINISTIC": 1,
		"GRADING_DISPATCH_LLM_EVALUATOR": 2,
	}
)

func (x GradingDispatch) Enum() *GradingDispatch {
	p := new(GradingDispatch)
	*p = x
	return p
}

func (x GradingDispatch) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (GradingDispatch) Descriptor() protoreflect.EnumDescriptor {
	return file_events_delivery_grading_proto_enumTypes[0].Descriptor()
}

func (GradingDispatch) Type() protoreflect.EnumType {
	return &file_events_delivery_grading_proto_enumTypes[0]
}

func (x GradingDispatch) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use GradingDispatch.Descriptor instead.
func (GradingDispatch) EnumDescriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{0}
}

// -----------------------------------------------------------------------------
// GradingJobStatus — high-level lifecycle of the grading job aggregate
// -----------------------------------------------------------------------------
//
// FSM:
//
//	REQUESTED → RUNNING → MCQ_DETERMINISTIC_DONE
//	                       ├─→ (no OE)  SCORED → FINALIZED
//	                       └─→ (has OE) OE_BATCH_PENDING → OE_BATCH_COMPLETE
//	                                    → SCORED → FINALIZED
//	any → FAILED (terminal)
//
// SCORED + FINALIZED differ: SCORED = all per-question grades computed but
// not yet persisted to Submission.result_jsonb. FINALIZED = grades persisted
// (and `chora.delivery.grading.finalized.v1` has been emitted).
type GradingJobStatus int32

const (
	GradingJobStatus_GRADING_JOB_STATUS_UNSPECIFIED            GradingJobStatus = 0
	GradingJobStatus_GRADING_JOB_STATUS_REQUESTED              GradingJobStatus = 1
	GradingJobStatus_GRADING_JOB_STATUS_RUNNING                GradingJobStatus = 2
	GradingJobStatus_GRADING_JOB_STATUS_MCQ_DETERMINISTIC_DONE GradingJobStatus = 3
	GradingJobStatus_GRADING_JOB_STATUS_OE_BATCH_PENDING       GradingJobStatus = 4
	GradingJobStatus_GRADING_JOB_STATUS_OE_BATCH_COMPLETE      GradingJobStatus = 5
	GradingJobStatus_GRADING_JOB_STATUS_SCORED                 GradingJobStatus = 6
	GradingJobStatus_GRADING_JOB_STATUS_FINALIZED              GradingJobStatus = 7
	GradingJobStatus_GRADING_JOB_STATUS_FAILED                 GradingJobStatus = 8
)

// Enum value maps for GradingJobStatus.
var (
	GradingJobStatus_name = map[int32]string{
		0: "GRADING_JOB_STATUS_UNSPECIFIED",
		1: "GRADING_JOB_STATUS_REQUESTED",
		2: "GRADING_JOB_STATUS_RUNNING",
		3: "GRADING_JOB_STATUS_MCQ_DETERMINISTIC_DONE",
		4: "GRADING_JOB_STATUS_OE_BATCH_PENDING",
		5: "GRADING_JOB_STATUS_OE_BATCH_COMPLETE",
		6: "GRADING_JOB_STATUS_SCORED",
		7: "GRADING_JOB_STATUS_FINALIZED",
		8: "GRADING_JOB_STATUS_FAILED",
	}
	GradingJobStatus_value = map[string]int32{
		"GRADING_JOB_STATUS_UNSPECIFIED":            0,
		"GRADING_JOB_STATUS_REQUESTED":              1,
		"GRADING_JOB_STATUS_RUNNING":                2,
		"GRADING_JOB_STATUS_MCQ_DETERMINISTIC_DONE": 3,
		"GRADING_JOB_STATUS_OE_BATCH_PENDING":       4,
		"GRADING_JOB_STATUS_OE_BATCH_COMPLETE":      5,
		"GRADING_JOB_STATUS_SCORED":                 6,
		"GRADING_JOB_STATUS_FINALIZED":              7,
		"GRADING_JOB_STATUS_FAILED":                 8,
	}
)

func (x GradingJobStatus) Enum() *GradingJobStatus {
	p := new(GradingJobStatus)
	*p = x
	return p
}

func (x GradingJobStatus) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (GradingJobStatus) Descriptor() protoreflect.EnumDescriptor {
	return file_events_delivery_grading_proto_enumTypes[1].Descriptor()
}

func (GradingJobStatus) Type() protoreflect.EnumType {
	return &file_events_delivery_grading_proto_enumTypes[1]
}

func (x GradingJobStatus) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use GradingJobStatus.Descriptor instead.
func (GradingJobStatus) EnumDescriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{1}
}

// -----------------------------------------------------------------------------
// GradingFailureCategory — short category for FAILED events
// -----------------------------------------------------------------------------
type GradingFailureCategory int32

const (
	GradingFailureCategory_GRADING_FAILURE_CATEGORY_UNSPECIFIED              GradingFailureCategory = 0
	GradingFailureCategory_GRADING_FAILURE_CATEGORY_VERTEX_AI_5XX            GradingFailureCategory = 1
	GradingFailureCategory_GRADING_FAILURE_CATEGORY_MODEL_ARMOR_BLOCKED      GradingFailureCategory = 2
	GradingFailureCategory_GRADING_FAILURE_CATEGORY_TIMEOUT                  GradingFailureCategory = 3
	GradingFailureCategory_GRADING_FAILURE_CATEGORY_QUOTA_EXCEEDED           GradingFailureCategory = 4
	GradingFailureCategory_GRADING_FAILURE_CATEGORY_INSUFFICIENT_TENANT_MANA GradingFailureCategory = 5
	GradingFailureCategory_GRADING_FAILURE_CATEGORY_INTERNAL_ERROR           GradingFailureCategory = 6
)

// Enum value maps for GradingFailureCategory.
var (
	GradingFailureCategory_name = map[int32]string{
		0: "GRADING_FAILURE_CATEGORY_UNSPECIFIED",
		1: "GRADING_FAILURE_CATEGORY_VERTEX_AI_5XX",
		2: "GRADING_FAILURE_CATEGORY_MODEL_ARMOR_BLOCKED",
		3: "GRADING_FAILURE_CATEGORY_TIMEOUT",
		4: "GRADING_FAILURE_CATEGORY_QUOTA_EXCEEDED",
		5: "GRADING_FAILURE_CATEGORY_INSUFFICIENT_TENANT_MANA",
		6: "GRADING_FAILURE_CATEGORY_INTERNAL_ERROR",
	}
	GradingFailureCategory_value = map[string]int32{
		"GRADING_FAILURE_CATEGORY_UNSPECIFIED":              0,
		"GRADING_FAILURE_CATEGORY_VERTEX_AI_5XX":            1,
		"GRADING_FAILURE_CATEGORY_MODEL_ARMOR_BLOCKED":      2,
		"GRADING_FAILURE_CATEGORY_TIMEOUT":                  3,
		"GRADING_FAILURE_CATEGORY_QUOTA_EXCEEDED":           4,
		"GRADING_FAILURE_CATEGORY_INSUFFICIENT_TENANT_MANA": 5,
		"GRADING_FAILURE_CATEGORY_INTERNAL_ERROR":           6,
	}
)

func (x GradingFailureCategory) Enum() *GradingFailureCategory {
	p := new(GradingFailureCategory)
	*p = x
	return p
}

func (x GradingFailureCategory) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (GradingFailureCategory) Descriptor() protoreflect.EnumDescriptor {
	return file_events_delivery_grading_proto_enumTypes[2].Descriptor()
}

func (GradingFailureCategory) Type() protoreflect.EnumType {
	return &file_events_delivery_grading_proto_enumTypes[2]
}

func (x GradingFailureCategory) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use GradingFailureCategory.Descriptor instead.
func (GradingFailureCategory) EnumDescriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{2}
}

// -----------------------------------------------------------------------------
// GradeOverrideField — which artifact a HITL override touched
// -----------------------------------------------------------------------------
type GradeOverrideField int32

const (
	GradeOverrideField_GRADE_OVERRIDE_FIELD_UNSPECIFIED     GradeOverrideField = 0
	GradeOverrideField_GRADE_OVERRIDE_FIELD_SCORE           GradeOverrideField = 1
	GradeOverrideField_GRADE_OVERRIDE_FIELD_COMMENT         GradeOverrideField = 2 // per-question grader comment
	GradeOverrideField_GRADE_OVERRIDE_FIELD_MODEL_ANSWER    GradeOverrideField = 3 // canonical model answer (→ creation)
	GradeOverrideField_GRADE_OVERRIDE_FIELD_OVERALL_COMMENT GradeOverrideField = 4 // submission-level narrative
)

// Enum value maps for GradeOverrideField.
var (
	GradeOverrideField_name = map[int32]string{
		0: "GRADE_OVERRIDE_FIELD_UNSPECIFIED",
		1: "GRADE_OVERRIDE_FIELD_SCORE",
		2: "GRADE_OVERRIDE_FIELD_COMMENT",
		3: "GRADE_OVERRIDE_FIELD_MODEL_ANSWER",
		4: "GRADE_OVERRIDE_FIELD_OVERALL_COMMENT",
	}
	GradeOverrideField_value = map[string]int32{
		"GRADE_OVERRIDE_FIELD_UNSPECIFIED":     0,
		"GRADE_OVERRIDE_FIELD_SCORE":           1,
		"GRADE_OVERRIDE_FIELD_COMMENT":         2,
		"GRADE_OVERRIDE_FIELD_MODEL_ANSWER":    3,
		"GRADE_OVERRIDE_FIELD_OVERALL_COMMENT": 4,
	}
)

func (x GradeOverrideField) Enum() *GradeOverrideField {
	p := new(GradeOverrideField)
	*p = x
	return p
}

func (x GradeOverrideField) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (GradeOverrideField) Descriptor() protoreflect.EnumDescriptor {
	return file_events_delivery_grading_proto_enumTypes[3].Descriptor()
}

func (GradeOverrideField) Type() protoreflect.EnumType {
	return &file_events_delivery_grading_proto_enumTypes[3]
}

func (x GradeOverrideField) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use GradeOverrideField.Descriptor instead.
func (GradeOverrideField) EnumDescriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{3}
}

// QuestionGradeBatchEntry — a single learner's submission to be graded by
// the batched OE evaluator. Carried inside oe_batch_requested.v1's
// `batch_submissions[]`.
type QuestionGradeBatchEntry struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// UUIDv7 of the parent submission row (chora_delivery.submissions).
	SubmissionId string `protobuf:"bytes,1,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	// UUIDv7 of the test_set_question row (per-inclusion identifier).
	TestSetQuestionId string `protobuf:"bytes,2,opt,name=test_set_question_id,json=testSetQuestionId,proto3" json:"test_set_question_id,omitempty"`
	// UUIDv7 of the underlying chora_creation question.
	QuestionId string `protobuf:"bytes,3,opt,name=question_id,json=questionId,proto3" json:"question_id,omitempty"`
	// GCID of the learner whose answer is being graded.
	LearnerGcid string `protobuf:"bytes,4,opt,name=learner_gcid,json=learnerGcid,proto3" json:"learner_gcid,omitempty"`
	// The learner's free-text OE response.
	ResponseText string `protobuf:"bytes,5,opt,name=response_text,json=responseText,proto3" json:"response_text,omitempty"`
	// Optional learner-supplied context (e.g., assistive accommodations
	// annotations). Free-form JSON string for forward-compat.
	AccommodationsJson string `protobuf:"bytes,6,opt,name=accommodations_json,json=accommodationsJson,proto3" json:"accommodations_json,omitempty"`
	unknownFields      protoimpl.UnknownFields
	sizeCache          protoimpl.SizeCache
}

func (x *QuestionGradeBatchEntry) Reset() {
	*x = QuestionGradeBatchEntry{}
	mi := &file_events_delivery_grading_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *QuestionGradeBatchEntry) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*QuestionGradeBatchEntry) ProtoMessage() {}

func (x *QuestionGradeBatchEntry) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use QuestionGradeBatchEntry.ProtoReflect.Descriptor instead.
func (*QuestionGradeBatchEntry) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{0}
}

func (x *QuestionGradeBatchEntry) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *QuestionGradeBatchEntry) GetTestSetQuestionId() string {
	if x != nil {
		return x.TestSetQuestionId
	}
	return ""
}

func (x *QuestionGradeBatchEntry) GetQuestionId() string {
	if x != nil {
		return x.QuestionId
	}
	return ""
}

func (x *QuestionGradeBatchEntry) GetLearnerGcid() string {
	if x != nil {
		return x.LearnerGcid
	}
	return ""
}

func (x *QuestionGradeBatchEntry) GetResponseText() string {
	if x != nil {
		return x.ResponseText
	}
	return ""
}

func (x *QuestionGradeBatchEntry) GetAccommodationsJson() string {
	if x != nil {
		return x.AccommodationsJson
	}
	return ""
}

// QuestionGradeBatchResult — per-submission scoring output from the OE
// evaluator. Carried inside oe_batch_completed.v1's `graded[]`.
type QuestionGradeBatchResult struct {
	state             protoimpl.MessageState `protogen:"open.v1"`
	SubmissionId      string                 `protobuf:"bytes,1,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	TestSetQuestionId string                 `protobuf:"bytes,2,opt,name=test_set_question_id,json=testSetQuestionId,proto3" json:"test_set_question_id,omitempty"`
	QuestionId        string                 `protobuf:"bytes,3,opt,name=question_id,json=questionId,proto3" json:"question_id,omitempty"`
	LearnerGcid       string                 `protobuf:"bytes,4,opt,name=learner_gcid,json=learnerGcid,proto3" json:"learner_gcid,omitempty"`
	// Composite score (0..points_possible). Float — supports rubric-weighted
	// partial credit.
	PointsEarned float32 `protobuf:"fixed32,5,opt,name=points_earned,json=pointsEarned,proto3" json:"points_earned,omitempty"`
	// Points possible (snapshot of TestSetQuestion.points at grading time).
	PointsPossible int32 `protobuf:"varint,6,opt,name=points_possible,json=pointsPossible,proto3" json:"points_possible,omitempty"`
	// Per-rubric-criterion sub-scores (JSON-encoded for forward-compat —
	// criterion_id / title / score / max_score / feedback).
	CriterionScoresJson string `protobuf:"bytes,7,opt,name=criterion_scores_json,json=criterionScoresJson,proto3" json:"criterion_scores_json,omitempty"`
	// Markdown-formatted human-facing feedback from the evaluator. Surfaced
	// to learners only after the parent assessment is RELEASED.
	LlmEvaluatorFeedback string `protobuf:"bytes,8,opt,name=llm_evaluator_feedback,json=llmEvaluatorFeedback,proto3" json:"llm_evaluator_feedback,omitempty"`
	// Vendor model_id used (e.g., "gemini-3.1-pro-preview"). Pinned per
	// request for IMDA D2 transparency.
	GradingModelId string `protobuf:"bytes,9,opt,name=grading_model_id,json=gradingModelId,proto3" json:"grading_model_id,omitempty"`
	// Vendor response_id for provenance auditing (IMDA D2).
	GradingResponseId string `protobuf:"bytes,10,opt,name=grading_response_id,json=gradingResponseId,proto3" json:"grading_response_id,omitempty"`
	// Per-criterion or per-axis decisions (free JSON for evaluator-specific
	// shape). Forward-compat hook.
	GraderMetadataJson string `protobuf:"bytes,11,opt,name=grader_metadata_json,json=graderMetadataJson,proto3" json:"grader_metadata_json,omitempty"`
	unknownFields      protoimpl.UnknownFields
	sizeCache          protoimpl.SizeCache
}

func (x *QuestionGradeBatchResult) Reset() {
	*x = QuestionGradeBatchResult{}
	mi := &file_events_delivery_grading_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *QuestionGradeBatchResult) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*QuestionGradeBatchResult) ProtoMessage() {}

func (x *QuestionGradeBatchResult) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use QuestionGradeBatchResult.ProtoReflect.Descriptor instead.
func (*QuestionGradeBatchResult) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{1}
}

func (x *QuestionGradeBatchResult) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *QuestionGradeBatchResult) GetTestSetQuestionId() string {
	if x != nil {
		return x.TestSetQuestionId
	}
	return ""
}

func (x *QuestionGradeBatchResult) GetQuestionId() string {
	if x != nil {
		return x.QuestionId
	}
	return ""
}

func (x *QuestionGradeBatchResult) GetLearnerGcid() string {
	if x != nil {
		return x.LearnerGcid
	}
	return ""
}

func (x *QuestionGradeBatchResult) GetPointsEarned() float32 {
	if x != nil {
		return x.PointsEarned
	}
	return 0
}

func (x *QuestionGradeBatchResult) GetPointsPossible() int32 {
	if x != nil {
		return x.PointsPossible
	}
	return 0
}

func (x *QuestionGradeBatchResult) GetCriterionScoresJson() string {
	if x != nil {
		return x.CriterionScoresJson
	}
	return ""
}

func (x *QuestionGradeBatchResult) GetLlmEvaluatorFeedback() string {
	if x != nil {
		return x.LlmEvaluatorFeedback
	}
	return ""
}

func (x *QuestionGradeBatchResult) GetGradingModelId() string {
	if x != nil {
		return x.GradingModelId
	}
	return ""
}

func (x *QuestionGradeBatchResult) GetGradingResponseId() string {
	if x != nil {
		return x.GradingResponseId
	}
	return ""
}

func (x *QuestionGradeBatchResult) GetGraderMetadataJson() string {
	if x != nil {
		return x.GraderMetadataJson
	}
	return ""
}

// -----------------------------------------------------------------------------
// GradingRequested — chora.delivery.grading.requested.v1
// -----------------------------------------------------------------------------
//
// Fired by chora-delivery when a submission finalises (POST .../submit lands
// in delivery-assessments.yaml). Marks the start of the grading lifecycle.
//
// Consumers:
//   - Observability + O+ (per-tenant grading-volume metric)
//   - R+ instructor monitoring dashboard (in-progress count → graded count)
//   - chora-governance (IMDA D1 accountability — record that grading was
//     attempted for every finalised submission)
type GradingRequested struct {
	state    protoimpl.MessageState `protogen:"open.v1"`
	Envelope *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	// UUIDv7 of the grading job (1:1 with submission attempt; new job on regrade).
	GradingJobId string `protobuf:"bytes,2,opt,name=grading_job_id,json=gradingJobId,proto3" json:"grading_job_id,omitempty"`
	// UUIDv7 of the parent submission (chora_delivery.submissions).
	SubmissionId string `protobuf:"bytes,3,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	// UUIDv7 of the assessment (chora_delivery.assessments).
	AssessmentId string `protobuf:"bytes,4,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	// UUIDv7 of the test-set the assessment instantiated (chora_delivery.test_sets).
	TestSetId string `protobuf:"bytes,5,opt,name=test_set_id,json=testSetId,proto3" json:"test_set_id,omitempty"`
	// GCID of the learner whose submission is being graded.
	LearnerGcid string `protobuf:"bytes,6,opt,name=learner_gcid,json=learnerGcid,proto3" json:"learner_gcid,omitempty"`
	// Total question count + total points (snapshot at grading time).
	QuestionCount       int32 `protobuf:"varint,7,opt,name=question_count,json=questionCount,proto3" json:"question_count,omitempty"`
	TotalPointsPossible int32 `protobuf:"varint,8,opt,name=total_points_possible,json=totalPointsPossible,proto3" json:"total_points_possible,omitempty"`
	// Question-type mix (informational; helps subscribers pre-allocate).
	McqCount int32 `protobuf:"varint,9,opt,name=mcq_count,json=mcqCount,proto3" json:"mcq_count,omitempty"`
	OeCount  int32 `protobuf:"varint,10,opt,name=oe_count,json=oeCount,proto3" json:"oe_count,omitempty"`
	// Configured passing threshold (0..100 percent).
	PassingThresholdPercent int32 `protobuf:"varint,11,opt,name=passing_threshold_percent,json=passingThresholdPercent,proto3" json:"passing_threshold_percent,omitempty"`
	// Whether this job is a regrade (force_regrade flag from the trigger).
	ForceRegrade bool `protobuf:"varint,12,opt,name=force_regrade,json=forceRegrade,proto3" json:"force_regrade,omitempty"`
	// When the grading job was created at the producer's domain database.
	RequestedAt   *timestamppb.Timestamp `protobuf:"bytes,13,opt,name=requested_at,json=requestedAt,proto3" json:"requested_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GradingRequested) Reset() {
	*x = GradingRequested{}
	mi := &file_events_delivery_grading_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingRequested) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingRequested) ProtoMessage() {}

func (x *GradingRequested) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingRequested.ProtoReflect.Descriptor instead.
func (*GradingRequested) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{2}
}

func (x *GradingRequested) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingRequested) GetGradingJobId() string {
	if x != nil {
		return x.GradingJobId
	}
	return ""
}

func (x *GradingRequested) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingRequested) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingRequested) GetTestSetId() string {
	if x != nil {
		return x.TestSetId
	}
	return ""
}

func (x *GradingRequested) GetLearnerGcid() string {
	if x != nil {
		return x.LearnerGcid
	}
	return ""
}

func (x *GradingRequested) GetQuestionCount() int32 {
	if x != nil {
		return x.QuestionCount
	}
	return 0
}

func (x *GradingRequested) GetTotalPointsPossible() int32 {
	if x != nil {
		return x.TotalPointsPossible
	}
	return 0
}

func (x *GradingRequested) GetMcqCount() int32 {
	if x != nil {
		return x.McqCount
	}
	return 0
}

func (x *GradingRequested) GetOeCount() int32 {
	if x != nil {
		return x.OeCount
	}
	return 0
}

func (x *GradingRequested) GetPassingThresholdPercent() int32 {
	if x != nil {
		return x.PassingThresholdPercent
	}
	return 0
}

func (x *GradingRequested) GetForceRegrade() bool {
	if x != nil {
		return x.ForceRegrade
	}
	return false
}

func (x *GradingRequested) GetRequestedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.RequestedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingMcqCompleted — chora.delivery.grading.mcq_completed.v1
// -----------------------------------------------------------------------------
//
// Fired by chora-delivery's in-process deterministic MCQ scorer after MCQ
// answers for a submission have been graded. Carries per-question MCQ
// outcomes for observability + the R+ partial-progress UI.
//
// This event is a MARKER — chora-delivery has already persisted the MCQ
// grades inline; consumers read them via gRPC GetGradingJob if they need
// detail. The list of per_question_grades inline here is the OBSERVABILITY
// projection (full per-rubric detail is in the in-DB record).
//
// Consumers:
//   - R+ monitoring (partial-progress UI — show "MCQ scored, OE pending")
//   - Observability (latency histogram bucket — submit → mcq_completed
//     should be sub-200ms p99)
//   - chora-governance (audit trail — deterministic grades have ZERO LLM
//     evidence, only the deterministic compare-to-key event)
type GradingMcqCompleted struct {
	state        protoimpl.MessageState `protogen:"open.v1"`
	Envelope     *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	GradingJobId string                 `protobuf:"bytes,2,opt,name=grading_job_id,json=gradingJobId,proto3" json:"grading_job_id,omitempty"`
	SubmissionId string                 `protobuf:"bytes,3,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	AssessmentId string                 `protobuf:"bytes,4,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	LearnerGcid  string                 `protobuf:"bytes,5,opt,name=learner_gcid,json=learnerGcid,proto3" json:"learner_gcid,omitempty"`
	// Aggregate MCQ scoring outcome.
	McqCorrectCount   int32   `protobuf:"varint,6,opt,name=mcq_correct_count,json=mcqCorrectCount,proto3" json:"mcq_correct_count,omitempty"`
	McqIncorrectCount int32   `protobuf:"varint,7,opt,name=mcq_incorrect_count,json=mcqIncorrectCount,proto3" json:"mcq_incorrect_count,omitempty"`
	McqPointsEarned   float32 `protobuf:"fixed32,8,opt,name=mcq_points_earned,json=mcqPointsEarned,proto3" json:"mcq_points_earned,omitempty"`
	McqPointsPossible int32   `protobuf:"varint,9,opt,name=mcq_points_possible,json=mcqPointsPossible,proto3" json:"mcq_points_possible,omitempty"`
	// True when there are no OE questions on the assessment — implies the
	// job transitions directly to SCORED + (per finalize flow) FINALIZED.
	NoOePending    bool                   `protobuf:"varint,10,opt,name=no_oe_pending,json=noOePending,proto3" json:"no_oe_pending,omitempty"`
	McqCompletedAt *timestamppb.Timestamp `protobuf:"bytes,11,opt,name=mcq_completed_at,json=mcqCompletedAt,proto3" json:"mcq_completed_at,omitempty"`
	unknownFields  protoimpl.UnknownFields
	sizeCache      protoimpl.SizeCache
}

func (x *GradingMcqCompleted) Reset() {
	*x = GradingMcqCompleted{}
	mi := &file_events_delivery_grading_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingMcqCompleted) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingMcqCompleted) ProtoMessage() {}

func (x *GradingMcqCompleted) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingMcqCompleted.ProtoReflect.Descriptor instead.
func (*GradingMcqCompleted) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{3}
}

func (x *GradingMcqCompleted) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingMcqCompleted) GetGradingJobId() string {
	if x != nil {
		return x.GradingJobId
	}
	return ""
}

func (x *GradingMcqCompleted) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingMcqCompleted) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingMcqCompleted) GetLearnerGcid() string {
	if x != nil {
		return x.LearnerGcid
	}
	return ""
}

func (x *GradingMcqCompleted) GetMcqCorrectCount() int32 {
	if x != nil {
		return x.McqCorrectCount
	}
	return 0
}

func (x *GradingMcqCompleted) GetMcqIncorrectCount() int32 {
	if x != nil {
		return x.McqIncorrectCount
	}
	return 0
}

func (x *GradingMcqCompleted) GetMcqPointsEarned() float32 {
	if x != nil {
		return x.McqPointsEarned
	}
	return 0
}

func (x *GradingMcqCompleted) GetMcqPointsPossible() int32 {
	if x != nil {
		return x.McqPointsPossible
	}
	return 0
}

func (x *GradingMcqCompleted) GetNoOePending() bool {
	if x != nil {
		return x.NoOePending
	}
	return false
}

func (x *GradingMcqCompleted) GetMcqCompletedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.McqCompletedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingOeBatchRequested — chora.delivery.grading.oe_batch_requested.v1
// -----------------------------------------------------------------------------
//
// Fired by chora-delivery's batched OE dispatcher when an OE batch is ready
// for the evaluator agent. Carries the full payload the orchestrator needs
// to invoke `oe_grader`.
//
// Subscribed by `grading_orchestrator` (Python LangGraph on Vertex AI Agent
// Engine). The orchestrator:
//  1. Picks up the event via Pub/Sub subscription
//  2. Validates schema + TenantManaPool balance (gRPC to chora-tenancy)
//  3. Invokes `oe_grader` reasoning engine with the batched payload
//  4. Emits `oe_batch_completed.v1` on agent return
//
// Cost-optimised: ONE event per (assessment × question × batch-window) —
// not per learner. Batch window = max 50 submissions or 60s elapsed
// (configurable per tenant).
type GradingOeBatchRequested struct {
	state    protoimpl.MessageState `protogen:"open.v1"`
	Envelope *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	// UUIDv7 of the batch (shared by all submissions in the batch).
	OeBatchId string `protobuf:"bytes,2,opt,name=oe_batch_id,json=oeBatchId,proto3" json:"oe_batch_id,omitempty"`
	// UUIDv7 of the assessment the batch belongs to.
	AssessmentId string `protobuf:"bytes,3,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	// UUIDv7 of the test_set_question being graded (one batch = one question
	// shared across N learners' submissions).
	TestSetQuestionId string `protobuf:"bytes,4,opt,name=test_set_question_id,json=testSetQuestionId,proto3" json:"test_set_question_id,omitempty"`
	// UUIDv7 of the underlying chora_creation question.
	QuestionId string `protobuf:"bytes,5,opt,name=question_id,json=questionId,proto3" json:"question_id,omitempty"`
	// Rubric (JSON-encoded — per-criterion id + title + weight; matches
	// creation-questions.yaml `OEPayload.rubric` shape). Snapshot at grading
	// time — later edits in chora_creation do NOT mutate this batch.
	RubricJson string `protobuf:"bytes,6,opt,name=rubric_json,json=rubricJson,proto3" json:"rubric_json,omitempty"`
	// Reference model answer (free text, ≤8000 chars).
	ModelAnswer string `protobuf:"bytes,7,opt,name=model_answer,json=modelAnswer,proto3" json:"model_answer,omitempty"`
	// Question prompt (so the grader can reason about the prompt-answer
	// alignment without an extra fetch).
	Prompt string `protobuf:"bytes,8,opt,name=prompt,proto3" json:"prompt,omitempty"`
	// Points possible per submission (uniform across the batch).
	PointsPossiblePerSubmission int32 `protobuf:"varint,9,opt,name=points_possible_per_submission,json=pointsPossiblePerSubmission,proto3" json:"points_possible_per_submission,omitempty"`
	// Tier of the evaluator model (T1 = gemini-3.1-pro-preview default).
	// Mirrors GradingConfig.llm_evaluator_model_tier (delivery-test-sets.yaml).
	ModelTier string `protobuf:"bytes,10,opt,name=model_tier,json=modelTier,proto3" json:"model_tier,omitempty"`
	// Whether per-question feedback is to be generated (false = score-only,
	// cost-saving mode; per_question_feedback_enabled in GradingConfig).
	PerQuestionFeedbackEnabled bool `protobuf:"varint,11,opt,name=per_question_feedback_enabled,json=perQuestionFeedbackEnabled,proto3" json:"per_question_feedback_enabled,omitempty"`
	// The batched submissions to grade. Up to 50 entries per batch.
	BatchSubmissions []*QuestionGradeBatchEntry `protobuf:"bytes,12,rep,name=batch_submissions,json=batchSubmissions,proto3" json:"batch_submissions,omitempty"`
	// Estimated TenantManaPool debit for this batch (informational — actual
	// debit happens on oe_batch_completed.v1).
	EstimatedManaUnits int32                  `protobuf:"varint,13,opt,name=estimated_mana_units,json=estimatedManaUnits,proto3" json:"estimated_mana_units,omitempty"`
	DispatchedAt       *timestamppb.Timestamp `protobuf:"bytes,14,opt,name=dispatched_at,json=dispatchedAt,proto3" json:"dispatched_at,omitempty"`
	unknownFields      protoimpl.UnknownFields
	sizeCache          protoimpl.SizeCache
}

func (x *GradingOeBatchRequested) Reset() {
	*x = GradingOeBatchRequested{}
	mi := &file_events_delivery_grading_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingOeBatchRequested) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingOeBatchRequested) ProtoMessage() {}

func (x *GradingOeBatchRequested) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingOeBatchRequested.ProtoReflect.Descriptor instead.
func (*GradingOeBatchRequested) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{4}
}

func (x *GradingOeBatchRequested) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingOeBatchRequested) GetOeBatchId() string {
	if x != nil {
		return x.OeBatchId
	}
	return ""
}

func (x *GradingOeBatchRequested) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingOeBatchRequested) GetTestSetQuestionId() string {
	if x != nil {
		return x.TestSetQuestionId
	}
	return ""
}

func (x *GradingOeBatchRequested) GetQuestionId() string {
	if x != nil {
		return x.QuestionId
	}
	return ""
}

func (x *GradingOeBatchRequested) GetRubricJson() string {
	if x != nil {
		return x.RubricJson
	}
	return ""
}

func (x *GradingOeBatchRequested) GetModelAnswer() string {
	if x != nil {
		return x.ModelAnswer
	}
	return ""
}

func (x *GradingOeBatchRequested) GetPrompt() string {
	if x != nil {
		return x.Prompt
	}
	return ""
}

func (x *GradingOeBatchRequested) GetPointsPossiblePerSubmission() int32 {
	if x != nil {
		return x.PointsPossiblePerSubmission
	}
	return 0
}

func (x *GradingOeBatchRequested) GetModelTier() string {
	if x != nil {
		return x.ModelTier
	}
	return ""
}

func (x *GradingOeBatchRequested) GetPerQuestionFeedbackEnabled() bool {
	if x != nil {
		return x.PerQuestionFeedbackEnabled
	}
	return false
}

func (x *GradingOeBatchRequested) GetBatchSubmissions() []*QuestionGradeBatchEntry {
	if x != nil {
		return x.BatchSubmissions
	}
	return nil
}

func (x *GradingOeBatchRequested) GetEstimatedManaUnits() int32 {
	if x != nil {
		return x.EstimatedManaUnits
	}
	return 0
}

func (x *GradingOeBatchRequested) GetDispatchedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.DispatchedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingOeBatchCompleted — chora.delivery.grading.oe_batch_completed.v1
// -----------------------------------------------------------------------------
//
// Fired by `grading_orchestrator` (Python LangGraph) after `oe_grader`
// (ADK Go reasoning engine) returns per-submission scores. Carries the full
// per-submission result list + LLM provenance fields.
//
// Subscribed by:
//   - chora-delivery (writes per_question_grades_jsonb into grading_jobs;
//     transitions job status → SCORED → FINALIZED; emits finalized.v1)
//   - Observability + O+ (per-batch latency + cost; runtime stage of
//     IMDA D2 transparency evidence)
//   - chora-governance (records per-submission LLM grade evidence with
//     full model_id + response_id provenance for IMDA D2 audit)
type GradingOeBatchCompleted struct {
	state             protoimpl.MessageState `protogen:"open.v1"`
	Envelope          *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	OeBatchId         string                 `protobuf:"bytes,2,opt,name=oe_batch_id,json=oeBatchId,proto3" json:"oe_batch_id,omitempty"`
	AssessmentId      string                 `protobuf:"bytes,3,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	TestSetQuestionId string                 `protobuf:"bytes,4,opt,name=test_set_question_id,json=testSetQuestionId,proto3" json:"test_set_question_id,omitempty"`
	QuestionId        string                 `protobuf:"bytes,5,opt,name=question_id,json=questionId,proto3" json:"question_id,omitempty"`
	// Per-submission scoring outcomes — one entry per submission in the
	// original batch_submissions[] of oe_batch_requested.v1.
	Graded []*QuestionGradeBatchResult `protobuf:"bytes,6,rep,name=graded,proto3" json:"graded,omitempty"`
	// Vendor model_id used (same across all entries in the batch — single LLM
	// call per batch). Duplicated from `graded[].grading_model_id` for
	// filter-friendly subscribers.
	GradingModelId string `protobuf:"bytes,7,opt,name=grading_model_id,json=gradingModelId,proto3" json:"grading_model_id,omitempty"`
	// Aggregate TenantManaPool units debited for this batch.
	ManaDebited int32 `protobuf:"varint,8,opt,name=mana_debited,json=manaDebited,proto3" json:"mana_debited,omitempty"`
	// Aggregate token telemetry (input + output, informational).
	TotalInputTokens  int64 `protobuf:"varint,9,opt,name=total_input_tokens,json=totalInputTokens,proto3" json:"total_input_tokens,omitempty"`
	TotalOutputTokens int64 `protobuf:"varint,10,opt,name=total_output_tokens,json=totalOutputTokens,proto3" json:"total_output_tokens,omitempty"`
	// Orchestrator-level outcome — was the batch successful end-to-end?
	// SUCCESS / PARTIAL (some submissions failed individual grading) / FAILED.
	BatchOutcome string `protobuf:"bytes,11,opt,name=batch_outcome,json=batchOutcome,proto3" json:"batch_outcome,omitempty"`
	// On batch_outcome != "SUCCESS" — short category for failure.
	FailureCategory GradingFailureCategory `protobuf:"varint,12,opt,name=failure_category,json=failureCategory,proto3,enum=chora.delivery.v1.GradingFailureCategory" json:"failure_category,omitempty"`
	FailureMessage  string                 `protobuf:"bytes,13,opt,name=failure_message,json=failureMessage,proto3" json:"failure_message,omitempty"`
	CompletedAt     *timestamppb.Timestamp `protobuf:"bytes,14,opt,name=completed_at,json=completedAt,proto3" json:"completed_at,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *GradingOeBatchCompleted) Reset() {
	*x = GradingOeBatchCompleted{}
	mi := &file_events_delivery_grading_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingOeBatchCompleted) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingOeBatchCompleted) ProtoMessage() {}

func (x *GradingOeBatchCompleted) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingOeBatchCompleted.ProtoReflect.Descriptor instead.
func (*GradingOeBatchCompleted) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{5}
}

func (x *GradingOeBatchCompleted) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingOeBatchCompleted) GetOeBatchId() string {
	if x != nil {
		return x.OeBatchId
	}
	return ""
}

func (x *GradingOeBatchCompleted) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingOeBatchCompleted) GetTestSetQuestionId() string {
	if x != nil {
		return x.TestSetQuestionId
	}
	return ""
}

func (x *GradingOeBatchCompleted) GetQuestionId() string {
	if x != nil {
		return x.QuestionId
	}
	return ""
}

func (x *GradingOeBatchCompleted) GetGraded() []*QuestionGradeBatchResult {
	if x != nil {
		return x.Graded
	}
	return nil
}

func (x *GradingOeBatchCompleted) GetGradingModelId() string {
	if x != nil {
		return x.GradingModelId
	}
	return ""
}

func (x *GradingOeBatchCompleted) GetManaDebited() int32 {
	if x != nil {
		return x.ManaDebited
	}
	return 0
}

func (x *GradingOeBatchCompleted) GetTotalInputTokens() int64 {
	if x != nil {
		return x.TotalInputTokens
	}
	return 0
}

func (x *GradingOeBatchCompleted) GetTotalOutputTokens() int64 {
	if x != nil {
		return x.TotalOutputTokens
	}
	return 0
}

func (x *GradingOeBatchCompleted) GetBatchOutcome() string {
	if x != nil {
		return x.BatchOutcome
	}
	return ""
}

func (x *GradingOeBatchCompleted) GetFailureCategory() GradingFailureCategory {
	if x != nil {
		return x.FailureCategory
	}
	return GradingFailureCategory_GRADING_FAILURE_CATEGORY_UNSPECIFIED
}

func (x *GradingOeBatchCompleted) GetFailureMessage() string {
	if x != nil {
		return x.FailureMessage
	}
	return ""
}

func (x *GradingOeBatchCompleted) GetCompletedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CompletedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingFinalized — chora.delivery.grading.finalized.v1
// -----------------------------------------------------------------------------
//
// Fired by chora-delivery after BOTH MCQ + OE grades for a submission are
// persisted to `chora_delivery.grading_jobs.per_question_grades_jsonb` and
// the parent `chora_delivery.submissions.result_jsonb`. Marks the grading
// lifecycle terminal.
//
// Note: terminal-of-grading-job ≠ released-to-learner. The grade is
// invisible to the learner until the parent Assessment is RELEASED (via
// instructor /release-results in delivery-assessments.yaml) OR
// auto-released per GradingConfig.auto_release.
//
// Consumers:
//   - chora-consumption (queue XP credit pending release)
//   - Notifications (instructor "grading complete; ready to release" email)
//   - R+ monitoring (enable the "Release Results" CTA on the dashboard)
//   - delivery-assessments-yaml's submission FSM (GRADING → GRADED transition)
type GradingFinalized struct {
	state        protoimpl.MessageState `protogen:"open.v1"`
	Envelope     *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	GradingJobId string                 `protobuf:"bytes,2,opt,name=grading_job_id,json=gradingJobId,proto3" json:"grading_job_id,omitempty"`
	SubmissionId string                 `protobuf:"bytes,3,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	AssessmentId string                 `protobuf:"bytes,4,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	LearnerGcid  string                 `protobuf:"bytes,5,opt,name=learner_gcid,json=learnerGcid,proto3" json:"learner_gcid,omitempty"`
	// Final aggregate totals (sum of per-question grades).
	TotalPointsEarned       float32 `protobuf:"fixed32,6,opt,name=total_points_earned,json=totalPointsEarned,proto3" json:"total_points_earned,omitempty"`
	TotalPointsPossible     int32   `protobuf:"varint,7,opt,name=total_points_possible,json=totalPointsPossible,proto3" json:"total_points_possible,omitempty"`
	PassingThresholdPercent int32   `protobuf:"varint,8,opt,name=passing_threshold_percent,json=passingThresholdPercent,proto3" json:"passing_threshold_percent,omitempty"`
	Passed                  bool    `protobuf:"varint,9,opt,name=passed,proto3" json:"passed,omitempty"`
	// Indicator: are results released to the learner immediately
	// (auto_release=true) or pending instructor release?
	ReleasedToLearner bool `protobuf:"varint,10,opt,name=released_to_learner,json=releasedToLearner,proto3" json:"released_to_learner,omitempty"`
	// Status of the job at finalization (always FINALIZED on success;
	// included for filter-friendly subscribers).
	Status        GradingJobStatus       `protobuf:"varint,11,opt,name=status,proto3,enum=chora.delivery.v1.GradingJobStatus" json:"status,omitempty"`
	FinalizedAt   *timestamppb.Timestamp `protobuf:"bytes,12,opt,name=finalized_at,json=finalizedAt,proto3" json:"finalized_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GradingFinalized) Reset() {
	*x = GradingFinalized{}
	mi := &file_events_delivery_grading_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingFinalized) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingFinalized) ProtoMessage() {}

func (x *GradingFinalized) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingFinalized.ProtoReflect.Descriptor instead.
func (*GradingFinalized) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{6}
}

func (x *GradingFinalized) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingFinalized) GetGradingJobId() string {
	if x != nil {
		return x.GradingJobId
	}
	return ""
}

func (x *GradingFinalized) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingFinalized) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingFinalized) GetLearnerGcid() string {
	if x != nil {
		return x.LearnerGcid
	}
	return ""
}

func (x *GradingFinalized) GetTotalPointsEarned() float32 {
	if x != nil {
		return x.TotalPointsEarned
	}
	return 0
}

func (x *GradingFinalized) GetTotalPointsPossible() int32 {
	if x != nil {
		return x.TotalPointsPossible
	}
	return 0
}

func (x *GradingFinalized) GetPassingThresholdPercent() int32 {
	if x != nil {
		return x.PassingThresholdPercent
	}
	return 0
}

func (x *GradingFinalized) GetPassed() bool {
	if x != nil {
		return x.Passed
	}
	return false
}

func (x *GradingFinalized) GetReleasedToLearner() bool {
	if x != nil {
		return x.ReleasedToLearner
	}
	return false
}

func (x *GradingFinalized) GetStatus() GradingJobStatus {
	if x != nil {
		return x.Status
	}
	return GradingJobStatus_GRADING_JOB_STATUS_UNSPECIFIED
}

func (x *GradingFinalized) GetFinalizedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.FinalizedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingFailed — chora.delivery.grading.failed.v1
// -----------------------------------------------------------------------------
//
// Fired by chora-delivery when a grading job reaches terminal FAILED state.
// Today the load-bearing producer is the OE-batch completion-event subscriber
// (services/chora-delivery/internal/adapter/subscribers/grading_inbox.go):
// when the orchestrator returns batch_outcome != "ok" the subscriber records
// the failure here so downstream subscribers can react.
//
// Distinct from `oe_batch_completed.v1` carrying `failure_category` — that
// is the *batch-level* dispatch outcome (the orchestrator → executor hop).
// This event is the *grading-job-level* terminal marker: it means the entire
// grading job for the submission cannot make forward progress without manual
// intervention (instructor regrade or admin remediation).
//
// FSM transition: any state → FAILED (GRADING_JOB_STATUS_FAILED).
//
// Consumers:
//   - O+ governance (IMDA D1 accountability — every grading failure
//     auditable with category + provenance + traceparent)
//   - R+ monitoring (display "Grading failed; re-dispatch?" CTA + per-tenant
//     failure-rate gauge)
//   - Observability (alerting on failure-rate spikes; ties to D3
//     safety_and_robustness)
//   - chora-consumption (clear XP pending for this submission since the
//     grade is not coming through)
type GradingFailed struct {
	state    protoimpl.MessageState `protogen:"open.v1"`
	Envelope *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	// UUIDv7 of the grading job (1:1 with submission attempt).
	GradingJobId string `protobuf:"bytes,2,opt,name=grading_job_id,json=gradingJobId,proto3" json:"grading_job_id,omitempty"`
	// UUIDv7 of the parent submission (chora_delivery.submissions). Required.
	SubmissionId string `protobuf:"bytes,3,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	// UUIDv7 of the assessment (chora_delivery.assessments).
	AssessmentId string `protobuf:"bytes,4,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	// GCID of the learner whose submission was being graded.
	LearnerGcid string `protobuf:"bytes,5,opt,name=learner_gcid,json=learnerGcid,proto3" json:"learner_gcid,omitempty"`
	// Categorised failure reason (mirrors `oe_batch_completed.v1.failure_category`
	// when the trigger was an OE batch; populated to INTERNAL_ERROR for
	// grading-job-level failures not tied to a specific batch).
	FailureCategory GradingFailureCategory `protobuf:"varint,6,opt,name=failure_category,json=failureCategory,proto3,enum=chora.delivery.v1.GradingFailureCategory" json:"failure_category,omitempty"`
	// Human-readable failure summary (≤4096 chars). Forwarded to the
	// governance audit + the R+ admin UI.
	FailureMessage string `protobuf:"bytes,7,opt,name=failure_message,json=failureMessage,proto3" json:"failure_message,omitempty"`
	// Status of the job at failure time (always GRADING_JOB_STATUS_FAILED;
	// included for filter-friendly subscribers).
	Status GradingJobStatus `protobuf:"varint,8,opt,name=status,proto3,enum=chora.delivery.v1.GradingJobStatus" json:"status,omitempty"`
	// Number of grading attempts before the job was marked terminal-failed
	// (informational; 1-based counter — the orchestrator retries upstream
	// are still captured via oe_batch_completed.v1 sequencing).
	AttemptCount int32 `protobuf:"varint,9,opt,name=attempt_count,json=attemptCount,proto3" json:"attempt_count,omitempty"`
	// Reference to the OE batch (if any) that triggered the terminal failure;
	// empty when the failure was grading-job-level (e.g., MCQ snapshot
	// missing + no OE batch was dispatched).
	OeBatchId string `protobuf:"bytes,10,opt,name=oe_batch_id,json=oeBatchId,proto3" json:"oe_batch_id,omitempty"`
	// When the terminal failure was recorded at the producer's domain database.
	FailedAt      *timestamppb.Timestamp `protobuf:"bytes,11,opt,name=failed_at,json=failedAt,proto3" json:"failed_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GradingFailed) Reset() {
	*x = GradingFailed{}
	mi := &file_events_delivery_grading_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingFailed) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingFailed) ProtoMessage() {}

func (x *GradingFailed) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingFailed.ProtoReflect.Descriptor instead.
func (*GradingFailed) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{7}
}

func (x *GradingFailed) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingFailed) GetGradingJobId() string {
	if x != nil {
		return x.GradingJobId
	}
	return ""
}

func (x *GradingFailed) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingFailed) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingFailed) GetLearnerGcid() string {
	if x != nil {
		return x.LearnerGcid
	}
	return ""
}

func (x *GradingFailed) GetFailureCategory() GradingFailureCategory {
	if x != nil {
		return x.FailureCategory
	}
	return GradingFailureCategory_GRADING_FAILURE_CATEGORY_UNSPECIFIED
}

func (x *GradingFailed) GetFailureMessage() string {
	if x != nil {
		return x.FailureMessage
	}
	return ""
}

func (x *GradingFailed) GetStatus() GradingJobStatus {
	if x != nil {
		return x.Status
	}
	return GradingJobStatus_GRADING_JOB_STATUS_UNSPECIFIED
}

func (x *GradingFailed) GetAttemptCount() int32 {
	if x != nil {
		return x.AttemptCount
	}
	return 0
}

func (x *GradingFailed) GetOeBatchId() string {
	if x != nil {
		return x.OeBatchId
	}
	return ""
}

func (x *GradingFailed) GetFailedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.FailedAt
	}
	return nil
}

// SubmissionQuestionContext — one question's full grading context inside a
// per-submission grading request. MCQ entries carry their deterministic
// outcome (so the summarizer can reference total score); OE entries carry the
// learner answer + rubric + model_answer the evaluator grades against.
type SubmissionQuestionContext struct {
	state             protoimpl.MessageState `protogen:"open.v1"`
	TestSetQuestionId string                 `protobuf:"bytes,1,opt,name=test_set_question_id,json=testSetQuestionId,proto3" json:"test_set_question_id,omitempty"`
	QuestionId        string                 `protobuf:"bytes,2,opt,name=question_id,json=questionId,proto3" json:"question_id,omitempty"`
	QuestionType      string                 `protobuf:"bytes,3,opt,name=question_type,json=questionType,proto3" json:"question_type,omitempty"` // "mcq" | "oe"
	DisplayOrder      int32                  `protobuf:"varint,4,opt,name=display_order,json=displayOrder,proto3" json:"display_order,omitempty"`
	PointsPossible    int32                  `protobuf:"varint,5,opt,name=points_possible,json=pointsPossible,proto3" json:"points_possible,omitempty"`
	Prompt            string                 `protobuf:"bytes,6,opt,name=prompt,proto3" json:"prompt,omitempty"`
	Subject           string                 `protobuf:"bytes,7,opt,name=subject,proto3" json:"subject,omitempty"` // for summarizer theming (e.g., "science")
	Topic             string                 `protobuf:"bytes,8,opt,name=topic,proto3" json:"topic,omitempty"`     // e.g., "photosynthesis" (free text / topic node label)
	// OE-only — empty for MCQ entries.
	OeResponseText string `protobuf:"bytes,9,opt,name=oe_response_text,json=oeResponseText,proto3" json:"oe_response_text,omitempty"` // learner's free-text answer
	RubricJson     string `protobuf:"bytes,10,opt,name=rubric_json,json=rubricJson,proto3" json:"rubric_json,omitempty"`              // mandatory weighted rubric (criteria + weights), snapshot
	ModelAnswer    string `protobuf:"bytes,11,opt,name=model_answer,json=modelAnswer,proto3" json:"model_answer,omitempty"`           // reference answer (snapshot at grading time)
	// MCQ-only — populated for MCQ entries so assess_summary sees full results.
	McqCorrect      bool    `protobuf:"varint,12,opt,name=mcq_correct,json=mcqCorrect,proto3" json:"mcq_correct,omitempty"`
	McqPointsEarned float32 `protobuf:"fixed32,13,opt,name=mcq_points_earned,json=mcqPointsEarned,proto3" json:"mcq_points_earned,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *SubmissionQuestionContext) Reset() {
	*x = SubmissionQuestionContext{}
	mi := &file_events_delivery_grading_proto_msgTypes[8]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SubmissionQuestionContext) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SubmissionQuestionContext) ProtoMessage() {}

func (x *SubmissionQuestionContext) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[8]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SubmissionQuestionContext.ProtoReflect.Descriptor instead.
func (*SubmissionQuestionContext) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{8}
}

func (x *SubmissionQuestionContext) GetTestSetQuestionId() string {
	if x != nil {
		return x.TestSetQuestionId
	}
	return ""
}

func (x *SubmissionQuestionContext) GetQuestionId() string {
	if x != nil {
		return x.QuestionId
	}
	return ""
}

func (x *SubmissionQuestionContext) GetQuestionType() string {
	if x != nil {
		return x.QuestionType
	}
	return ""
}

func (x *SubmissionQuestionContext) GetDisplayOrder() int32 {
	if x != nil {
		return x.DisplayOrder
	}
	return 0
}

func (x *SubmissionQuestionContext) GetPointsPossible() int32 {
	if x != nil {
		return x.PointsPossible
	}
	return 0
}

func (x *SubmissionQuestionContext) GetPrompt() string {
	if x != nil {
		return x.Prompt
	}
	return ""
}

func (x *SubmissionQuestionContext) GetSubject() string {
	if x != nil {
		return x.Subject
	}
	return ""
}

func (x *SubmissionQuestionContext) GetTopic() string {
	if x != nil {
		return x.Topic
	}
	return ""
}

func (x *SubmissionQuestionContext) GetOeResponseText() string {
	if x != nil {
		return x.OeResponseText
	}
	return ""
}

func (x *SubmissionQuestionContext) GetRubricJson() string {
	if x != nil {
		return x.RubricJson
	}
	return ""
}

func (x *SubmissionQuestionContext) GetModelAnswer() string {
	if x != nil {
		return x.ModelAnswer
	}
	return ""
}

func (x *SubmissionQuestionContext) GetMcqCorrect() bool {
	if x != nil {
		return x.McqCorrect
	}
	return false
}

func (x *SubmissionQuestionContext) GetMcqPointsEarned() float32 {
	if x != nil {
		return x.McqPointsEarned
	}
	return 0
}

// OEQuestionGradeResult — per-OE-question scoring output from the
// evaluator→moderator loop. Carried inside submission_completed.v1's graded[].
type OEQuestionGradeResult struct {
	state             protoimpl.MessageState `protogen:"open.v1"`
	TestSetQuestionId string                 `protobuf:"bytes,1,opt,name=test_set_question_id,json=testSetQuestionId,proto3" json:"test_set_question_id,omitempty"`
	QuestionId        string                 `protobuf:"bytes,2,opt,name=question_id,json=questionId,proto3" json:"question_id,omitempty"`
	// Weighted composite (0..points_possible). Float — rubric partial credit.
	PointsEarned   float32 `protobuf:"fixed32,3,opt,name=points_earned,json=pointsEarned,proto3" json:"points_earned,omitempty"`
	PointsPossible int32   `protobuf:"varint,4,opt,name=points_possible,json=pointsPossible,proto3" json:"points_possible,omitempty"`
	// Per-rubric-criterion sub-scores (JSON: criterion_id/title/score/max/feedback).
	CriterionScoresJson string `protobuf:"bytes,5,opt,name=criterion_scores_json,json=criterionScoresJson,proto3" json:"criterion_scores_json,omitempty"`
	// Per-question comment — ALWAYS present (right answer or wrong), ADR-172 §D4.
	// Markdown; surfaced to the learner only after release.
	Comment string `protobuf:"bytes,6,opt,name=comment,proto3" json:"comment,omitempty"`
	// LLM provenance (IMDA D2). Pinned per request.
	GradingModelId    string `protobuf:"bytes,7,opt,name=grading_model_id,json=gradingModelId,proto3" json:"grading_model_id,omitempty"`
	GradingResponseId string `protobuf:"bytes,8,opt,name=grading_response_id,json=gradingResponseId,proto3" json:"grading_response_id,omitempty"`
	// True when the evaluator→moderator loop exhausted its retries without the
	// moderator accepting — the R+ queue flags this for priority human review.
	QualityFlagged bool `protobuf:"varint,9,opt,name=quality_flagged,json=qualityFlagged,proto3" json:"quality_flagged,omitempty"`
	// Evaluator iterations consumed (1-based; ≤ max_iterations+1).
	AttemptCount  int32 `protobuf:"varint,10,opt,name=attempt_count,json=attemptCount,proto3" json:"attempt_count,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *OEQuestionGradeResult) Reset() {
	*x = OEQuestionGradeResult{}
	mi := &file_events_delivery_grading_proto_msgTypes[9]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *OEQuestionGradeResult) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*OEQuestionGradeResult) ProtoMessage() {}

func (x *OEQuestionGradeResult) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[9]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use OEQuestionGradeResult.ProtoReflect.Descriptor instead.
func (*OEQuestionGradeResult) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{9}
}

func (x *OEQuestionGradeResult) GetTestSetQuestionId() string {
	if x != nil {
		return x.TestSetQuestionId
	}
	return ""
}

func (x *OEQuestionGradeResult) GetQuestionId() string {
	if x != nil {
		return x.QuestionId
	}
	return ""
}

func (x *OEQuestionGradeResult) GetPointsEarned() float32 {
	if x != nil {
		return x.PointsEarned
	}
	return 0
}

func (x *OEQuestionGradeResult) GetPointsPossible() int32 {
	if x != nil {
		return x.PointsPossible
	}
	return 0
}

func (x *OEQuestionGradeResult) GetCriterionScoresJson() string {
	if x != nil {
		return x.CriterionScoresJson
	}
	return ""
}

func (x *OEQuestionGradeResult) GetComment() string {
	if x != nil {
		return x.Comment
	}
	return ""
}

func (x *OEQuestionGradeResult) GetGradingModelId() string {
	if x != nil {
		return x.GradingModelId
	}
	return ""
}

func (x *OEQuestionGradeResult) GetGradingResponseId() string {
	if x != nil {
		return x.GradingResponseId
	}
	return ""
}

func (x *OEQuestionGradeResult) GetQualityFlagged() bool {
	if x != nil {
		return x.QualityFlagged
	}
	return false
}

func (x *OEQuestionGradeResult) GetAttemptCount() int32 {
	if x != nil {
		return x.AttemptCount
	}
	return 0
}

// -----------------------------------------------------------------------------
// GradingSubmissionRequested — chora.delivery.grading.submission_requested.v1
// -----------------------------------------------------------------------------
//
// Fired by chora-delivery on /submit when the submission has ≥1 OE answer.
// Carries the WHOLE submission so the crew can grade each OE answer AND write a
// holistic overall comment that references MCQ outcomes (ADR-172 §D1, §D5).
// Subscribed by the oe_grading_crew (Python LangGraph, GKE).
type GradingSubmissionRequested struct {
	state                      protoimpl.MessageState       `protogen:"open.v1"`
	Envelope                   *v1.EventEnvelope            `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	GradingJobId               string                       `protobuf:"bytes,2,opt,name=grading_job_id,json=gradingJobId,proto3" json:"grading_job_id,omitempty"`
	SubmissionId               string                       `protobuf:"bytes,3,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	AssessmentId               string                       `protobuf:"bytes,4,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	TestSetId                  string                       `protobuf:"bytes,5,opt,name=test_set_id,json=testSetId,proto3" json:"test_set_id,omitempty"`
	LearnerGcid                string                       `protobuf:"bytes,6,opt,name=learner_gcid,json=learnerGcid,proto3" json:"learner_gcid,omitempty"`
	PassingThresholdPercent    int32                        `protobuf:"varint,7,opt,name=passing_threshold_percent,json=passingThresholdPercent,proto3" json:"passing_threshold_percent,omitempty"`
	ModelTier                  string                       `protobuf:"bytes,8,opt,name=model_tier,json=modelTier,proto3" json:"model_tier,omitempty"` // T1 default (LLM-as-judge)
	PerQuestionFeedbackEnabled bool                         `protobuf:"varint,9,opt,name=per_question_feedback_enabled,json=perQuestionFeedbackEnabled,proto3" json:"per_question_feedback_enabled,omitempty"`
	Subject                    string                       `protobuf:"bytes,10,opt,name=subject,proto3" json:"subject,omitempty"`     // assessment-level subject for the summary
	Questions                  []*SubmissionQuestionContext `protobuf:"bytes,11,rep,name=questions,proto3" json:"questions,omitempty"` // MCQ + OE, full context
	TotalPointsPossible        int32                        `protobuf:"varint,12,opt,name=total_points_possible,json=totalPointsPossible,proto3" json:"total_points_possible,omitempty"`
	McqPointsEarned            float32                      `protobuf:"fixed32,13,opt,name=mcq_points_earned,json=mcqPointsEarned,proto3" json:"mcq_points_earned,omitempty"`         // deterministic MCQ total (for summary)
	EstimatedManaUnits         int32                        `protobuf:"varint,14,opt,name=estimated_mana_units,json=estimatedManaUnits,proto3" json:"estimated_mana_units,omitempty"` // informational; debit on completion
	RequestedAt                *timestamppb.Timestamp       `protobuf:"bytes,15,opt,name=requested_at,json=requestedAt,proto3" json:"requested_at,omitempty"`
	unknownFields              protoimpl.UnknownFields
	sizeCache                  protoimpl.SizeCache
}

func (x *GradingSubmissionRequested) Reset() {
	*x = GradingSubmissionRequested{}
	mi := &file_events_delivery_grading_proto_msgTypes[10]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingSubmissionRequested) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingSubmissionRequested) ProtoMessage() {}

func (x *GradingSubmissionRequested) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[10]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingSubmissionRequested.ProtoReflect.Descriptor instead.
func (*GradingSubmissionRequested) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{10}
}

func (x *GradingSubmissionRequested) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingSubmissionRequested) GetGradingJobId() string {
	if x != nil {
		return x.GradingJobId
	}
	return ""
}

func (x *GradingSubmissionRequested) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingSubmissionRequested) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingSubmissionRequested) GetTestSetId() string {
	if x != nil {
		return x.TestSetId
	}
	return ""
}

func (x *GradingSubmissionRequested) GetLearnerGcid() string {
	if x != nil {
		return x.LearnerGcid
	}
	return ""
}

func (x *GradingSubmissionRequested) GetPassingThresholdPercent() int32 {
	if x != nil {
		return x.PassingThresholdPercent
	}
	return 0
}

func (x *GradingSubmissionRequested) GetModelTier() string {
	if x != nil {
		return x.ModelTier
	}
	return ""
}

func (x *GradingSubmissionRequested) GetPerQuestionFeedbackEnabled() bool {
	if x != nil {
		return x.PerQuestionFeedbackEnabled
	}
	return false
}

func (x *GradingSubmissionRequested) GetSubject() string {
	if x != nil {
		return x.Subject
	}
	return ""
}

func (x *GradingSubmissionRequested) GetQuestions() []*SubmissionQuestionContext {
	if x != nil {
		return x.Questions
	}
	return nil
}

func (x *GradingSubmissionRequested) GetTotalPointsPossible() int32 {
	if x != nil {
		return x.TotalPointsPossible
	}
	return 0
}

func (x *GradingSubmissionRequested) GetMcqPointsEarned() float32 {
	if x != nil {
		return x.McqPointsEarned
	}
	return 0
}

func (x *GradingSubmissionRequested) GetEstimatedManaUnits() int32 {
	if x != nil {
		return x.EstimatedManaUnits
	}
	return 0
}

func (x *GradingSubmissionRequested) GetRequestedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.RequestedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingSubmissionCompleted — chora.delivery.grading.submission_completed.v1
// -----------------------------------------------------------------------------
//
// Fired by oe_grading_crew after every OE question is graded (loop) and the
// assess_summary mode has produced the overall comment. Subscribed by
// chora-delivery (applies grades; lands GRADED_PENDING_RELEASE + PENDING_REVIEW)
// + Observability/O+ + chora-governance (IMDA D2 evidence).
type GradingSubmissionCompleted struct {
	state        protoimpl.MessageState   `protogen:"open.v1"`
	Envelope     *v1.EventEnvelope        `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	GradingJobId string                   `protobuf:"bytes,2,opt,name=grading_job_id,json=gradingJobId,proto3" json:"grading_job_id,omitempty"`
	SubmissionId string                   `protobuf:"bytes,3,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	AssessmentId string                   `protobuf:"bytes,4,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	LearnerGcid  string                   `protobuf:"bytes,5,opt,name=learner_gcid,json=learnerGcid,proto3" json:"learner_gcid,omitempty"`
	Graded       []*OEQuestionGradeResult `protobuf:"bytes,6,rep,name=graded,proto3" json:"graded,omitempty"`
	// Overall whole-assessment narrative comment (ADR-172 §D5) — AI-drafted,
	// instructor-editable, learner-visible. Spans MCQ + OE.
	OverallComment           string `protobuf:"bytes,7,opt,name=overall_comment,json=overallComment,proto3" json:"overall_comment,omitempty"`
	OverallCommentModelId    string `protobuf:"bytes,8,opt,name=overall_comment_model_id,json=overallCommentModelId,proto3" json:"overall_comment_model_id,omitempty"`
	OverallCommentResponseId string `protobuf:"bytes,9,opt,name=overall_comment_response_id,json=overallCommentResponseId,proto3" json:"overall_comment_response_id,omitempty"`
	// SUCCESS | PARTIAL | FAILED (PARTIAL = some OE questions failed individually).
	Outcome           string                 `protobuf:"bytes,10,opt,name=outcome,proto3" json:"outcome,omitempty"`
	FailureCategory   GradingFailureCategory `protobuf:"varint,11,opt,name=failure_category,json=failureCategory,proto3,enum=chora.delivery.v1.GradingFailureCategory" json:"failure_category,omitempty"`
	FailureMessage    string                 `protobuf:"bytes,12,opt,name=failure_message,json=failureMessage,proto3" json:"failure_message,omitempty"`
	ManaDebited       int32                  `protobuf:"varint,13,opt,name=mana_debited,json=manaDebited,proto3" json:"mana_debited,omitempty"`
	TotalInputTokens  int64                  `protobuf:"varint,14,opt,name=total_input_tokens,json=totalInputTokens,proto3" json:"total_input_tokens,omitempty"`
	TotalOutputTokens int64                  `protobuf:"varint,15,opt,name=total_output_tokens,json=totalOutputTokens,proto3" json:"total_output_tokens,omitempty"`
	CompletedAt       *timestamppb.Timestamp `protobuf:"bytes,16,opt,name=completed_at,json=completedAt,proto3" json:"completed_at,omitempty"`
	unknownFields     protoimpl.UnknownFields
	sizeCache         protoimpl.SizeCache
}

func (x *GradingSubmissionCompleted) Reset() {
	*x = GradingSubmissionCompleted{}
	mi := &file_events_delivery_grading_proto_msgTypes[11]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingSubmissionCompleted) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingSubmissionCompleted) ProtoMessage() {}

func (x *GradingSubmissionCompleted) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[11]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingSubmissionCompleted.ProtoReflect.Descriptor instead.
func (*GradingSubmissionCompleted) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{11}
}

func (x *GradingSubmissionCompleted) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingSubmissionCompleted) GetGradingJobId() string {
	if x != nil {
		return x.GradingJobId
	}
	return ""
}

func (x *GradingSubmissionCompleted) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingSubmissionCompleted) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingSubmissionCompleted) GetLearnerGcid() string {
	if x != nil {
		return x.LearnerGcid
	}
	return ""
}

func (x *GradingSubmissionCompleted) GetGraded() []*OEQuestionGradeResult {
	if x != nil {
		return x.Graded
	}
	return nil
}

func (x *GradingSubmissionCompleted) GetOverallComment() string {
	if x != nil {
		return x.OverallComment
	}
	return ""
}

func (x *GradingSubmissionCompleted) GetOverallCommentModelId() string {
	if x != nil {
		return x.OverallCommentModelId
	}
	return ""
}

func (x *GradingSubmissionCompleted) GetOverallCommentResponseId() string {
	if x != nil {
		return x.OverallCommentResponseId
	}
	return ""
}

func (x *GradingSubmissionCompleted) GetOutcome() string {
	if x != nil {
		return x.Outcome
	}
	return ""
}

func (x *GradingSubmissionCompleted) GetFailureCategory() GradingFailureCategory {
	if x != nil {
		return x.FailureCategory
	}
	return GradingFailureCategory_GRADING_FAILURE_CATEGORY_UNSPECIFIED
}

func (x *GradingSubmissionCompleted) GetFailureMessage() string {
	if x != nil {
		return x.FailureMessage
	}
	return ""
}

func (x *GradingSubmissionCompleted) GetManaDebited() int32 {
	if x != nil {
		return x.ManaDebited
	}
	return 0
}

func (x *GradingSubmissionCompleted) GetTotalInputTokens() int64 {
	if x != nil {
		return x.TotalInputTokens
	}
	return 0
}

func (x *GradingSubmissionCompleted) GetTotalOutputTokens() int64 {
	if x != nil {
		return x.TotalOutputTokens
	}
	return 0
}

func (x *GradingSubmissionCompleted) GetCompletedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CompletedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingScoreOverridden — chora.delivery.grading.score_overridden.v1
// -----------------------------------------------------------------------------
//
// Fired by chora-delivery when an instructor edits an AI grading artifact in
// the R+ grading queue. Append-only audit (ADR-172 §D7 / ADR-170 discipline);
// flips the artifact's provenance AI→HUMAN. Consumers: chora-governance
// (IMDA D1 accountability + D4 human-oversight evidence), O+ audit trail.
type GradingScoreOverridden struct {
	state             protoimpl.MessageState `protogen:"open.v1"`
	Envelope          *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	SubmissionId      string                 `protobuf:"bytes,2,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	AssessmentId      string                 `protobuf:"bytes,3,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	AnswerId          string                 `protobuf:"bytes,4,opt,name=answer_id,json=answerId,proto3" json:"answer_id,omitempty"` // submission_answer row; empty for OVERALL_COMMENT
	TestSetQuestionId string                 `protobuf:"bytes,5,opt,name=test_set_question_id,json=testSetQuestionId,proto3" json:"test_set_question_id,omitempty"`
	QuestionId        string                 `protobuf:"bytes,6,opt,name=question_id,json=questionId,proto3" json:"question_id,omitempty"`
	Field             GradeOverrideField     `protobuf:"varint,7,opt,name=field,proto3,enum=chora.delivery.v1.GradeOverrideField" json:"field,omitempty"`
	OldValue          string                 `protobuf:"bytes,8,opt,name=old_value,json=oldValue,proto3" json:"old_value,omitempty"` // stringified prior value (score → number string)
	NewValue          string                 `protobuf:"bytes,9,opt,name=new_value,json=newValue,proto3" json:"new_value,omitempty"`
	ActorGcid         string                 `protobuf:"bytes,10,opt,name=actor_gcid,json=actorGcid,proto3" json:"actor_gcid,omitempty"` // the human instructor/grader
	Reason            string                 `protobuf:"bytes,11,opt,name=reason,proto3" json:"reason,omitempty"`                        // optional override rationale
	OverriddenAt      *timestamppb.Timestamp `protobuf:"bytes,12,opt,name=overridden_at,json=overriddenAt,proto3" json:"overridden_at,omitempty"`
	unknownFields     protoimpl.UnknownFields
	sizeCache         protoimpl.SizeCache
}

func (x *GradingScoreOverridden) Reset() {
	*x = GradingScoreOverridden{}
	mi := &file_events_delivery_grading_proto_msgTypes[12]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingScoreOverridden) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingScoreOverridden) ProtoMessage() {}

func (x *GradingScoreOverridden) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[12]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingScoreOverridden.ProtoReflect.Descriptor instead.
func (*GradingScoreOverridden) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{12}
}

func (x *GradingScoreOverridden) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingScoreOverridden) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingScoreOverridden) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingScoreOverridden) GetAnswerId() string {
	if x != nil {
		return x.AnswerId
	}
	return ""
}

func (x *GradingScoreOverridden) GetTestSetQuestionId() string {
	if x != nil {
		return x.TestSetQuestionId
	}
	return ""
}

func (x *GradingScoreOverridden) GetQuestionId() string {
	if x != nil {
		return x.QuestionId
	}
	return ""
}

func (x *GradingScoreOverridden) GetField() GradeOverrideField {
	if x != nil {
		return x.Field
	}
	return GradeOverrideField_GRADE_OVERRIDE_FIELD_UNSPECIFIED
}

func (x *GradingScoreOverridden) GetOldValue() string {
	if x != nil {
		return x.OldValue
	}
	return ""
}

func (x *GradingScoreOverridden) GetNewValue() string {
	if x != nil {
		return x.NewValue
	}
	return ""
}

func (x *GradingScoreOverridden) GetActorGcid() string {
	if x != nil {
		return x.ActorGcid
	}
	return ""
}

func (x *GradingScoreOverridden) GetReason() string {
	if x != nil {
		return x.Reason
	}
	return ""
}

func (x *GradingScoreOverridden) GetOverriddenAt() *timestamppb.Timestamp {
	if x != nil {
		return x.OverriddenAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingSubmissionApproved — chora.delivery.grading.submission_approved.v1
// -----------------------------------------------------------------------------
//
// Fired when an instructor approves a single submission's grading (per-candidate
// one-click "approve as-is", or after edits). review_status → APPROVED.
type GradingSubmissionApproved struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Envelope      *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	SubmissionId  string                 `protobuf:"bytes,2,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	AssessmentId  string                 `protobuf:"bytes,3,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	ApproverGcid  string                 `protobuf:"bytes,4,opt,name=approver_gcid,json=approverGcid,proto3" json:"approver_gcid,omitempty"`
	HadEdits      bool                   `protobuf:"varint,5,opt,name=had_edits,json=hadEdits,proto3" json:"had_edits,omitempty"` // true if any score/comment was overridden first
	ApprovedAt    *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=approved_at,json=approvedAt,proto3" json:"approved_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GradingSubmissionApproved) Reset() {
	*x = GradingSubmissionApproved{}
	mi := &file_events_delivery_grading_proto_msgTypes[13]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingSubmissionApproved) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingSubmissionApproved) ProtoMessage() {}

func (x *GradingSubmissionApproved) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[13]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingSubmissionApproved.ProtoReflect.Descriptor instead.
func (*GradingSubmissionApproved) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{13}
}

func (x *GradingSubmissionApproved) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingSubmissionApproved) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingSubmissionApproved) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingSubmissionApproved) GetApproverGcid() string {
	if x != nil {
		return x.ApproverGcid
	}
	return ""
}

func (x *GradingSubmissionApproved) GetHadEdits() bool {
	if x != nil {
		return x.HadEdits
	}
	return false
}

func (x *GradingSubmissionApproved) GetApprovedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.ApprovedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingAssessmentApproved — chora.delivery.grading.assessment_approved.v1
// -----------------------------------------------------------------------------
//
// Fired on assessment-wide bulk approve (accept all submissions' AI grading).
type GradingAssessmentApproved struct {
	state                   protoimpl.MessageState `protogen:"open.v1"`
	Envelope                *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	AssessmentId            string                 `protobuf:"bytes,2,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	ApproverGcid            string                 `protobuf:"bytes,3,opt,name=approver_gcid,json=approverGcid,proto3" json:"approver_gcid,omitempty"`
	ApprovedSubmissionCount int32                  `protobuf:"varint,4,opt,name=approved_submission_count,json=approvedSubmissionCount,proto3" json:"approved_submission_count,omitempty"`
	Released                bool                   `protobuf:"varint,5,opt,name=released,proto3" json:"released,omitempty"` // true when this was approve-and-release
	ApprovedAt              *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=approved_at,json=approvedAt,proto3" json:"approved_at,omitempty"`
	unknownFields           protoimpl.UnknownFields
	sizeCache               protoimpl.SizeCache
}

func (x *GradingAssessmentApproved) Reset() {
	*x = GradingAssessmentApproved{}
	mi := &file_events_delivery_grading_proto_msgTypes[14]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingAssessmentApproved) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingAssessmentApproved) ProtoMessage() {}

func (x *GradingAssessmentApproved) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[14]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingAssessmentApproved.ProtoReflect.Descriptor instead.
func (*GradingAssessmentApproved) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{14}
}

func (x *GradingAssessmentApproved) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingAssessmentApproved) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingAssessmentApproved) GetApproverGcid() string {
	if x != nil {
		return x.ApproverGcid
	}
	return ""
}

func (x *GradingAssessmentApproved) GetApprovedSubmissionCount() int32 {
	if x != nil {
		return x.ApprovedSubmissionCount
	}
	return 0
}

func (x *GradingAssessmentApproved) GetReleased() bool {
	if x != nil {
		return x.Released
	}
	return false
}

func (x *GradingAssessmentApproved) GetApprovedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.ApprovedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingModelAnswerAmended — chora.delivery.grading.model_answer_amended.v1
// -----------------------------------------------------------------------------
//
// Fired when an instructor corrects a question's canonical model_answer during
// grading. Consumed by chora-creation, which APPENDS a new QuestionRevision to
// the question's oe_payload.model_answer (source=grading_amendment; never a
// silent overwrite; idempotent on event_id). ADR-172 §D8 — the sole sanctioned
// creation↔delivery coupling, event-mediated.
type GradingModelAnswerAmended struct {
	state             protoimpl.MessageState `protogen:"open.v1"`
	Envelope          *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	SubmissionId      string                 `protobuf:"bytes,2,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"` // provenance: where the amendment originated
	AssessmentId      string                 `protobuf:"bytes,3,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	TestSetQuestionId string                 `protobuf:"bytes,4,opt,name=test_set_question_id,json=testSetQuestionId,proto3" json:"test_set_question_id,omitempty"`
	QuestionId        string                 `protobuf:"bytes,5,opt,name=question_id,json=questionId,proto3" json:"question_id,omitempty"`
	AtomId            string                 `protobuf:"bytes,6,opt,name=atom_id,json=atomId,proto3" json:"atom_id,omitempty"`
	NewModelAnswer    string                 `protobuf:"bytes,7,opt,name=new_model_answer,json=newModelAnswer,proto3" json:"new_model_answer,omitempty"`
	ActorGcid         string                 `protobuf:"bytes,8,opt,name=actor_gcid,json=actorGcid,proto3" json:"actor_gcid,omitempty"` // the human instructor
	AmendedAt         *timestamppb.Timestamp `protobuf:"bytes,9,opt,name=amended_at,json=amendedAt,proto3" json:"amended_at,omitempty"`
	unknownFields     protoimpl.UnknownFields
	sizeCache         protoimpl.SizeCache
}

func (x *GradingModelAnswerAmended) Reset() {
	*x = GradingModelAnswerAmended{}
	mi := &file_events_delivery_grading_proto_msgTypes[15]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingModelAnswerAmended) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingModelAnswerAmended) ProtoMessage() {}

func (x *GradingModelAnswerAmended) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[15]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingModelAnswerAmended.ProtoReflect.Descriptor instead.
func (*GradingModelAnswerAmended) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{15}
}

func (x *GradingModelAnswerAmended) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingModelAnswerAmended) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingModelAnswerAmended) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingModelAnswerAmended) GetTestSetQuestionId() string {
	if x != nil {
		return x.TestSetQuestionId
	}
	return ""
}

func (x *GradingModelAnswerAmended) GetQuestionId() string {
	if x != nil {
		return x.QuestionId
	}
	return ""
}

func (x *GradingModelAnswerAmended) GetAtomId() string {
	if x != nil {
		return x.AtomId
	}
	return ""
}

func (x *GradingModelAnswerAmended) GetNewModelAnswer() string {
	if x != nil {
		return x.NewModelAnswer
	}
	return ""
}

func (x *GradingModelAnswerAmended) GetActorGcid() string {
	if x != nil {
		return x.ActorGcid
	}
	return ""
}

func (x *GradingModelAnswerAmended) GetAmendedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.AmendedAt
	}
	return nil
}

// -----------------------------------------------------------------------------
// GradingMcqSnapshotMissing — chora.delivery.grading.mcq_snapshot_missing.v1
// -----------------------------------------------------------------------------
//
// Fired by chora-delivery when a submission's MCQ answer cannot be graded
// because its per-question payload snapshot is missing at grade time (the
// fail-loud ErrMCQSnapshotMissing path in Submission.GradeMCQAnswersFromSnapshot).
// A structured accountability signal — NOT a grading lifecycle transition —
// that lets O+ surface the gap and chora-notifications alert the instructor.
//
// Producer: services/chora-delivery/internal/adapter/http/assessment_handler.go
// emitMCQSnapshotMissingEvent. The envelope carries chora_imda_dimension =
// "accountability" (D1); the message fields below are the diagnostic payload.
//
// Consumers:
//   - chora-observability / O+ : surface the snapshot gap (IMDA D1)
//   - chora-notifications      : alert the instructor
//
// Contract authored by the 2026-07-01 event-fabric audit (Class D — the
// producer emitted to an unprovisioned topic; this proto + the Schema Registry
// binding + the protomarshal encoder land the event on a real binary topic).
type GradingMcqSnapshotMissing struct {
	state    protoimpl.MessageState `protogen:"open.v1"`
	Envelope *v1.EventEnvelope      `protobuf:"bytes,1,opt,name=envelope,proto3" json:"envelope,omitempty"`
	// UUIDv7 of the parent submission (chora_delivery.submissions).
	SubmissionId string `protobuf:"bytes,2,opt,name=submission_id,json=submissionId,proto3" json:"submission_id,omitempty"`
	// UUIDv7 of the assessment (chora_delivery.assessments).
	AssessmentId string `protobuf:"bytes,3,opt,name=assessment_id,json=assessmentId,proto3" json:"assessment_id,omitempty"`
	// GCID of the learner whose submission could not be graded.
	LearnerGcid string `protobuf:"bytes,4,opt,name=learner_gcid,json=learnerGcid,proto3" json:"learner_gcid,omitempty"`
	// Human-readable failure detail (the ErrMCQSnapshotMissing error string,
	// including the offending test_set_question_id).
	ErrorMessage string `protobuf:"bytes,5,opt,name=error_message,json=errorMessage,proto3" json:"error_message,omitempty"`
	// When the missing snapshot was detected at the producer's domain database.
	DetectedAt    *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=detected_at,json=detectedAt,proto3" json:"detected_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GradingMcqSnapshotMissing) Reset() {
	*x = GradingMcqSnapshotMissing{}
	mi := &file_events_delivery_grading_proto_msgTypes[16]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GradingMcqSnapshotMissing) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GradingMcqSnapshotMissing) ProtoMessage() {}

func (x *GradingMcqSnapshotMissing) ProtoReflect() protoreflect.Message {
	mi := &file_events_delivery_grading_proto_msgTypes[16]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GradingMcqSnapshotMissing.ProtoReflect.Descriptor instead.
func (*GradingMcqSnapshotMissing) Descriptor() ([]byte, []int) {
	return file_events_delivery_grading_proto_rawDescGZIP(), []int{16}
}

func (x *GradingMcqSnapshotMissing) GetEnvelope() *v1.EventEnvelope {
	if x != nil {
		return x.Envelope
	}
	return nil
}

func (x *GradingMcqSnapshotMissing) GetSubmissionId() string {
	if x != nil {
		return x.SubmissionId
	}
	return ""
}

func (x *GradingMcqSnapshotMissing) GetAssessmentId() string {
	if x != nil {
		return x.AssessmentId
	}
	return ""
}

func (x *GradingMcqSnapshotMissing) GetLearnerGcid() string {
	if x != nil {
		return x.LearnerGcid
	}
	return ""
}

func (x *GradingMcqSnapshotMissing) GetErrorMessage() string {
	if x != nil {
		return x.ErrorMessage
	}
	return ""
}

func (x *GradingMcqSnapshotMissing) GetDetectedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.DetectedAt
	}
	return nil
}

var File_events_delivery_grading_proto protoreflect.FileDescriptor

const file_events_delivery_grading_proto_rawDesc = "" +
	"\n" +
	"\x1devents/delivery/grading.proto\x12\x11chora.delivery.v1\x1a\x1fgoogle/protobuf/timestamp.proto\x1a\x1echora/common/v1/envelope.proto\"\x89\x02\n" +
	"\x17QuestionGradeBatchEntry\x12#\n" +
	"\rsubmission_id\x18\x01 \x01(\tR\fsubmissionId\x12/\n" +
	"\x14test_set_question_id\x18\x02 \x01(\tR\x11testSetQuestionId\x12\x1f\n" +
	"\vquestion_id\x18\x03 \x01(\tR\n" +
	"questionId\x12!\n" +
	"\flearner_gcid\x18\x04 \x01(\tR\vlearnerGcid\x12#\n" +
	"\rresponse_text\x18\x05 \x01(\tR\fresponseText\x12/\n" +
	"\x13accommodations_json\x18\x06 \x01(\tR\x12accommodationsJson\"\xf8\x03\n" +
	"\x18QuestionGradeBatchResult\x12#\n" +
	"\rsubmission_id\x18\x01 \x01(\tR\fsubmissionId\x12/\n" +
	"\x14test_set_question_id\x18\x02 \x01(\tR\x11testSetQuestionId\x12\x1f\n" +
	"\vquestion_id\x18\x03 \x01(\tR\n" +
	"questionId\x12!\n" +
	"\flearner_gcid\x18\x04 \x01(\tR\vlearnerGcid\x12#\n" +
	"\rpoints_earned\x18\x05 \x01(\x02R\fpointsEarned\x12'\n" +
	"\x0fpoints_possible\x18\x06 \x01(\x05R\x0epointsPossible\x122\n" +
	"\x15criterion_scores_json\x18\a \x01(\tR\x13criterionScoresJson\x124\n" +
	"\x16llm_evaluator_feedback\x18\b \x01(\tR\x14llmEvaluatorFeedback\x12(\n" +
	"\x10grading_model_id\x18\t \x01(\tR\x0egradingModelId\x12.\n" +
	"\x13grading_response_id\x18\n" +
	" \x01(\tR\x11gradingResponseId\x120\n" +
	"\x14grader_metadata_json\x18\v \x01(\tR\x12graderMetadataJson\"\xb4\x04\n" +
	"\x10GradingRequested\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12$\n" +
	"\x0egrading_job_id\x18\x02 \x01(\tR\fgradingJobId\x12#\n" +
	"\rsubmission_id\x18\x03 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x04 \x01(\tR\fassessmentId\x12\x1e\n" +
	"\vtest_set_id\x18\x05 \x01(\tR\ttestSetId\x12!\n" +
	"\flearner_gcid\x18\x06 \x01(\tR\vlearnerGcid\x12%\n" +
	"\x0equestion_count\x18\a \x01(\x05R\rquestionCount\x122\n" +
	"\x15total_points_possible\x18\b \x01(\x05R\x13totalPointsPossible\x12\x1b\n" +
	"\tmcq_count\x18\t \x01(\x05R\bmcqCount\x12\x19\n" +
	"\boe_count\x18\n" +
	" \x01(\x05R\aoeCount\x12:\n" +
	"\x19passing_threshold_percent\x18\v \x01(\x05R\x17passingThresholdPercent\x12#\n" +
	"\rforce_regrade\x18\f \x01(\bR\fforceRegrade\x12=\n" +
	"\frequested_at\x18\r \x01(\v2\x1a.google.protobuf.TimestampR\vrequestedAt\"\x86\x04\n" +
	"\x13GradingMcqCompleted\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12$\n" +
	"\x0egrading_job_id\x18\x02 \x01(\tR\fgradingJobId\x12#\n" +
	"\rsubmission_id\x18\x03 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x04 \x01(\tR\fassessmentId\x12!\n" +
	"\flearner_gcid\x18\x05 \x01(\tR\vlearnerGcid\x12*\n" +
	"\x11mcq_correct_count\x18\x06 \x01(\x05R\x0fmcqCorrectCount\x12.\n" +
	"\x13mcq_incorrect_count\x18\a \x01(\x05R\x11mcqIncorrectCount\x12*\n" +
	"\x11mcq_points_earned\x18\b \x01(\x02R\x0fmcqPointsEarned\x12.\n" +
	"\x13mcq_points_possible\x18\t \x01(\x05R\x11mcqPointsPossible\x12\"\n" +
	"\rno_oe_pending\x18\n" +
	" \x01(\bR\vnoOePending\x12D\n" +
	"\x10mcq_completed_at\x18\v \x01(\v2\x1a.google.protobuf.TimestampR\x0emcqCompletedAt\"\xbb\x05\n" +
	"\x17GradingOeBatchRequested\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12\x1e\n" +
	"\voe_batch_id\x18\x02 \x01(\tR\toeBatchId\x12#\n" +
	"\rassessment_id\x18\x03 \x01(\tR\fassessmentId\x12/\n" +
	"\x14test_set_question_id\x18\x04 \x01(\tR\x11testSetQuestionId\x12\x1f\n" +
	"\vquestion_id\x18\x05 \x01(\tR\n" +
	"questionId\x12\x1f\n" +
	"\vrubric_json\x18\x06 \x01(\tR\n" +
	"rubricJson\x12!\n" +
	"\fmodel_answer\x18\a \x01(\tR\vmodelAnswer\x12\x16\n" +
	"\x06prompt\x18\b \x01(\tR\x06prompt\x12C\n" +
	"\x1epoints_possible_per_submission\x18\t \x01(\x05R\x1bpointsPossiblePerSubmission\x12\x1d\n" +
	"\n" +
	"model_tier\x18\n" +
	" \x01(\tR\tmodelTier\x12A\n" +
	"\x1dper_question_feedback_enabled\x18\v \x01(\bR\x1aperQuestionFeedbackEnabled\x12W\n" +
	"\x11batch_submissions\x18\f \x03(\v2*.chora.delivery.v1.QuestionGradeBatchEntryR\x10batchSubmissions\x120\n" +
	"\x14estimated_mana_units\x18\r \x01(\x05R\x12estimatedManaUnits\x12?\n" +
	"\rdispatched_at\x18\x0e \x01(\v2\x1a.google.protobuf.TimestampR\fdispatchedAt\"\xbf\x05\n" +
	"\x17GradingOeBatchCompleted\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12\x1e\n" +
	"\voe_batch_id\x18\x02 \x01(\tR\toeBatchId\x12#\n" +
	"\rassessment_id\x18\x03 \x01(\tR\fassessmentId\x12/\n" +
	"\x14test_set_question_id\x18\x04 \x01(\tR\x11testSetQuestionId\x12\x1f\n" +
	"\vquestion_id\x18\x05 \x01(\tR\n" +
	"questionId\x12C\n" +
	"\x06graded\x18\x06 \x03(\v2+.chora.delivery.v1.QuestionGradeBatchResultR\x06graded\x12(\n" +
	"\x10grading_model_id\x18\a \x01(\tR\x0egradingModelId\x12!\n" +
	"\fmana_debited\x18\b \x01(\x05R\vmanaDebited\x12,\n" +
	"\x12total_input_tokens\x18\t \x01(\x03R\x10totalInputTokens\x12.\n" +
	"\x13total_output_tokens\x18\n" +
	" \x01(\x03R\x11totalOutputTokens\x12#\n" +
	"\rbatch_outcome\x18\v \x01(\tR\fbatchOutcome\x12T\n" +
	"\x10failure_category\x18\f \x01(\x0e2).chora.delivery.v1.GradingFailureCategoryR\x0ffailureCategory\x12'\n" +
	"\x0ffailure_message\x18\r \x01(\tR\x0efailureMessage\x12=\n" +
	"\fcompleted_at\x18\x0e \x01(\v2\x1a.google.protobuf.TimestampR\vcompletedAt\"\xc5\x04\n" +
	"\x10GradingFinalized\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12$\n" +
	"\x0egrading_job_id\x18\x02 \x01(\tR\fgradingJobId\x12#\n" +
	"\rsubmission_id\x18\x03 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x04 \x01(\tR\fassessmentId\x12!\n" +
	"\flearner_gcid\x18\x05 \x01(\tR\vlearnerGcid\x12.\n" +
	"\x13total_points_earned\x18\x06 \x01(\x02R\x11totalPointsEarned\x122\n" +
	"\x15total_points_possible\x18\a \x01(\x05R\x13totalPointsPossible\x12:\n" +
	"\x19passing_threshold_percent\x18\b \x01(\x05R\x17passingThresholdPercent\x12\x16\n" +
	"\x06passed\x18\t \x01(\bR\x06passed\x12.\n" +
	"\x13released_to_learner\x18\n" +
	" \x01(\bR\x11releasedToLearner\x12;\n" +
	"\x06status\x18\v \x01(\x0e2#.chora.delivery.v1.GradingJobStatusR\x06status\x12=\n" +
	"\ffinalized_at\x18\f \x01(\v2\x1a.google.protobuf.TimestampR\vfinalizedAt\"\x98\x04\n" +
	"\rGradingFailed\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12$\n" +
	"\x0egrading_job_id\x18\x02 \x01(\tR\fgradingJobId\x12#\n" +
	"\rsubmission_id\x18\x03 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x04 \x01(\tR\fassessmentId\x12!\n" +
	"\flearner_gcid\x18\x05 \x01(\tR\vlearnerGcid\x12T\n" +
	"\x10failure_category\x18\x06 \x01(\x0e2).chora.delivery.v1.GradingFailureCategoryR\x0ffailureCategory\x12'\n" +
	"\x0ffailure_message\x18\a \x01(\tR\x0efailureMessage\x12;\n" +
	"\x06status\x18\b \x01(\x0e2#.chora.delivery.v1.GradingJobStatusR\x06status\x12#\n" +
	"\rattempt_count\x18\t \x01(\x05R\fattemptCount\x12\x1e\n" +
	"\voe_batch_id\x18\n" +
	" \x01(\tR\toeBatchId\x127\n" +
	"\tfailed_at\x18\v \x01(\v2\x1a.google.protobuf.TimestampR\bfailedAt\"\xe3\x03\n" +
	"\x19SubmissionQuestionContext\x12/\n" +
	"\x14test_set_question_id\x18\x01 \x01(\tR\x11testSetQuestionId\x12\x1f\n" +
	"\vquestion_id\x18\x02 \x01(\tR\n" +
	"questionId\x12#\n" +
	"\rquestion_type\x18\x03 \x01(\tR\fquestionType\x12#\n" +
	"\rdisplay_order\x18\x04 \x01(\x05R\fdisplayOrder\x12'\n" +
	"\x0fpoints_possible\x18\x05 \x01(\x05R\x0epointsPossible\x12\x16\n" +
	"\x06prompt\x18\x06 \x01(\tR\x06prompt\x12\x18\n" +
	"\asubject\x18\a \x01(\tR\asubject\x12\x14\n" +
	"\x05topic\x18\b \x01(\tR\x05topic\x12(\n" +
	"\x10oe_response_text\x18\t \x01(\tR\x0eoeResponseText\x12\x1f\n" +
	"\vrubric_json\x18\n" +
	" \x01(\tR\n" +
	"rubricJson\x12!\n" +
	"\fmodel_answer\x18\v \x01(\tR\vmodelAnswer\x12\x1f\n" +
	"\vmcq_correct\x18\f \x01(\bR\n" +
	"mcqCorrect\x12*\n" +
	"\x11mcq_points_earned\x18\r \x01(\x02R\x0fmcqPointsEarned\"\xad\x03\n" +
	"\x15OEQuestionGradeResult\x12/\n" +
	"\x14test_set_question_id\x18\x01 \x01(\tR\x11testSetQuestionId\x12\x1f\n" +
	"\vquestion_id\x18\x02 \x01(\tR\n" +
	"questionId\x12#\n" +
	"\rpoints_earned\x18\x03 \x01(\x02R\fpointsEarned\x12'\n" +
	"\x0fpoints_possible\x18\x04 \x01(\x05R\x0epointsPossible\x122\n" +
	"\x15criterion_scores_json\x18\x05 \x01(\tR\x13criterionScoresJson\x12\x18\n" +
	"\acomment\x18\x06 \x01(\tR\acomment\x12(\n" +
	"\x10grading_model_id\x18\a \x01(\tR\x0egradingModelId\x12.\n" +
	"\x13grading_response_id\x18\b \x01(\tR\x11gradingResponseId\x12'\n" +
	"\x0fquality_flagged\x18\t \x01(\bR\x0equalityFlagged\x12#\n" +
	"\rattempt_count\x18\n" +
	" \x01(\x05R\fattemptCount\"\xe0\x05\n" +
	"\x1aGradingSubmissionRequested\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12$\n" +
	"\x0egrading_job_id\x18\x02 \x01(\tR\fgradingJobId\x12#\n" +
	"\rsubmission_id\x18\x03 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x04 \x01(\tR\fassessmentId\x12\x1e\n" +
	"\vtest_set_id\x18\x05 \x01(\tR\ttestSetId\x12!\n" +
	"\flearner_gcid\x18\x06 \x01(\tR\vlearnerGcid\x12:\n" +
	"\x19passing_threshold_percent\x18\a \x01(\x05R\x17passingThresholdPercent\x12\x1d\n" +
	"\n" +
	"model_tier\x18\b \x01(\tR\tmodelTier\x12A\n" +
	"\x1dper_question_feedback_enabled\x18\t \x01(\bR\x1aperQuestionFeedbackEnabled\x12\x18\n" +
	"\asubject\x18\n" +
	" \x01(\tR\asubject\x12J\n" +
	"\tquestions\x18\v \x03(\v2,.chora.delivery.v1.SubmissionQuestionContextR\tquestions\x122\n" +
	"\x15total_points_possible\x18\f \x01(\x05R\x13totalPointsPossible\x12*\n" +
	"\x11mcq_points_earned\x18\r \x01(\x02R\x0fmcqPointsEarned\x120\n" +
	"\x14estimated_mana_units\x18\x0e \x01(\x05R\x12estimatedManaUnits\x12=\n" +
	"\frequested_at\x18\x0f \x01(\v2\x1a.google.protobuf.TimestampR\vrequestedAt\"\xa7\x06\n" +
	"\x1aGradingSubmissionCompleted\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12$\n" +
	"\x0egrading_job_id\x18\x02 \x01(\tR\fgradingJobId\x12#\n" +
	"\rsubmission_id\x18\x03 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x04 \x01(\tR\fassessmentId\x12!\n" +
	"\flearner_gcid\x18\x05 \x01(\tR\vlearnerGcid\x12@\n" +
	"\x06graded\x18\x06 \x03(\v2(.chora.delivery.v1.OEQuestionGradeResultR\x06graded\x12'\n" +
	"\x0foverall_comment\x18\a \x01(\tR\x0eoverallComment\x127\n" +
	"\x18overall_comment_model_id\x18\b \x01(\tR\x15overallCommentModelId\x12=\n" +
	"\x1boverall_comment_response_id\x18\t \x01(\tR\x18overallCommentResponseId\x12\x18\n" +
	"\aoutcome\x18\n" +
	" \x01(\tR\aoutcome\x12T\n" +
	"\x10failure_category\x18\v \x01(\x0e2).chora.delivery.v1.GradingFailureCategoryR\x0ffailureCategory\x12'\n" +
	"\x0ffailure_message\x18\f \x01(\tR\x0efailureMessage\x12!\n" +
	"\fmana_debited\x18\r \x01(\x05R\vmanaDebited\x12,\n" +
	"\x12total_input_tokens\x18\x0e \x01(\x03R\x10totalInputTokens\x12.\n" +
	"\x13total_output_tokens\x18\x0f \x01(\x03R\x11totalOutputTokens\x12=\n" +
	"\fcompleted_at\x18\x10 \x01(\v2\x1a.google.protobuf.TimestampR\vcompletedAt\"\xfc\x03\n" +
	"\x16GradingScoreOverridden\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12#\n" +
	"\rsubmission_id\x18\x02 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x03 \x01(\tR\fassessmentId\x12\x1b\n" +
	"\tanswer_id\x18\x04 \x01(\tR\banswerId\x12/\n" +
	"\x14test_set_question_id\x18\x05 \x01(\tR\x11testSetQuestionId\x12\x1f\n" +
	"\vquestion_id\x18\x06 \x01(\tR\n" +
	"questionId\x12;\n" +
	"\x05field\x18\a \x01(\x0e2%.chora.delivery.v1.GradeOverrideFieldR\x05field\x12\x1b\n" +
	"\told_value\x18\b \x01(\tR\boldValue\x12\x1b\n" +
	"\tnew_value\x18\t \x01(\tR\bnewValue\x12\x1d\n" +
	"\n" +
	"actor_gcid\x18\n" +
	" \x01(\tR\tactorGcid\x12\x16\n" +
	"\x06reason\x18\v \x01(\tR\x06reason\x12?\n" +
	"\roverridden_at\x18\f \x01(\v2\x1a.google.protobuf.TimestampR\foverriddenAt\"\xa0\x02\n" +
	"\x19GradingSubmissionApproved\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12#\n" +
	"\rsubmission_id\x18\x02 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x03 \x01(\tR\fassessmentId\x12#\n" +
	"\rapprover_gcid\x18\x04 \x01(\tR\fapproverGcid\x12\x1b\n" +
	"\thad_edits\x18\x05 \x01(\bR\bhadEdits\x12;\n" +
	"\vapproved_at\x18\x06 \x01(\v2\x1a.google.protobuf.TimestampR\n" +
	"approvedAt\"\xb6\x02\n" +
	"\x19GradingAssessmentApproved\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12#\n" +
	"\rassessment_id\x18\x02 \x01(\tR\fassessmentId\x12#\n" +
	"\rapprover_gcid\x18\x03 \x01(\tR\fapproverGcid\x12:\n" +
	"\x19approved_submission_count\x18\x04 \x01(\x05R\x17approvedSubmissionCount\x12\x1a\n" +
	"\breleased\x18\x05 \x01(\bR\breleased\x12;\n" +
	"\vapproved_at\x18\x06 \x01(\v2\x1a.google.protobuf.TimestampR\n" +
	"approvedAt\"\x90\x03\n" +
	"\x19GradingModelAnswerAmended\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12#\n" +
	"\rsubmission_id\x18\x02 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x03 \x01(\tR\fassessmentId\x12/\n" +
	"\x14test_set_question_id\x18\x04 \x01(\tR\x11testSetQuestionId\x12\x1f\n" +
	"\vquestion_id\x18\x05 \x01(\tR\n" +
	"questionId\x12\x17\n" +
	"\aatom_id\x18\x06 \x01(\tR\x06atomId\x12(\n" +
	"\x10new_model_answer\x18\a \x01(\tR\x0enewModelAnswer\x12\x1d\n" +
	"\n" +
	"actor_gcid\x18\b \x01(\tR\tactorGcid\x129\n" +
	"\n" +
	"amended_at\x18\t \x01(\v2\x1a.google.protobuf.TimestampR\tamendedAt\"\xa6\x02\n" +
	"\x19GradingMcqSnapshotMissing\x12:\n" +
	"\benvelope\x18\x01 \x01(\v2\x1e.chora.common.v1.EventEnvelopeR\benvelope\x12#\n" +
	"\rsubmission_id\x18\x02 \x01(\tR\fsubmissionId\x12#\n" +
	"\rassessment_id\x18\x03 \x01(\tR\fassessmentId\x12!\n" +
	"\flearner_gcid\x18\x04 \x01(\tR\vlearnerGcid\x12#\n" +
	"\rerror_message\x18\x05 \x01(\tR\ferrorMessage\x12;\n" +
	"\vdetected_at\x18\x06 \x01(\v2\x1a.google.protobuf.TimestampR\n" +
	"detectedAt*{\n" +
	"\x0fGradingDispatch\x12 \n" +
	"\x1cGRADING_DISPATCH_UNSPECIFIED\x10\x00\x12\"\n" +
	"\x1eGRADING_DISPATCH_DETERMINISTIC\x10\x01\x12\"\n" +
	"\x1eGRADING_DISPATCH_LLM_EVALUATOR\x10\x02*\xda\x02\n" +
	"\x10GradingJobStatus\x12\"\n" +
	"\x1eGRADING_JOB_STATUS_UNSPECIFIED\x10\x00\x12 \n" +
	"\x1cGRADING_JOB_STATUS_REQUESTED\x10\x01\x12\x1e\n" +
	"\x1aGRADING_JOB_STATUS_RUNNING\x10\x02\x12-\n" +
	")GRADING_JOB_STATUS_MCQ_DETERMINISTIC_DONE\x10\x03\x12'\n" +
	"#GRADING_JOB_STATUS_OE_BATCH_PENDING\x10\x04\x12(\n" +
	"$GRADING_JOB_STATUS_OE_BATCH_COMPLETE\x10\x05\x12\x1d\n" +
	"\x19GRADING_JOB_STATUS_SCORED\x10\x06\x12 \n" +
	"\x1cGRADING_JOB_STATUS_FINALIZED\x10\a\x12\x1d\n" +
	"\x19GRADING_JOB_STATUS_FAILED\x10\b*\xd7\x02\n" +
	"\x16GradingFailureCategory\x12(\n" +
	"$GRADING_FAILURE_CATEGORY_UNSPECIFIED\x10\x00\x12*\n" +
	"&GRADING_FAILURE_CATEGORY_VERTEX_AI_5XX\x10\x01\x120\n" +
	",GRADING_FAILURE_CATEGORY_MODEL_ARMOR_BLOCKED\x10\x02\x12$\n" +
	" GRADING_FAILURE_CATEGORY_TIMEOUT\x10\x03\x12+\n" +
	"'GRADING_FAILURE_CATEGORY_QUOTA_EXCEEDED\x10\x04\x125\n" +
	"1GRADING_FAILURE_CATEGORY_INSUFFICIENT_TENANT_MANA\x10\x05\x12+\n" +
	"'GRADING_FAILURE_CATEGORY_INTERNAL_ERROR\x10\x06*\xcd\x01\n" +
	"\x12GradeOverrideField\x12$\n" +
	" GRADE_OVERRIDE_FIELD_UNSPECIFIED\x10\x00\x12\x1e\n" +
	"\x1aGRADE_OVERRIDE_FIELD_SCORE\x10\x01\x12 \n" +
	"\x1cGRADE_OVERRIDE_FIELD_COMMENT\x10\x02\x12%\n" +
	"!GRADE_OVERRIDE_FIELD_MODEL_ANSWER\x10\x03\x12(\n" +
	"$GRADE_OVERRIDE_FIELD_OVERALL_COMMENT\x10\x04BMZKgithub.com/apollo-chora/chora-contracts/gen/go/chora/delivery/v1;deliveryv1b\x06proto3"

var (
	file_events_delivery_grading_proto_rawDescOnce sync.Once
	file_events_delivery_grading_proto_rawDescData []byte
)

func file_events_delivery_grading_proto_rawDescGZIP() []byte {
	file_events_delivery_grading_proto_rawDescOnce.Do(func() {
		file_events_delivery_grading_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_events_delivery_grading_proto_rawDesc), len(file_events_delivery_grading_proto_rawDesc)))
	})
	return file_events_delivery_grading_proto_rawDescData
}

var file_events_delivery_grading_proto_enumTypes = make([]protoimpl.EnumInfo, 4)
var file_events_delivery_grading_proto_msgTypes = make([]protoimpl.MessageInfo, 17)
var file_events_delivery_grading_proto_goTypes = []any{
	(GradingDispatch)(0),               // 0: chora.delivery.v1.GradingDispatch
	(GradingJobStatus)(0),              // 1: chora.delivery.v1.GradingJobStatus
	(GradingFailureCategory)(0),        // 2: chora.delivery.v1.GradingFailureCategory
	(GradeOverrideField)(0),            // 3: chora.delivery.v1.GradeOverrideField
	(*QuestionGradeBatchEntry)(nil),    // 4: chora.delivery.v1.QuestionGradeBatchEntry
	(*QuestionGradeBatchResult)(nil),   // 5: chora.delivery.v1.QuestionGradeBatchResult
	(*GradingRequested)(nil),           // 6: chora.delivery.v1.GradingRequested
	(*GradingMcqCompleted)(nil),        // 7: chora.delivery.v1.GradingMcqCompleted
	(*GradingOeBatchRequested)(nil),    // 8: chora.delivery.v1.GradingOeBatchRequested
	(*GradingOeBatchCompleted)(nil),    // 9: chora.delivery.v1.GradingOeBatchCompleted
	(*GradingFinalized)(nil),           // 10: chora.delivery.v1.GradingFinalized
	(*GradingFailed)(nil),              // 11: chora.delivery.v1.GradingFailed
	(*SubmissionQuestionContext)(nil),  // 12: chora.delivery.v1.SubmissionQuestionContext
	(*OEQuestionGradeResult)(nil),      // 13: chora.delivery.v1.OEQuestionGradeResult
	(*GradingSubmissionRequested)(nil), // 14: chora.delivery.v1.GradingSubmissionRequested
	(*GradingSubmissionCompleted)(nil), // 15: chora.delivery.v1.GradingSubmissionCompleted
	(*GradingScoreOverridden)(nil),     // 16: chora.delivery.v1.GradingScoreOverridden
	(*GradingSubmissionApproved)(nil),  // 17: chora.delivery.v1.GradingSubmissionApproved
	(*GradingAssessmentApproved)(nil),  // 18: chora.delivery.v1.GradingAssessmentApproved
	(*GradingModelAnswerAmended)(nil),  // 19: chora.delivery.v1.GradingModelAnswerAmended
	(*GradingMcqSnapshotMissing)(nil),  // 20: chora.delivery.v1.GradingMcqSnapshotMissing
	(*v1.EventEnvelope)(nil),           // 21: chora.common.v1.EventEnvelope
	(*timestamppb.Timestamp)(nil),      // 22: google.protobuf.Timestamp
}
var file_events_delivery_grading_proto_depIdxs = []int32{
	21, // 0: chora.delivery.v1.GradingRequested.envelope:type_name -> chora.common.v1.EventEnvelope
	22, // 1: chora.delivery.v1.GradingRequested.requested_at:type_name -> google.protobuf.Timestamp
	21, // 2: chora.delivery.v1.GradingMcqCompleted.envelope:type_name -> chora.common.v1.EventEnvelope
	22, // 3: chora.delivery.v1.GradingMcqCompleted.mcq_completed_at:type_name -> google.protobuf.Timestamp
	21, // 4: chora.delivery.v1.GradingOeBatchRequested.envelope:type_name -> chora.common.v1.EventEnvelope
	4,  // 5: chora.delivery.v1.GradingOeBatchRequested.batch_submissions:type_name -> chora.delivery.v1.QuestionGradeBatchEntry
	22, // 6: chora.delivery.v1.GradingOeBatchRequested.dispatched_at:type_name -> google.protobuf.Timestamp
	21, // 7: chora.delivery.v1.GradingOeBatchCompleted.envelope:type_name -> chora.common.v1.EventEnvelope
	5,  // 8: chora.delivery.v1.GradingOeBatchCompleted.graded:type_name -> chora.delivery.v1.QuestionGradeBatchResult
	2,  // 9: chora.delivery.v1.GradingOeBatchCompleted.failure_category:type_name -> chora.delivery.v1.GradingFailureCategory
	22, // 10: chora.delivery.v1.GradingOeBatchCompleted.completed_at:type_name -> google.protobuf.Timestamp
	21, // 11: chora.delivery.v1.GradingFinalized.envelope:type_name -> chora.common.v1.EventEnvelope
	1,  // 12: chora.delivery.v1.GradingFinalized.status:type_name -> chora.delivery.v1.GradingJobStatus
	22, // 13: chora.delivery.v1.GradingFinalized.finalized_at:type_name -> google.protobuf.Timestamp
	21, // 14: chora.delivery.v1.GradingFailed.envelope:type_name -> chora.common.v1.EventEnvelope
	2,  // 15: chora.delivery.v1.GradingFailed.failure_category:type_name -> chora.delivery.v1.GradingFailureCategory
	1,  // 16: chora.delivery.v1.GradingFailed.status:type_name -> chora.delivery.v1.GradingJobStatus
	22, // 17: chora.delivery.v1.GradingFailed.failed_at:type_name -> google.protobuf.Timestamp
	21, // 18: chora.delivery.v1.GradingSubmissionRequested.envelope:type_name -> chora.common.v1.EventEnvelope
	12, // 19: chora.delivery.v1.GradingSubmissionRequested.questions:type_name -> chora.delivery.v1.SubmissionQuestionContext
	22, // 20: chora.delivery.v1.GradingSubmissionRequested.requested_at:type_name -> google.protobuf.Timestamp
	21, // 21: chora.delivery.v1.GradingSubmissionCompleted.envelope:type_name -> chora.common.v1.EventEnvelope
	13, // 22: chora.delivery.v1.GradingSubmissionCompleted.graded:type_name -> chora.delivery.v1.OEQuestionGradeResult
	2,  // 23: chora.delivery.v1.GradingSubmissionCompleted.failure_category:type_name -> chora.delivery.v1.GradingFailureCategory
	22, // 24: chora.delivery.v1.GradingSubmissionCompleted.completed_at:type_name -> google.protobuf.Timestamp
	21, // 25: chora.delivery.v1.GradingScoreOverridden.envelope:type_name -> chora.common.v1.EventEnvelope
	3,  // 26: chora.delivery.v1.GradingScoreOverridden.field:type_name -> chora.delivery.v1.GradeOverrideField
	22, // 27: chora.delivery.v1.GradingScoreOverridden.overridden_at:type_name -> google.protobuf.Timestamp
	21, // 28: chora.delivery.v1.GradingSubmissionApproved.envelope:type_name -> chora.common.v1.EventEnvelope
	22, // 29: chora.delivery.v1.GradingSubmissionApproved.approved_at:type_name -> google.protobuf.Timestamp
	21, // 30: chora.delivery.v1.GradingAssessmentApproved.envelope:type_name -> chora.common.v1.EventEnvelope
	22, // 31: chora.delivery.v1.GradingAssessmentApproved.approved_at:type_name -> google.protobuf.Timestamp
	21, // 32: chora.delivery.v1.GradingModelAnswerAmended.envelope:type_name -> chora.common.v1.EventEnvelope
	22, // 33: chora.delivery.v1.GradingModelAnswerAmended.amended_at:type_name -> google.protobuf.Timestamp
	21, // 34: chora.delivery.v1.GradingMcqSnapshotMissing.envelope:type_name -> chora.common.v1.EventEnvelope
	22, // 35: chora.delivery.v1.GradingMcqSnapshotMissing.detected_at:type_name -> google.protobuf.Timestamp
	36, // [36:36] is the sub-list for method output_type
	36, // [36:36] is the sub-list for method input_type
	36, // [36:36] is the sub-list for extension type_name
	36, // [36:36] is the sub-list for extension extendee
	0,  // [0:36] is the sub-list for field type_name
}

func init() { file_events_delivery_grading_proto_init() }
func file_events_delivery_grading_proto_init() {
	if File_events_delivery_grading_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_events_delivery_grading_proto_rawDesc), len(file_events_delivery_grading_proto_rawDesc)),
			NumEnums:      4,
			NumMessages:   17,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_events_delivery_grading_proto_goTypes,
		DependencyIndexes: file_events_delivery_grading_proto_depIdxs,
		EnumInfos:         file_events_delivery_grading_proto_enumTypes,
		MessageInfos:      file_events_delivery_grading_proto_msgTypes,
	}.Build()
	File_events_delivery_grading_proto = out.File
	file_events_delivery_grading_proto_goTypes = nil
	file_events_delivery_grading_proto_depIdxs = nil
}
