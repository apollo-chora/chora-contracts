// Tests for the REQUIRED-SET check (ruling 44).
//
// The flattener used to derive its output set from whatever source protos
// happened to exist. When a source was renamed (ADR-254) or deleted (the legacy
// duel protos), it silently stopped emitting flat protos that Terraform still
// resolves with file(), and because the wrapper wipes the tree first, running it
// DELETED live wire. That cost one total dev-plan outage (39 file() errors,
// hand-restored by 27d187ffb) and would have cost a second.
//
// The required set is therefore derived from the DECLARED TOPIC LIST, read from
// the Terraform files Terraform itself reads, and a declared topic with no
// emitted flat proto is a loud failure naming the topic.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/descriptorpb"
)

const m10Fixture = `
locals {
  pubsub_topics = {
    "chora.creation.atom.created.v1"   = { domain = "creation", aggregate = "atom", team = "1" }
    # a commented-out declaration must NOT be treated as declared
    # "chora.creation.atom.ghost.v1"   = { domain = "creation", aggregate = "atom", team = "1" }
    "chora.consumption.campaign.focus_assigned.v1" = { domain = "consumption", aggregate = "campaign", team = "1" }
    "chora.closure.requested.v1"       = { domain = "closure", aggregate = "saga", team = "3" }
    "chora.observability.agent_decision.logged.v1" = { domain = "observability", aggregate = "agent_decision", team = "3" }
    "chora.creation.question.generation_requested.v2" = { domain = "creation", aggregate = "question", team = "1" }
    "chora.delivery.enrollment.created.v1" = { domain = "delivery", aggregate = "enrollment", team = "2" }
  }

  atom_v2_topics = toset([
    "chora.creation.atom.created.v1",
  ])

  observability_decision_v2_topics = toset([
    "chora.observability.agent_decision.logged.v1",
  ])

  compose_v2_topics = toset([
    "chora.creation.question.generation_requested.v2",
  ])

  schemaless_topics = toset([
    "chora.delivery.enrollment.created.v1",
  ])
}

resource "google_pubsub_schema" "aggregate" {}
resource "google_pubsub_schema" "atom_v2" {}
resource "google_pubsub_schema" "observability_decision_v2" {}
resource "google_pubsub_schema" "compose_v2" {}
`

const estateFixture = `
locals {
  companion_topic_schema = {
    "chora.consumption.companion.bonded.v1" = { schema = "chora-consumption-companion-bonded-v1", encoding = "BINARY", proto = "consumption/companion/bonded.proto" }
    "chora.consumption.companion.hatched.v1" = { schema = "chora-consumption-companion-hatched-v1", encoding = "BINARY", proto = "consumption/companion/hatched.proto" }
  }
}

resource "google_pubsub_schema" "companion" {}
`

func TestParseHCLBlockKeys_SkipsCommentsAndNestedValues(t *testing.T) {
	got, err := parseHCLBlockKeys(m10Fixture, "pubsub_topics")
	if err != nil {
		t.Fatalf("parseHCLBlockKeys: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("want 6 declared topics, got %d: %v", len(got), got)
	}
	for _, k := range got {
		if strings.Contains(k, "ghost") {
			t.Errorf("commented-out declaration was parsed as declared: %q", k)
		}
		if k == "creation" || k == "atom" {
			t.Errorf("nested map VALUE parsed as a topic key: %q", k)
		}
	}
}

func TestParseHCLBlockKeys_TosetForm(t *testing.T) {
	got, err := parseHCLBlockKeys(m10Fixture, "atom_v2_topics")
	if err != nil {
		t.Fatalf("parseHCLBlockKeys: %v", err)
	}
	if len(got) != 1 || got[0] != "chora.creation.atom.created.v1" {
		t.Fatalf("toset form mis-parsed: %v", got)
	}
}

func TestParseHCLBlockKeys_MissingBlockIsLoud(t *testing.T) {
	if _, err := parseHCLBlockKeys(m10Fixture, "no_such_local"); err == nil {
		t.Fatal("a missing local must fail loud, not return an empty set that reads as 'nothing declared'")
	}
}

func TestAggregateFlatRel_MirrorsTerraformDerivation(t *testing.T) {
	cases := map[string]string{
		// 5 segments: domain / aggregate / event_type
		"chora.consumption.campaign.focus_assigned.v1": "consumption/campaign/focus_assigned.proto",
		// 4 segments: aggregate collapses to "saga", event_type is split[len-2]
		"chora.closure.requested.v1":                     "closure/saga/requested.proto",
		"chora.governance.observability_sink_failure.v1": "governance/saga/observability_sink_failure.proto",
	}
	for topic, want := range cases {
		if got := aggregateFlatRel(topic); got != want {
			t.Errorf("aggregateFlatRel(%q) = %q, want %q", topic, got, want)
		}
	}
}

func TestVersionedFlatRel_AppendsV2(t *testing.T) {
	if got, want := versionedFlatRel("chora.creation.atom.created.v1"), "creation/atom/created.v2.proto"; got != want {
		t.Errorf("versionedFlatRel = %q, want %q", got, want)
	}
	// compose v2 keys on the segment BEFORE the version, so a .v2 topic still
	// resolves to {event}.v2.proto and not {v2}.v2.proto.
	if got, want := versionedFlatRel("chora.creation.question.generation_requested.v2"), "creation/question/generation_requested.v2.proto"; got != want {
		t.Errorf("versionedFlatRel(compose) = %q, want %q", got, want)
	}
}

func TestRequiredFromTerraform_AppliesEveryExclusionAndResource(t *testing.T) {
	req, err := requiredFromTerraform(m10Fixture, estateFixture)
	if err != nil {
		t.Fatalf("requiredFromTerraform: %v", err)
	}
	byRel := map[string]requiredPath{}
	for _, r := range req {
		byRel[r.Rel] = r
	}

	// schemaless: declared but bound to NO schema, so it requires no flat proto.
	if _, ok := byRel["delivery/enrollment/created.proto"]; ok {
		t.Error("a schemaless topic must NOT be in the required set")
	}
	// atom_v2: excluded from the aggregate map, required as .v2.proto instead.
	if _, ok := byRel["creation/atom/created.proto"]; ok {
		t.Error("an atom_v2 topic must be excluded from the aggregate derivation")
	}
	if _, ok := byRel["creation/atom/created.v2.proto"]; !ok {
		t.Error("an atom_v2 topic must be required as {event}.v2.proto")
	}
	// observability_decision_v2 binds the UNSUFFIXED flat proto.
	if _, ok := byRel["observability/agent_decision/logged.proto"]; !ok {
		t.Error("observability_decision_v2 must require the unsuffixed flat proto")
	}
	// compose_v2 requires the .v2 flat.
	if _, ok := byRel["creation/question/generation_requested.v2.proto"]; !ok {
		t.Error("compose_v2 must require {event}.v2.proto")
	}
	// the plain aggregate case survives.
	if _, ok := byRel["consumption/campaign/focus_assigned.proto"]; !ok {
		t.Error("an ordinary declared topic must be required")
	}
	// the _root companion estate contributes its EXPLICIT proto paths.
	e, ok := byRel["consumption/companion/bonded.proto"]
	if !ok {
		t.Fatal("the _root companion estate's explicit proto list must be in the required set")
	}
	if e.Resource != "google_pubsub_schema.companion" {
		t.Errorf("estate requirement attributed to %q", e.Resource)
	}
}

func TestAssertKnownSchemaResources_RefusesAnUnrecognisedDerivation(t *testing.T) {
	// The required set MIRRORS Terraform's path derivations. A mirror can drift
	// from what it mirrors, and a narrowed guard that still reports success is
	// exactly the failure this unit exists to end. So a sixth resource must
	// break the run by name rather than be silently ignored.
	withSixth := m10Fixture + "\nresource \"google_pubsub_schema\" \"brand_new_thing\" {}\n"
	err := assertKnownSchemaResources(map[string]string{"m10.tf": withSixth, "estate.tf": estateFixture})
	if err == nil {
		t.Fatal("an unrecognised google_pubsub_schema resource must fail loud")
	}
	if !strings.Contains(err.Error(), "brand_new_thing") {
		t.Errorf("the error must NAME the unrecognised resource, got: %v", err)
	}
}

func TestAssertKnownSchemaResources_PassesOnTheKnownFive(t *testing.T) {
	if err := assertKnownSchemaResources(map[string]string{"m10.tf": m10Fixture, "estate.tf": estateFixture}); err != nil {
		t.Fatalf("the five known resources must pass: %v", err)
	}
}

func TestCheckRequired_FailsLoudNamingTheTopicAndTheResource(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFlat(t, dir, "consumption/campaign/focus_assigned.proto")
	req := []requiredPath{
		{Rel: "consumption/campaign/focus_assigned.proto", Topic: "chora.consumption.campaign.focus_assigned.v1", Resource: "google_pubsub_schema.aggregate"},
		{Rel: "sharing/duel/initiated.proto", Topic: "chora.sharing.duel.initiated.v1", Resource: "google_pubsub_schema.aggregate"},
	}
	err := checkRequired(dir, req)
	if err == nil {
		t.Fatal("a declared topic with no flat proto must fail loud")
	}
	for _, want := range []string{"chora.sharing.duel.initiated.v1", "sharing/duel/initiated.proto", "google_pubsub_schema.aggregate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "focus_assigned") {
		t.Errorf("a SATISFIED requirement must not be reported as missing: %v", err)
	}
}

func TestCheckRequired_PassesWhenEveryDeclaredTopicHasItsFlat(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFlat(t, dir, "consumption/campaign/focus_assigned.proto")
	req := []requiredPath{
		{Rel: "consumption/campaign/focus_assigned.proto", Topic: "chora.consumption.campaign.focus_assigned.v1", Resource: "google_pubsub_schema.aggregate"},
	}
	if err := checkRequired(dir, req); err != nil {
		t.Fatalf("a satisfied required set must pass: %v", err)
	}
}

func writeFixtureFlat(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("syntax = \"proto3\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRequiredSet_RealTerraformIsSatisfiedByCommittedTree is the acceptance
// guard in test form: it parses the REAL Terraform files that Terraform itself
// reads and asserts the committed flat tree satisfies every declared topic.
// A failure here is the dev root module failing `terraform plan` on file().
func TestRequiredSet_RealTerraformIsSatisfiedByCommittedTree(t *testing.T) {
	m10, estate := readRealTerraform(t)
	if err := assertKnownSchemaResources(map[string]string{m10Path: m10, estatePath: estate}); err != nil {
		t.Fatalf("unmirrored schema resource: %v", err)
	}
	req, err := requiredFromTerraform(m10, estate)
	if err != nil {
		t.Fatalf("requiredFromTerraform: %v", err)
	}
	if len(req) < 300 {
		// A parser that silently matched nothing would otherwise pass this test
		// by requiring nothing at all.
		t.Fatalf("required set is implausibly small (%d); the parser probably matched nothing", len(req))
	}
	if err := checkRequired(filepath.Join("..", "..", "proto", "events-flat"), req); err != nil {
		t.Fatalf("committed flat tree does not satisfy the declared topics:\n%v", err)
	}
}

const (
	m10Path    = "../../../chora-infra/terraform/modules/m10-data-plane/main.tf"
	estatePath = "../../../chora-infra/terraform/environments/_root/companion_topic_estate.tf"
)

func readRealTerraform(t *testing.T) (string, string) {
	t.Helper()
	m10, err := os.ReadFile(m10Path)
	if err != nil {
		t.Skipf("Terraform source not readable from this checkout: %v", err)
	}
	estate, err := os.ReadFile(estatePath)
	if err != nil {
		t.Skipf("companion estate not readable from this checkout: %v", err)
	}
	return string(m10), string(estate)
}

// TestGenTreeHasNoFrozenGenerationBindings pins the codegen exclusion (ruling 44).
//
// proto-frozen/ is FLATTEN-INPUT ONLY. `make codegen` runs `buf generate proto`,
// targeting the current module explicitly, so no Go or Python bindings are
// produced for a retired generation that nothing consumes. The exclusion is not
// merely tidiness: proto-frozen/consumption/ritual.proto and
// proto/events/consumption/ritual.proto BOTH declare RitualStepStamp, a helper
// the ADR-254 rename never touched, so the two trees cannot share a buf module
// at all ("RitualStepStamp declared multiple times"). That is why proto-frozen
// is a separate module rather than a subdirectory.
//
// Each symbol is positively controlled: it must be PRESENT in proto-frozen, or
// the absence check downstream would pass by looking for nothing.
func TestGenTreeHasNoFrozenGenerationBindings(t *testing.T) {
	frozenOnly := []string{"FamiliarBonded", "FamiliarHatched", "FamiliarEggRefunded", "DuelInitiated"}
	frozenSrc := readTree(t, filepath.Join("..", "..", "proto-frozen"))
	genTree := readTree(t, filepath.Join("..", "..", "gen"))

	for _, sym := range frozenOnly {
		// POSITIVE CONTROL: the symbol really is declared in the frozen tree.
		if !strings.Contains(frozenSrc, "message "+sym+" ") && !strings.Contains(frozenSrc, "message "+sym+"{") {
			t.Fatalf("positive control failed: %q is not declared in proto-frozen/, so the absence check below proves nothing", sym)
		}
		// THE ASSERTION: no binding for it reached gen/.
		if strings.Contains(genTree, sym) {
			t.Errorf("gen/ contains %q: proto-frozen leaked into codegen. `make codegen` must run `buf generate proto`, not `buf generate`.", sym)
		}
	}
}

// TestFrozenTreeIsASeparateBufModule pins the structural reason for the split.
func TestFrozenTreeIsASeparateBufModule(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "buf.yaml"))
	if err != nil {
		t.Skipf("buf.yaml unreadable: %v", err)
	}
	if !strings.Contains(string(b), "- path: proto-frozen") {
		t.Error("proto-frozen must be its own buf module; sharing proto/'s module breaks on duplicate helper symbols")
	}
	mk, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Skipf("Makefile unreadable: %v", err)
	}
	if !strings.Contains(string(mk), "buf generate proto\n") {
		t.Error("codegen must target the proto module explicitly (`buf generate proto`), or the frozen tree is generated too")
	}
}

func readTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		b.Write(data)
		return nil
	})
	if err != nil {
		t.Skipf("cannot read %s: %v", root, err)
	}
	return b.String()
}

// --- driving generate() and the check end to end over a fixture -------------
//
// main() was a ~200-line function no test could reach, which is how the
// required-set check could have shipped unexercised end to end. generate() is
// the same body, callable.

// fixtureFDS builds a minimal descriptor set: the envelope file plus one event
// file carrying two event messages, one of them frozen-style.
func fixtureFDS(t *testing.T) (*descriptorpb.FileDescriptorSet, map[string]bool) {
	t.Helper()
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING
	msgT := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	lblOpt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	sp := func(s string) *string { return &s }
	i32 := func(i int32) *int32 { return &i }

	envelope := &descriptorpb.FileDescriptorProto{
		Name:    sp(envelopeFile),
		Package: sp("chora.common.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: sp("EventEnvelope"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: sp("event_id"), Number: i32(1), Type: &str, Label: &lblOpt},
			},
		}},
	}
	event := func(fileName, pkg, msgName, topic string) *descriptorpb.FileDescriptorProto {
		return &descriptorpb.FileDescriptorProto{
			Name:       sp(fileName),
			Package:    sp(pkg),
			Dependency: []string{envelopeFile},
			MessageType: []*descriptorpb.DescriptorProto{{
				Name: sp(msgName),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: sp("envelope"), Number: i32(1), Type: &msgT, Label: &lblOpt, TypeName: sp(".chora.common.v1.EventEnvelope")},
					{Name: sp("familiar_id"), Number: i32(2), Type: &str, Label: &lblOpt},
				},
			}},
			SourceCodeInfo: &descriptorpb.SourceCodeInfo{
				Location: []*descriptorpb.SourceCodeInfo_Location{{
					Path:            []int32{4, 0},
					Span:            []int32{0, 0, 1},
					LeadingComments: sp(" Topic: " + topic + "\n"),
				}},
			},
		}
	}
	fds := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		envelope,
		event("events/consumption/campaign.proto", "chora.consumption.v1", "CampaignFocusAssigned", "chora.consumption.campaign.focus_assigned.v1"),
		event("consumption/familiar.proto", "chora.consumption.v1", "FamiliarBonded", "chora.consumption.familiar.bonded.v1"),
	}}
	return fds, map[string]bool{"consumption/familiar.proto": true}
}

func TestGenerate_EmitsCurrentAndFrozenSources(t *testing.T) {
	fds, frozen := fixtureFDS(t)
	out := t.TempDir()
	var stdout, stderr strings.Builder

	files, msgs, err := generate(fds, frozen, out, true, &stdout, &stderr)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if files != 2 || msgs != 2 {
		t.Errorf("generate walked %d files / %d messages, want 2 / 2", files, msgs)
	}
	for _, rel := range []string{"consumption/campaign/focus_assigned.proto", "consumption/familiar/bonded.proto"} {
		if _, err := os.Stat(filepath.Join(out, rel)); err != nil {
			t.Errorf("expected %s to be emitted: %v", rel, err)
		}
	}
	// A frozen source must present its path as events/-prefixed, because that is
	// what the LIVE schema definition already carries. Getting this wrong is a
	// definition diff on every plan.
	b, err := os.ReadFile(filepath.Join(out, "consumption/familiar/bonded.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "// Source : events/consumption/familiar.proto message FamiliarBonded") {
		t.Errorf("frozen header not events/-prefixed:\n%s", firstLines(string(b), 6))
	}
	// POSITIVE CONTROL on the control: the current-generation file keeps its own path.
	c, err := os.ReadFile(filepath.Join(out, "consumption/campaign/focus_assigned.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c), "// Source : events/consumption/campaign.proto message CampaignFocusAssigned") {
		t.Errorf("current-generation header wrong:\n%s", firstLines(string(c), 6))
	}
}

func TestGenerate_FailsLoudWhenTheEnvelopeIsMissing(t *testing.T) {
	fds, frozen := fixtureFDS(t)
	fds.File = fds.File[1:] // drop the envelope
	var stdout, stderr strings.Builder
	if _, _, err := generate(fds, frozen, t.TempDir(), true, &stdout, &stderr); err == nil {
		t.Fatal("a descriptor set with no envelope must fail loud, not emit half a tree")
	}
}

// runRequiredCheck is the fail-loud path the wrapper invokes after staging.
func TestRunRequiredCheck_FailsLoudOnADeclaredTopicWithNoSource(t *testing.T) {
	dir := t.TempDir()
	m10 := filepath.Join(dir, "m10.tf")
	estate := filepath.Join(dir, "estate.tf")
	if err := os.WriteFile(m10, []byte(m10Fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(estate, []byte(estateFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	// Empty tree: every declared topic is missing.
	err := runRequiredCheck(t.TempDir(), m10, estate)
	if err == nil {
		t.Fatal("an empty tree must fail the required-set check")
	}
	if !strings.Contains(err.Error(), "chora.consumption.campaign.focus_assigned.v1") {
		t.Errorf("the failure must NAME the declared topic, got: %v", err)
	}

	// Now satisfy every requirement and it must pass.
	full := t.TempDir()
	req, derr := requiredFromTerraform(m10Fixture, estateFixture)
	if derr != nil {
		t.Fatal(derr)
	}
	for _, r := range req {
		writeFixtureFlat(t, full, r.Rel)
	}
	if err := runRequiredCheck(full, m10, estate); err != nil {
		t.Fatalf("a satisfied tree must pass: %v", err)
	}
}

func TestRunRequiredCheck_FailsLoudOnAnUnreadableTerraformFile(t *testing.T) {
	if err := runRequiredCheck(t.TempDir(), filepath.Join(t.TempDir(), "absent.tf"), filepath.Join(t.TempDir(), "absent.tf")); err == nil {
		t.Fatal("an unreadable topic list must fail, never silently skip the check")
	}
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}
