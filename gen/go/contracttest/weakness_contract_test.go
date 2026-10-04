// Package contracttest holds hand-written CONTRACT-CONFORMANCE tests that pin
// the shape of generated stubs the platform depends on. It lives OUTSIDE the
// generated subtrees (gen/go/chora, gen/python) so `make codegen` (clean-gen)
// never deletes it, yet it compiles against the freshly generated types in the
// same module — making it a compile-enforced guard on additive proto changes.
//
// WS-1 (ADR-205 / CHO-1953): the Growth-Edge analyser graduation adds bounded,
// injection-safe learner steering + output selection + a mana reservation to
// the (already-live, schema-unregistered) WeaknessDocUploaded event. These
// tests fail to COMPILE until weakness.proto carries the additive fields and is
// regenerated — that compile failure IS the RED state.
package contracttest

import (
	"testing"

	"google.golang.org/protobuf/proto"

	consumptionv1 "github.com/apollo-chora/chora-contracts/gen/go/chora/consumption/v1"
)

// TestWeaknessDocUploaded_StructuredClues_RoundTrip proves the new bounded
// structured-clue steering survives a proto round-trip with field fidelity.
// Replaces the injection-prone free-text context_hint as the DATA-only path.
func TestWeaknessDocUploaded_StructuredClues_RoundTrip(t *testing.T) {
	src := &consumptionv1.WeaknessDocUploaded{
		UploadId:      "0190a000-0000-7000-8000-000000000001",
		TenantId:      "tenant-1",
		LearnerGcid:   "gcid-1",
		UploadKind:    "source_material", // new documented kind (textbook grounding)
		ReservationId: "rsv-0190a000",    // new field 12
		StructuredClues: &consumptionv1.StructuredClues{
			Subject:        "physical-geography",
			WeakTopicKeys:  []string{"riverine-flood-causes", "monsoon-cycle"},
			SelfConfidence: 2, // 1..5; 2 = quite shaky
			ContextKind:    consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_EXAM,
			Note:           "I keep mixing up the flood stages.",
		},
		RequestedOutputs: &consumptionv1.OutputSelection{
			FocusedDose:       true,
			FamiliarCoaching: true,
			PracticeTest:      false,
			StudyAids:         true,
		},
	}

	wire, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("marshal WeaknessDocUploaded: %v", err)
	}
	var got consumptionv1.WeaknessDocUploaded
	if err := proto.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal WeaknessDocUploaded: %v", err)
	}

	if got.GetReservationId() != "rsv-0190a000" {
		t.Errorf("reservation_id: got %q want %q", got.GetReservationId(), "rsv-0190a000")
	}
	if got.GetUploadKind() != "source_material" {
		t.Errorf("upload_kind: got %q want %q", got.GetUploadKind(), "source_material")
	}
	sc := got.GetStructuredClues()
	if sc == nil {
		t.Fatal("structured_clues lost in round-trip")
	}
	if sc.GetSubject() != "physical-geography" {
		t.Errorf("subject: got %q", sc.GetSubject())
	}
	if len(sc.GetWeakTopicKeys()) != 2 || sc.GetWeakTopicKeys()[0] != "riverine-flood-causes" {
		t.Errorf("weak_topic_keys: got %v", sc.GetWeakTopicKeys())
	}
	if sc.GetSelfConfidence() != 2 {
		t.Errorf("self_confidence: got %d want 2", sc.GetSelfConfidence())
	}
	if sc.GetContextKind() != consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_EXAM {
		t.Errorf("context_kind: got %v", sc.GetContextKind())
	}
	if sc.GetNote() != "I keep mixing up the flood stages." {
		t.Errorf("note: got %q", sc.GetNote())
	}
	out := got.GetRequestedOutputs()
	if out == nil || !out.GetFocusedDose() || !out.GetFamiliarCoaching() || out.GetPracticeTest() || !out.GetStudyAids() {
		t.Errorf("requested_outputs round-trip mismatch: %+v", out)
	}
}

// TestWeaknessContextKind_EnumValues pins the bounded context-kind set. The
// zero value MUST be UNSPECIFIED (buf STANDARD enum_zero_value_suffix) so an
// unset clue is unambiguous, never silently "exam".
func TestWeaknessContextKind_EnumValues(t *testing.T) {
	if consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_UNSPECIFIED != 0 {
		t.Errorf("UNSPECIFIED must be 0, got %d", consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_UNSPECIFIED)
	}
	for _, v := range []consumptionv1.WeaknessContextKind{
		consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_EXAM,
		consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_ASSIGNMENT,
		consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_QUIZ,
		consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_PRACTICE,
		consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_SELF_STUDY,
	} {
		if v == consumptionv1.WeaknessContextKind_WEAKNESS_CONTEXT_KIND_UNSPECIFIED {
			t.Errorf("bounded value collapsed to UNSPECIFIED: %v", v)
		}
	}
}

// TestWeaknessAnalyzed_Unchanged guards the BLAST RADIUS: the EXISTING fields the
// live consumption upsert reads must remain exactly as they are today. WS-1 left
// this message untouched; the CHO-1966 integration seam extends it PURELY
// ADDITIVELY with field 10 (output_selection — see
// TestWeaknessAnalyzed_OutputSelection_RoundTrip), which is wire-compatible (the
// live subscriber ignores the unknown field). This guard ensures any future
// change to the fields below stays additive — never a removal/retype/renumber.
func TestWeaknessAnalyzed_Unchanged(t *testing.T) {
	a := &consumptionv1.WeaknessAnalyzed{
		UploadId:    "u1",
		TenantId:    "t1",
		LearnerGcid: "g1",
		ModelUsed:   "gemini-3-pro-preview",
		Edges: []*consumptionv1.ExtractedGrowthEdge{
			{ConceptLabel: "x", ConceptKey: "x", Confidence: 0.9, Strength: 0.8, DescriptorJson: "{}"},
		},
		InputTokenCount:  10,
		OutputTokenCount: 20,
	}
	if a.GetEdges()[0].GetConceptLabel() != "x" {
		t.Fatal("ExtractedGrowthEdge shape drifted")
	}
}

// TestWeaknessAnalyzed_OutputSelection_RoundTrip proves the integration-seam
// additive field (output_selection on the analyzed event; ADR-205 D5 / CHO-1966)
// survives a proto round-trip. The graduated crew emits the learner's post-HITL
// selection so the consumption Companion-RAG subscriber can gate familiar_coaching (the WIRE-FROZEN name, ADR-254).
// Additive (field 10) — wire-compatible; the live single-shot path leaves it nil
// (→ no Companion-RAG write, the safe default). Reuses the existing OutputSelection
// message so the wire shape matches the upload event's requested_outputs.
func TestWeaknessAnalyzed_OutputSelection_RoundTrip(t *testing.T) {
	src := &consumptionv1.WeaknessAnalyzed{
		UploadId:    "u1",
		TenantId:    "t1",
		LearnerGcid: "g1",
		OutputSelection: &consumptionv1.OutputSelection{
			FocusedDose:       true,
			FamiliarCoaching: true,
			PracticeTest:      false,
			StudyAids:         true,
		},
	}
	wire, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("marshal WeaknessAnalyzed: %v", err)
	}
	var got consumptionv1.WeaknessAnalyzed
	if err := proto.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal WeaknessAnalyzed: %v", err)
	}
	sel := got.GetOutputSelection()
	if sel == nil {
		t.Fatal("output_selection lost in round-trip")
	}
	if !sel.GetFocusedDose() || !sel.GetFamiliarCoaching() || sel.GetPracticeTest() || !sel.GetStudyAids() {
		t.Errorf("output_selection round-trip mismatch: %+v", sel)
	}
}

// TestWeaknessReviewPending_RoundTrip proves the bounded HITL review panel
// (ADR-205 D4 / CHO-1973) survives a proto round-trip with field fidelity. This
// is the missing review-panel bridge: the graduated crew emits it when it pauses
// at the HITL interrupt, carrying the panel out of the orchestrator's LangGraph
// checkpoint and across the domain boundary (cross-DB forbidden) so chora-
// consumption can park the upload AWAITING_REVIEW and surface the panel to the
// A+ FE. The proposed_edge_id is EPHEMERAL (the orchestrator alone maps it back
// to its checkpoint candidate-edge); there is NO run_id on the wire (the resume
// thread is deterministic on {tenant, upload}). Fails to COMPILE until
// weakness.proto carries WeaknessReviewPending + its sub-messages and is
// regenerated — that compile failure IS the RED state.
func TestWeaknessReviewPending_RoundTrip(t *testing.T) {
	src := &consumptionv1.WeaknessReviewPending{
		UploadId:    "0190a000-0000-7000-8000-000000000001",
		TenantId:    "tenant-1",
		LearnerGcid: "gcid-1",
		Familiar: &consumptionv1.ReviewFamiliar{
			FamiliarId: "fam-1",
			Name:        "Ember",
			Species:     "dragon",
		},
		ProposedEdges: []*consumptionv1.ProposedGrowthEdge{
			{
				ProposedEdgeId:      "pe-0",
				ConceptLabel:        "causes of riverine flooding",
				Summary:             "mixes up flood causes with effects",
				SuggestedAngles:     []string{"compare causes vs effects", "label a flood diagram"},
				Strength:            0.8,
				SuggestedDifficulty: "standard",
			},
		},
		CandidateStruggles: []*consumptionv1.CandidateStruggle{
			{ConceptKey: "monsoon-cycle", ConceptLabel: "the monsoon cycle"},
		},
		AvailableOutputs: []*consumptionv1.AvailableOutput{
			{Kind: "focused_dose", ManaPrice: 0, DefaultSelected: true},
			{Kind: "practice_test", ManaPrice: 50, DefaultSelected: false},
		},
	}

	wire, err := proto.Marshal(src)
	if err != nil {
		t.Fatalf("marshal WeaknessReviewPending: %v", err)
	}
	var got consumptionv1.WeaknessReviewPending
	if err := proto.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal WeaknessReviewPending: %v", err)
	}

	if got.GetUploadId() != "0190a000-0000-7000-8000-000000000001" {
		t.Errorf("upload_id: got %q", got.GetUploadId())
	}
	if got.GetLearnerGcid() != "gcid-1" {
		t.Errorf("learner_gcid: got %q", got.GetLearnerGcid())
	}
	fam := got.GetFamiliar()
	if fam == nil || fam.GetFamiliarId() != "fam-1" || fam.GetName() != "Ember" || fam.GetSpecies() != "dragon" {
		t.Errorf("companion round-trip mismatch: %+v", fam)
	}
	if len(got.GetProposedEdges()) != 1 {
		t.Fatalf("proposed_edges: got %d want 1", len(got.GetProposedEdges()))
	}
	pe := got.GetProposedEdges()[0]
	if pe.GetProposedEdgeId() != "pe-0" {
		t.Errorf("proposed_edge_id: got %q", pe.GetProposedEdgeId())
	}
	if pe.GetConceptLabel() != "causes of riverine flooding" {
		t.Errorf("concept_label: got %q", pe.GetConceptLabel())
	}
	if pe.GetSummary() != "mixes up flood causes with effects" {
		t.Errorf("summary: got %q", pe.GetSummary())
	}
	if len(pe.GetSuggestedAngles()) != 2 {
		t.Errorf("suggested_angles: got %v", pe.GetSuggestedAngles())
	}
	if pe.GetStrength() != 0.8 {
		t.Errorf("strength: got %v want 0.8", pe.GetStrength())
	}
	if pe.GetSuggestedDifficulty() != "standard" {
		t.Errorf("suggested_difficulty: got %q", pe.GetSuggestedDifficulty())
	}
	if len(got.GetCandidateStruggles()) != 1 || got.GetCandidateStruggles()[0].GetConceptKey() != "monsoon-cycle" {
		t.Errorf("candidate_struggles round-trip mismatch: %+v", got.GetCandidateStruggles())
	}
	outs := got.GetAvailableOutputs()
	if len(outs) != 2 {
		t.Fatalf("available_outputs: got %d want 2", len(outs))
	}
	if outs[0].GetKind() != "focused_dose" || outs[0].GetManaPrice() != 0 || !outs[0].GetDefaultSelected() {
		t.Errorf("available_outputs[0] mismatch: %+v", outs[0])
	}
	if outs[1].GetKind() != "practice_test" || outs[1].GetManaPrice() != 50 || outs[1].GetDefaultSelected() {
		t.Errorf("available_outputs[1] mismatch: %+v", outs[1])
	}
}
