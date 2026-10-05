// Tests for the protoflatten codegen tool (Path C v2 — nested-types pattern).
//
// The integration test lives at chora-contracts/tests/test_events_flat_up_to_date.sh
// (shell) — it re-runs the codegen and asserts no diff against the committed
// proto/events-flat/ tree. That's the contract the committed artifact depends
// on for determinism.
//
// This file covers the descriptor-walking logic in isolation (unit tests) and
// — most importantly — adds the CI guardrail
// `TestEventsFlat_SingleTopLevelMessage` which scans the entire committed
// proto/events-flat/ tree and asserts every .proto declares exactly one
// top-level message. The flat schema must carry a single top-level message
// (some registry consumers reject more than one — "Too many message types
// specified in schema definition"), so this is the regression gate that
// prevents Path C v1's flat-siblings layout from sneaking back in.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestRewriteTypeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// EventEnvelope is renamed to the local short name `Envelope` when
		// nested inside the per-event message body.
		{".chora.common.v1.EventEnvelope", "Envelope"},
		{".google.protobuf.Timestamp", "Timestamp"},
		{".chora.delivery.v1.BookingStatus", "BookingStatus"},
		{".chora.creation.v1.Atom.Status", "Status"},
		{"BareName", "BareName"},
	}
	for _, c := range cases {
		if got := rewriteTypeName(c.in); got != c.want {
			t.Errorf("rewriteTypeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPascalToSnake covers the message-name → event_type derivation helper.
func TestPascalToSnake(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Created", "created"},
		{"NodeTraversed", "node_traversed"},
		{"SkinEquipped", "skin_equipped"},
		{"GCIDIssued", "gcid_issued"},
		{"AGIDIssued", "agid_issued"},
	}
	for _, c := range cases {
		if got := pascalToSnake(c.in); got != c.want {
			t.Errorf("pascalToSnake(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMessageNameToEventType covers the aggregate-prefix-stripping logic
// across the variants documented in messageNameToEventType's docstring.
func TestMessageNameToEventType(t *testing.T) {
	cases := []struct {
		messageName, aggregate, want string
	}{
		{"BookingCreated", "booking", "created"},
		{"CompanionBonded", "companion", "bonded"},
		{"CompanionSkinEquipped", "companion", "skin_equipped"},
		{"PathEnrolled", "learning_path", "enrolled"},
		// Aggregate-prefix absent — fall through to whole-name snake_case.
		{"NodeTraversed", "knowledge_graph", "node_traversed"},
	}
	for _, c := range cases {
		if got := messageNameToEventType(c.messageName, c.aggregate); got != c.want {
			t.Errorf("messageNameToEventType(%q, %q) = %q, want %q", c.messageName, c.aggregate, got, c.want)
		}
	}
}

// TestSplitTopic_ReturnsVersion covers the version-aware enhancement (ADR-195
// WS7): splitTopic must return the topic's major version so the flat emitter
// can write {event_type}.v{N}.proto for N>=2 (v1 stays unsuffixed for
// back-compat with the existing committed flat tree). Without this, a v2 topic
// (chora.creation.question.generation_requested.v2) collides on the same
// {event_type}.proto path as v1 and one silently overwrites the other — the
// R3 hand-authored-flat-wiped foot-gun the cp-alias trick can't cover when the
// v2 payload genuinely differs from v1.
func TestSplitTopic_ReturnsVersion(t *testing.T) {
	cases := []struct {
		topic               string
		wantD, wantA, wantE string
		wantV               int
	}{
		{"chora.creation.question.generation_requested.v1", "creation", "question", "generation_requested", 1},
		{"chora.creation.question.generation_requested.v2", "creation", "question", "generation_requested", 2},
		{"chora.creation.ai_assist.started.v2", "creation", "ai_assist", "started", 2},
		// 4-component closure-saga shape carries a version too.
		{"chora.closure.requested.v1", "closure", "saga", "requested", 1},
		{"chora.closure.requested.v3", "closure", "saga", "requested", 3},
		// Non-conforming -> all zero/empty (version 0 signals "no topic").
		{"not.a.topic", "", "", "", 0},
		{"chora.creation.question.generation_requested.vX", "", "", "", 0},
	}
	for _, c := range cases {
		d, a, e, v := splitTopic(c.topic)
		if d != c.wantD || a != c.wantA || e != c.wantE || v != c.wantV {
			t.Errorf("splitTopic(%q) = (%q,%q,%q,%d), want (%q,%q,%q,%d)",
				c.topic, d, a, e, v, c.wantD, c.wantA, c.wantE, c.wantV)
		}
	}
}

// TestFlatFileName_VersionedSuffix covers the flat-filename helper: v1 (and the
// unversioned/heuristic default, version 0) stays {event_type}.proto so the
// existing committed flat tree is byte-identical; v2+ gets a
// {event_type}.v{N}.proto suffix so it lives ALONGSIDE v1 instead of
// overwriting it.
func TestFlatFileName_VersionedSuffix(t *testing.T) {
	cases := []struct {
		eventType string
		version   int
		want      string
	}{
		{"generation_requested", 1, "generation_requested.proto"},
		{"generation_requested", 2, "generation_requested.v2.proto"},
		{"started", 2, "started.v2.proto"},
		{"created", 3, "created.v3.proto"},
		// Defensive: version 0 (no topic header) behaves like v1 (unsuffixed).
		{"created", 0, "created.proto"},
	}
	for _, c := range cases {
		if got := flatFileName(c.eventType, c.version); got != c.want {
			t.Errorf("flatFileName(%q, %d) = %q, want %q", c.eventType, c.version, got, c.want)
		}
	}
}

// TestWriteFlatPreservesFieldNumbers ensures the rendered output keeps every
// field number identical to the source descriptor — critical for schema
// versioning resilience: subscribers rely on stable numbers across schema
// revisions.
func TestWriteFlatPreservesFieldNumbers(t *testing.T) {
	envelope := envelopeDescriptor()
	src, event := singleEventFile(
		"chora.test.v1",
		"events/test/sample.proto",
		"Sample",
		[]*descriptorpb.FieldDescriptorProto{
			envelopeField(),
			{
				Name:   proto.String("name"),
				Number: proto.Int32(42),
				Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			},
			{
				Name:     proto.String("created_at"),
				Number:   proto.Int32(7),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".google.protobuf.Timestamp"),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			},
		},
	)

	var buf bytes.Buffer
	if err := writeFlat(&buf, src, src.GetName(), event, envelope, map[string]typeRef{}); err != nil {
		t.Fatalf("writeFlat: %v", err)
	}
	got := buf.String()

	// Field numbers must be preserved exactly — schema evolution mandate.
	for _, want := range []string{
		"Envelope envelope = 1;",
		"string name = 42;",
		"Timestamp created_at = 7;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q\n--- output ---\n%s", want, got)
		}
	}

	// No imports allowed in flat output.
	if strings.Contains(got, "import \"") {
		t.Errorf("flat output must not contain `import` statements; got:\n%s", got)
	}
}

// TestWriteFlatHasNoImports asserts the generator emits no import statements
// regardless of source imports — the flat consumer rejects them.
func TestWriteFlatHasNoImports(t *testing.T) {
	envelope := envelopeDescriptor()
	src, event := singleEventFile(
		"chora.test.v1",
		"events/test/withimports.proto",
		"Empty",
		[]*descriptorpb.FieldDescriptorProto{envelopeField()},
	)
	src.Dependency = []string{"chora/common/v1/envelope.proto", "google/protobuf/timestamp.proto"}
	var buf bytes.Buffer
	if err := writeFlat(&buf, src, src.GetName(), event, envelope, map[string]typeRef{}); err != nil {
		t.Fatalf("writeFlat: %v", err)
	}
	if strings.Contains(buf.String(), "import \"") {
		t.Fatalf("flat output contained import: %s", buf.String())
	}
}

// TestWriteFlatNestsEnvelopeAndTimestamp asserts both well-known types are
// nested INSIDE the per-event top-level message — Path C v2 nested-types
// pattern. Verifies the indentation prefix to confirm nesting.
func TestWriteFlatNestsEnvelopeAndTimestamp(t *testing.T) {
	envelope := envelopeDescriptor()
	src, event := singleEventFile(
		"chora.test.v1",
		"events/test/sample.proto",
		"Empty",
		[]*descriptorpb.FieldDescriptorProto{envelopeField()},
	)
	var buf bytes.Buffer
	if err := writeFlat(&buf, src, src.GetName(), event, envelope, map[string]typeRef{}); err != nil {
		t.Fatalf("writeFlat: %v", err)
	}
	got := buf.String()

	// The nested types must sit at indent level 1 (two spaces) — i.e., children
	// of the top-level event message.
	for _, want := range []string{
		"  message Envelope {",
		"  message Timestamp {",
		"int64 seconds = 1;",
		"int32 nanos = 2;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q (envelope/timestamp nesting)\n--- output ---\n%s", want, got)
		}
	}

	// Negative assertion: NO top-level Envelope / Timestamp / EventEnvelope
	// declaration. Path C v1 emitted siblings; v2 nests them. A regression
	// to v1 layout is what the single-top-level constraint forbids.
	for _, forbid := range []string{
		"\nmessage EventEnvelope {",
		"\nmessage Envelope {",
		"\nmessage Timestamp {",
	} {
		if strings.Contains(got, forbid) {
			t.Errorf("regression: flat output has top-level %q (Path C v1 layout); v2 requires nesting\n--- output ---\n%s", strings.TrimSpace(forbid), got)
		}
	}
}

// TestWriteFlatRendersEnums covers enum emission as a nested type (same-package
// enum referenced by the event message).
func TestWriteFlatRendersEnums(t *testing.T) {
	envelope := envelopeDescriptor()
	src := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("events/test/withenum.proto"),
		Package: proto.String("chora.test.v1"),
		EnumType: []*descriptorpb.EnumDescriptorProto{
			{
				Name: proto.String("Status"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					{Name: proto.String("STATUS_UNSPECIFIED"), Number: proto.Int32(0)},
					{Name: proto.String("STATUS_ACTIVE"), Number: proto.Int32(1)},
				},
			},
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Sample"),
				Field: []*descriptorpb.FieldDescriptorProto{
					envelopeField(),
					{
						Name:     proto.String("status"),
						Number:   proto.Int32(2),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
						TypeName: proto.String(".chora.test.v1.Status"),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					},
				},
			},
		},
	}
	idx := buildTypeIndex(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{src}})
	event := src.MessageType[0]

	var buf bytes.Buffer
	if err := writeFlat(&buf, src, src.GetName(), event, envelope, idx); err != nil {
		t.Fatalf("writeFlat: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"enum Status {",
		"STATUS_UNSPECIFIED = 0;",
		"STATUS_ACTIVE = 1;",
		"Status status = 2;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestWriteFlatSingleTopLevelMessage asserts the generator emits exactly ONE
// `message Foo {` declaration at column 0 — the single-top-level-message
// constraint that drove the Path C v2 refactor.
func TestWriteFlatSingleTopLevelMessage(t *testing.T) {
	envelope := envelopeDescriptor()
	src, event := singleEventFile(
		"chora.test.v1",
		"events/test/sample.proto",
		"Created",
		[]*descriptorpb.FieldDescriptorProto{envelopeField()},
	)
	var buf bytes.Buffer
	if err := writeFlat(&buf, src, src.GetName(), event, envelope, map[string]typeRef{}); err != nil {
		t.Fatalf("writeFlat: %v", err)
	}
	if got := countTopLevelMessages(buf.String()); got != 1 {
		t.Fatalf("flat output must declare exactly one top-level message; got %d\n--- output ---\n%s", got, buf.String())
	}
}

// =============================================================================
// CI guardrail (the v2 lock).
// =============================================================================

// TestEventsFlat_SingleTopLevelMessage walks the committed events-flat tree
// and asserts every .proto declares exactly ONE top-level message. This is
// the regression gate against Path C v1 (multi-top-level-message siblings),
// which the single-top-level constraint forbids.
//
// Resilience note (per feedback_resilience_priority): runs as a unit test so
// CI catches the violation BEFORE the flat consumer sees it. Faster failure =
// lower MTTR.
func TestEventsFlat_SingleTopLevelMessage(t *testing.T) {
	root := findEventsFlatDir(t)
	walked := 0

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		count := countTopLevelMessages(string(raw))
		if count != 1 {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s: expected exactly 1 top-level message, got %d (the flat schema forbids multi-top-level-message schemas)", rel, count)
		}
		walked++
		return nil
	})
	if err != nil {
		t.Fatalf("walking events-flat: %v", err)
	}
	if walked == 0 {
		t.Fatalf("events-flat tree at %s contained zero .proto files; tree missing or generator regressed", root)
	}
	t.Logf("scanned %d events-flat .proto files; all have exactly 1 top-level message", walked)
}

// TestEventsFlat_NoImports asserts the committed events-flat tree contains
// zero `import` statements — the flat consumer does NOT resolve imports.
func TestEventsFlat_NoImports(t *testing.T) {
	root := findEventsFlatDir(t)
	importRe := regexp.MustCompile(`(?m)^\s*import\s+"`)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if importRe.MatchString(string(raw)) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s: contains `import` statement (the flat consumer doesn't resolve imports)", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking events-flat: %v", err)
	}
}

// =============================================================================
// Helpers
// =============================================================================

// topLevelMessageRe matches a `message Name {` declaration anchored at the
// start of a line (column 0). Nested messages are indented and therefore
// excluded.
var topLevelMessageRe = regexp.MustCompile(`(?m)^message\s+\w+\s*\{`)

// countTopLevelMessages returns the number of column-0 `message Foo {`
// declarations in a .proto source.
func countTopLevelMessages(src string) int {
	return len(topLevelMessageRe.FindAllString(src, -1))
}

// findEventsFlatDir locates the committed events-flat tree relative to the
// test binary's working directory (chora-contracts/internal/protoflatten/).
func findEventsFlatDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	// Walk up from `chora-contracts/internal/protoflatten/` to find
	// `chora-contracts/proto/events-flat/`. Two levels up is chora-contracts.
	candidates := []string{
		filepath.Join(wd, "..", "..", "proto", "events-flat"),
		filepath.Join(wd, "proto", "events-flat"), // fallback when run from chora-contracts/
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if info, err := os.Stat(abs); err == nil && info.IsDir() {
			return abs
		}
	}
	t.Fatalf("could not locate events-flat tree relative to %s", wd)
	return ""
}

// TestTopicCommentsByMessageIndex_PrefersCanonicalHeader covers the bug that
// silently mis-routed CompanionEggPaymentSucceeded to
// `consumption/companion/egg_purchased.proto` (overwriting the legitimate
// CompanionEggPurchased flat schema). The leading comment of
// CompanionEggPaymentSucceeded mentions a downstream sibling topic
// (chora.consumption.companion.egg_purchased.v1) BEFORE the canonical
// `// Topic: chora.tenancy.companion_egg.payment_succeeded.v1` header.
// The first-match-wins regex picked the sibling. Fix: prefer the
// `Topic: ` header when present.
//
// Regression gate: when a message's leading comment cross-references a
// SIBLING topic in prose, the canonical `Topic: ` header MUST win.
func TestTopicCommentsByMessageIndex_PrefersCanonicalHeader(t *testing.T) {
	cases := []struct {
		name    string
		leading string
		want    string
	}{
		{
			name:    "topic header beats earlier prose cross-reference",
			leading: " CompanionEggPaymentSucceeded — emitted on Stripe's checkout.session.completed\n webhook. chora-consumption subscribes to this and provisions the Stage-0\n Companion instance, then emits chora.consumption.companion.egg_purchased.v1.\n\n Topic: chora.tenancy.companion_egg.payment_succeeded.v1\n Consumers: ...\n",
			want:    "chora.tenancy.companion_egg.payment_succeeded.v1",
		},
		{
			name:    "topic header alone (no prose cross-ref)",
			leading: " CompanionEggCheckoutStarted — emitted when chora-tenancy creates a Stripe\n Checkout session for an egg purchase.\n\n Topic: chora.tenancy.companion_egg.checkout_started.v1\n",
			want:    "chora.tenancy.companion_egg.checkout_started.v1",
		},
		{
			name:    "form-2 header (em-dash) falls back to first match",
			leading: " CompanionBonded — chora.consumption.companion.bonded.v1\n Emitted when a learner bonds to a companion.\n",
			want:    "chora.consumption.companion.bonded.v1",
		},
		{
			name:    "no chora topic ref returns empty",
			leading: " Some random docstring without any topic reference.\n",
			want:    "",
		},
		{
			name:    "4-component closure-saga shape still resolves via header",
			leading: " ClosureRequested — saga lifecycle.\n\n Topic: chora.closure.requested.v1\n",
			want:    "chora.closure.requested.v1",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &descriptorpb.FileDescriptorProto{
				Name: proto.String("events/test/sample.proto"),
				SourceCodeInfo: &descriptorpb.SourceCodeInfo{
					Location: []*descriptorpb.SourceCodeInfo_Location{
						{
							// Top-level message at index 0: path = [4, 0].
							Path:            []int32{4, 0},
							LeadingComments: proto.String(c.leading),
						},
					},
				},
			}
			out := topicCommentsByMessageIndex(f)
			got := out[0]
			if got != c.want {
				t.Errorf("topicCommentsByMessageIndex[0] = %q, want %q", got, c.want)
			}
		})
	}
}

// envelopeField returns the canonical envelope field descriptor (field 1
// referencing chora.common.v1.EventEnvelope) — required for isEventMessage
// to recognise a message as an event.
func envelopeField() *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String("envelope"),
		Number:   proto.Int32(1),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(".chora.common.v1.EventEnvelope"),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
}

// singleEventFile builds a minimal source FileDescriptorProto with a single
// event message. Convenience for unit tests.
func singleEventFile(pkg, fileName, eventName string, fields []*descriptorpb.FieldDescriptorProto) (*descriptorpb.FileDescriptorProto, *descriptorpb.DescriptorProto) {
	event := &descriptorpb.DescriptorProto{
		Name:  proto.String(eventName),
		Field: fields,
	}
	src := &descriptorpb.FileDescriptorProto{
		Name:        proto.String(fileName),
		Package:     proto.String(pkg),
		MessageType: []*descriptorpb.DescriptorProto{event},
	}
	return src, event
}

// envelopeDescriptor returns a minimal EventEnvelope descriptor mirroring the
// real one's first 5 fields. Sufficient for unit tests.
func envelopeDescriptor() *descriptorpb.DescriptorProto {
	return &descriptorpb.DescriptorProto{
		Name: proto.String("EventEnvelope"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{Name: proto.String("event_id"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()},
			{Name: proto.String("idempotency_key"), Number: proto.Int32(2), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()},
			{Name: proto.String("tenant_id"), Number: proto.Int32(3), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()},
			{Name: proto.String("gcid"), Number: proto.Int32(4), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()},
			{
				Name:     proto.String("occurred_at"),
				Number:   proto.Int32(5),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".google.protobuf.Timestamp"),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			},
		},
	}
}
