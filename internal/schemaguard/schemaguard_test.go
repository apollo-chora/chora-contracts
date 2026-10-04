package schemaguard_test

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/apollo-chora/chora-contracts/internal/schemaguard"
)

// A committed Pub/Sub schema, in the exact shape protoflatten emits: ONE
// top-level message, with EventEnvelope + Timestamp + any enum inlined as
// NESTED declarations. The nested declarations' fields must NOT be mistaken
// for top-level fields — that is the whole difficulty of parsing these.
const committedSubmissionGraded = `
syntax = "proto3";

package chora.delivery.v1;

message SubmissionGraded {
  message Envelope {
    string event_id = 1;
    string idempotency_key = 2;
    string tenant_id = 3;
  }

  message Timestamp {
    int64 seconds = 1;
    int32 nanos = 2;
  }

  enum SubmissionState {
    SUBMISSION_STATE_UNSPECIFIED = 0;
    SUBMISSION_STATE_GRADED = 4;
  }

  Envelope envelope = 1;
  string submission_id = 2;
  SubmissionState state = 6;
  float total_points_earned = 7;
  bool passed = 10;
  Timestamp graded_at = 11;
  repeated string imda_dimensions = 20;
}
`

func TestParseSchemaDefinition_TopLevelFieldsOnly(t *testing.T) {
	msg, err := schemaguard.ParseSchemaDefinition(committedSubmissionGraded)
	if err != nil {
		t.Fatalf("ParseSchemaDefinition: %v", err)
	}
	if msg.Package != "chora.delivery.v1" {
		t.Errorf("Package = %q, want chora.delivery.v1", msg.Package)
	}
	if msg.Name != "SubmissionGraded" {
		t.Errorf("Name = %q, want SubmissionGraded", msg.Name)
	}
	// 7 top-level fields. The nested Envelope/Timestamp fields (event_id,
	// seconds, ...) must NOT appear — if they leak in, every comparison is
	// garbage and the guard is worse than useless.
	if got, want := len(msg.Fields), 7; got != want {
		t.Fatalf("top-level fields = %d, want %d: %+v", got, want, msg.Fields)
	}
	for _, leak := range []string{"event_id", "idempotency_key", "seconds", "nanos"} {
		for _, f := range msg.Fields {
			if f.Name == leak {
				t.Errorf("nested field %q leaked into top-level fields", leak)
			}
		}
	}

	// Kind classification drives wire-compatibility. enum is a varint, message
	// is length-delimited — conflating them would miss a real wire break.
	want := map[int32]schemaguard.Field{
		1:  {Number: 1, Name: "envelope", Kind: schemaguard.KindMessage},
		2:  {Number: 2, Name: "submission_id", Kind: schemaguard.KindScalar, Scalar: "string"},
		6:  {Number: 6, Name: "state", Kind: schemaguard.KindEnum},
		7:  {Number: 7, Name: "total_points_earned", Kind: schemaguard.KindScalar, Scalar: "float"},
		10: {Number: 10, Name: "passed", Kind: schemaguard.KindScalar, Scalar: "bool"},
		11: {Number: 11, Name: "graded_at", Kind: schemaguard.KindMessage},
		20: {Number: 20, Name: "imda_dimensions", Kind: schemaguard.KindScalar, Scalar: "string", Repeated: true},
	}
	for num, w := range want {
		got, ok := msg.Fields[num]
		if !ok {
			t.Errorf("field %d missing", num)
			continue
		}
		if got != w {
			t.Errorf("field %d = %+v, want %+v", num, got, w)
		}
	}
}

// A proto map field is the trap that makes a naive guard cry wolf. In the
// DESCRIPTOR a `map<string, string> x = 6` is a REPEATED MESSAGE field (of a
// synthetic nested XEntry type); in the registered schema TEXT it is spelled
// `map<...>`. A parser that does not understand map<> syntax records the field
// as absent from the registry and screams that 12 healthy schemas are broken.
// Both sides must land on the same representation.
func TestParseSchemaDefinition_MapFieldMatchesDescriptorShape(t *testing.T) {
	const def = `
syntax = "proto3";
package chora.governance.v1;
message EvidenceRecorded {
  message Timestamp {
    int64 seconds = 1;
  }
  string evidence_id = 2;
  map<string, string> additional_fields = 6;
  map<string, int32> counts_by_domain = 7;
}
`
	msg, err := schemaguard.ParseSchemaDefinition(def)
	if err != nil {
		t.Fatalf("ParseSchemaDefinition: %v", err)
	}
	if len(msg.Fields) != 3 {
		t.Fatalf("fields = %d, want 3 (a map field must not be dropped): %+v", len(msg.Fields), msg.Fields)
	}
	for _, num := range []int32{6, 7} {
		f, ok := msg.Fields[num]
		if !ok {
			t.Fatalf("map field %d was DROPPED by the parser — this is what produces false positives", num)
		}
		// Must equal how the descriptor sees it: repeated message.
		if f.Kind != schemaguard.KindMessage || !f.Repeated {
			t.Errorf("map field %d = %+v; want Kind=message Repeated=true (the descriptor's shape)", num, f)
		}
	}

	// And the round-trip: a descriptor-side map field must Compare() equal.
	repo := schemaguard.Message{
		Package: "chora.governance.v1", Name: "EvidenceRecorded",
		Fields: map[int32]schemaguard.Field{
			2: {Number: 2, Name: "evidence_id", Kind: schemaguard.KindScalar, Scalar: "string"},
			6: {Number: 6, Name: "additional_fields", Kind: schemaguard.KindMessage, Repeated: true},
			7: {Number: 7, Name: "counts_by_domain", Kind: schemaguard.KindMessage, Repeated: true},
		},
	}
	if d := schemaguard.Compare(repo, msg); d.HasDrift() {
		t.Fatalf("a map field must not register as drift against its own schema: %+v", d)
	}
}

// --- Compare: the four outcomes that matter ---------------------------------

func msgOf(fields ...schemaguard.Field) schemaguard.Message {
	m := schemaguard.Message{Package: "chora.delivery.v1", Name: "SubmissionGraded", Fields: map[int32]schemaguard.Field{}}
	for _, f := range fields {
		m.Fields[f.Number] = f
	}
	return m
}

var (
	fEnvelope = schemaguard.Field{Number: 1, Name: "envelope", Kind: schemaguard.KindMessage}
	fSubID    = schemaguard.Field{Number: 2, Name: "submission_id", Kind: schemaguard.KindScalar, Scalar: "string"}
	fHint     = schemaguard.Field{Number: 12, Name: "hint_count", Kind: schemaguard.KindScalar, Scalar: "int32"}
	fTitle    = schemaguard.Field{Number: 13, Name: "assessment_title", Kind: schemaguard.KindScalar, Scalar: "string"}
)

func TestCompare_Identical_NoDrift(t *testing.T) {
	d := schemaguard.Compare(msgOf(fEnvelope, fSubID), msgOf(fEnvelope, fSubID))
	if d.HasDrift() {
		t.Fatalf("identical messages reported drift: %+v", d)
	}
	if d.Fatal() {
		t.Fatal("identical messages reported Fatal()")
	}
}

// THE BUG. This is chora.delivery.submission.graded.v1 exactly as it failed in
// production: the repo proto carries hint_count (12); the committed revision
// stops at field 2. Every publish that POPULATES hint_count is rejected with a
// 400. Must be FATAL — this breaks the wire.
func TestCompare_RepoFieldMissingFromRegistry_IsFatal(t *testing.T) {
	repo := msgOf(fEnvelope, fSubID, fHint)
	committed := msgOf(fEnvelope, fSubID)

	d := schemaguard.Compare(repo, committed)

	if !d.Fatal() {
		t.Fatal("a repo field absent from the committed revision MUST be fatal — " +
			"Pub/Sub rejects the publish with HTTP 400")
	}
	if len(d.MissingFromRegistry) != 1 || d.MissingFromRegistry[0].Name != "hint_count" {
		t.Fatalf("MissingFromRegistry = %+v, want [hint_count]", d.MissingFromRegistry)
	}
	if len(d.MissingFromRepo) != 0 {
		t.Errorf("MissingFromRepo = %+v, want empty", d.MissingFromRepo)
	}
}

// The reverse direction: the registry was hand-edited out-of-band and carries a
// field the repo has never heard of (assessment_title = 13, real, today). It
// does NOT break publishing — proto3 omits absent fields — so it must be
// reported but must NOT be fatal. Conflating the two directions would either
// cry wolf or miss the real break.
func TestCompare_RegistryFieldMissingFromRepo_IsNotFatal(t *testing.T) {
	repo := msgOf(fEnvelope, fSubID)
	committed := msgOf(fEnvelope, fSubID, fTitle)

	d := schemaguard.Compare(repo, committed)

	if d.Fatal() {
		t.Fatal("a registry-only field does not break publishing — must not be fatal")
	}
	if !d.HasDrift() {
		t.Fatal("a registry-only field is still drift and must be reported")
	}
	if len(d.MissingFromRepo) != 1 || d.MissingFromRepo[0].Name != "assessment_title" {
		t.Fatalf("MissingFromRepo = %+v, want [assessment_title]", d.MissingFromRepo)
	}
}

func TestCompare_IncompatibleFieldChange_IsFatal(t *testing.T) {
	cases := []struct {
		name            string
		repo, committed schemaguard.Field
	}{
		{
			name:      "scalar type changed (string -> int32: wire break)",
			repo:      schemaguard.Field{Number: 2, Name: "submission_id", Kind: schemaguard.KindScalar, Scalar: "int32"},
			committed: fSubID,
		},
		{
			name:      "cardinality changed (singular -> repeated)",
			repo:      schemaguard.Field{Number: 2, Name: "submission_id", Kind: schemaguard.KindScalar, Scalar: "string", Repeated: true},
			committed: fSubID,
		},
		{
			name:      "kind changed (enum -> message: varint vs length-delimited)",
			repo:      schemaguard.Field{Number: 2, Name: "submission_id", Kind: schemaguard.KindMessage},
			committed: fSubID,
		},
		{
			name:      "field renamed on the same number",
			repo:      schemaguard.Field{Number: 2, Name: "submission_uuid", Kind: schemaguard.KindScalar, Scalar: "string"},
			committed: fSubID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := schemaguard.Compare(msgOf(fEnvelope, tc.repo), msgOf(fEnvelope, tc.committed))
			if !d.Fatal() {
				t.Fatalf("%s must be fatal, got %+v", tc.name, d)
			}
			if len(d.Incompatible) != 1 {
				t.Fatalf("Incompatible = %+v, want 1 entry", d.Incompatible)
			}
		})
	}
}

// The remediation must be actionable and correct — an operator pastes this.
// `terraform apply` is NOT the answer here (standing rule: never full-apply;
// Pub/Sub is provisioned OOB), so the guard must emit the gcloud form.
func TestDrift_RemediationIsTheOOBGcloudCommit(t *testing.T) {
	d := schemaguard.Compare(msgOf(fEnvelope, fSubID, fHint), msgOf(fEnvelope, fSubID))
	d.SchemaName = "chora-delivery-submission-graded-v1"
	d.FlatProtoPath = "chora-contracts/proto/events-flat/delivery/submission/graded.proto"

	got := d.Remediation()
	for _, want := range []string{
		"gcloud pubsub schemas commit",
		"chora-delivery-submission-graded-v1",
		"--type=protocol-buffer",
		"chora-contracts/proto/events-flat/delivery/submission/graded.proto",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Remediation() missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "terraform apply") {
		t.Error("Remediation() must NOT tell anyone to run terraform apply")
	}
}

// --- Descriptor side: derive from the GENERATED descriptor, never a hand list -

func TestMessagesFromFDS_DerivesFieldsFromDescriptor(t *testing.T) {
	fds := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("events/delivery/assessment.proto"),
			Package: proto.String("chora.delivery.v1"),
			Syntax:  proto.String("proto3"),
			MessageType: []*descriptorpb.DescriptorProto{{
				Name: proto.String("SubmissionGraded"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name: proto.String("envelope"), Number: proto.Int32(1),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".chora.common.v1.EventEnvelope"),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					},
					{
						Name: proto.String("hint_count"), Number: proto.Int32(12),
						Type:  descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
						Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					},
					{
						Name: proto.String("imda_dimensions"), Number: proto.Int32(20),
						Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						Label: descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
					},
				},
			}},
		}},
	}

	got, err := schemaguard.MessagesFromFDS(fds)
	if err != nil {
		t.Fatalf("MessagesFromFDS: %v", err)
	}
	msg, ok := got["chora.delivery.v1.SubmissionGraded"]
	if !ok {
		t.Fatalf("SubmissionGraded not indexed; got keys %v", keys(got))
	}
	if len(msg.Fields) != 3 {
		t.Fatalf("fields = %d, want 3", len(msg.Fields))
	}
	if f := msg.Fields[1]; f.Kind != schemaguard.KindMessage {
		t.Errorf("field 1 kind = %v, want message", f.Kind)
	}
	if f := msg.Fields[12]; f.Kind != schemaguard.KindScalar || f.Scalar != "int32" {
		t.Errorf("field 12 = %+v, want scalar int32", f)
	}
	if f := msg.Fields[20]; !f.Repeated || f.Scalar != "string" {
		t.Errorf("field 20 = %+v, want repeated string", f)
	}
}

// A guard that silently indexes nothing is worse than no guard: it goes green
// forever. Both sides must refuse to be vacuous.
func TestVacuityRefused(t *testing.T) {
	if _, err := schemaguard.MessagesFromFDS(&descriptorpb.FileDescriptorSet{}); err == nil {
		t.Error("an empty FileDescriptorSet must be an error, not a vacuously-green index")
	}
	if _, err := schemaguard.ParseSchemaDefinition("syntax = \"proto3\";\npackage x.y;\n"); err == nil {
		t.Error("a definition with no top-level message must be an error")
	}
}

func keys(m map[string]schemaguard.Message) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- ruling 44: nested-scope drift, with controls in both directions ---------

const committedWeaknessAnalyzed = `
syntax = "proto3";
package chora.consumption.v1;
message WeaknessAnalyzed {
  message Envelope {
    string event_id = 1;
  }
  message OutputSelection {
    bool focused_dose      = 1;
    bool familiar_coaching = 2;
    bool practice_test     = 3;
  }
  Envelope envelope = 1;
  string upload_id = 2;
  OutputSelection output_selection = 10;
}
`

// A nested rename at a fixed tag. Pub/Sub refuses this exactly as it refuses a
// top-level one; the guard used to be blind to it.
const repoWeaknessNestedRename = `
syntax = "proto3";
package chora.consumption.v1;
message WeaknessAnalyzed {
  message Envelope {
    string event_id = 1;
  }
  message OutputSelection {
    bool focused_dose       = 1;
    bool companion_coaching = 2;
    bool practice_test      = 3;
  }
  Envelope envelope = 1;
  string upload_id = 2;
  OutputSelection output_selection = 10;
}
`

// A top-level rename at a fixed tag: the case the guard already caught. Kept as
// the other half of the control pair.
const repoWeaknessTopLevelRename = `
syntax = "proto3";
package chora.consumption.v1;
message WeaknessAnalyzed {
  message Envelope {
    string event_id = 1;
  }
  message OutputSelection {
    bool focused_dose      = 1;
    bool familiar_coaching = 2;
    bool practice_test     = 3;
  }
  Envelope envelope = 1;
  string upload_ref = 2;
  OutputSelection output_selection = 10;
}
`

func mustParse(t *testing.T, def string) schemaguard.Message {
	t.Helper()
	m, err := schemaguard.ParseSchemaDefinition(def)
	if err != nil {
		t.Fatalf("ParseSchemaDefinition: %v", err)
	}
	return m
}

func TestCompare_FiresOnANestedSameTagRename(t *testing.T) {
	committed := mustParse(t, committedWeaknessAnalyzed)
	repo := mustParse(t, repoWeaknessNestedRename)

	// The nested scope must actually have been parsed, or the assertion below
	// would pass by comparing nothing.
	if got := len(repo.Nested["OutputSelection"]); got != 3 {
		t.Fatalf("positive control: OutputSelection parsed with %d fields, want 3", got)
	}

	d := schemaguard.Compare(repo, committed)
	if !d.Fatal() {
		t.Fatal("a nested same-tag rename must be FATAL: Pub/Sub refuses the revision")
	}
	var found bool
	for _, c := range d.Incompatible {
		if c.Scope == "OutputSelection" && c.Number == 2 &&
			c.Repo.Name == "companion_coaching" && c.Committed.Name == "familiar_coaching" {
			found = true
		}
	}
	if !found {
		t.Errorf("the nested rename was not reported with its scope: %+v", d.Incompatible)
	}
}

func TestCompare_FiresOnATopLevelSameTagRename(t *testing.T) {
	d := schemaguard.Compare(mustParse(t, repoWeaknessTopLevelRename), mustParse(t, committedWeaknessAnalyzed))
	if !d.Fatal() {
		t.Fatal("a top-level same-tag rename must be FATAL")
	}
	for _, c := range d.Incompatible {
		if c.Number == 2 && c.Scope == "" && c.Repo.Name == "upload_ref" {
			return
		}
	}
	t.Errorf("top-level rename not reported: %+v", d.Incompatible)
}

// NEGATIVE CONTROL. A frozen generation that AGREES with its live schema must
// stay silent. Without this, a comparison that fired on everything would pass
// both tests above and still be useless.
func TestCompare_DoesNotFireWhenTheFrozenWireAgrees(t *testing.T) {
	const frozen = `
syntax = "proto3";
package chora.consumption.v1;
message RitualPublished {
  message Envelope {
    string event_id = 1;
  }
  Envelope envelope = 1;
  string tenant_id = 2;
  string familiar_id = 3;
}
`
	d := schemaguard.Compare(mustParse(t, frozen), mustParse(t, frozen))
	if d.HasDrift() {
		t.Errorf("a frozen wire matching its live schema must report NO drift, got %+v", d)
	}
}

// The frozen familiar_id at tag 3 must not read as a break just because the
// CURRENT generation spells it companion_id: those are two different schemas.
func TestCompare_FrozenAndCurrentGenerationsAreDifferentSchemas(t *testing.T) {
	const frozenRitual = `
syntax = "proto3";
package chora.consumption.v1;
message RitualPublished {
  message Envelope { string event_id = 1; }
  Envelope envelope = 1;
  string familiar_id = 3;
}
`
	const currentRitual = `
syntax = "proto3";
package chora.consumption.v1;
message RitualPublished {
  message Envelope { string event_id = 1; }
  Envelope envelope = 1;
  string companion_id = 3;
}
`
	// Joined correctly (frozen against the familiar-named schema), silent:
	if d := schemaguard.Compare(mustParse(t, frozenRitual), mustParse(t, frozenRitual)); d.HasDrift() {
		t.Errorf("frozen vs its own live schema must be silent, got %+v", d)
	}
	// Joined by message FULL NAME instead of by schema, the guard invents a
	// break. This is the false positive the schema-name join exists to prevent.
	if d := schemaguard.Compare(mustParse(t, currentRitual), mustParse(t, frozenRitual)); !d.Fatal() {
		t.Fatal("control: joining the CURRENT generation to the FROZEN schema should look like a break; if it does not, this test proves nothing about the join")
	}
}
