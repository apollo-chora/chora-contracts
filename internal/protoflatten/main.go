// Command protoflatten emits self-contained per-topic .proto files — the
// flattened-schema artifact at proto/events-flat/.
//
// PROBLEM 1: consumers of the flat tree cannot resolve `import` statements.
// Our canonical event protos at proto/events/{domain}/{aggregate}.proto each
// `import "chora/common/v1/envelope.proto"` and
// `import "google/protobuf/timestamp.proto"`.
//
// PROBLEM 2: the flat schema must declare a single TOP-LEVEL message (some
// registry consumers reject definitions with more than one). Nested messages
// are accepted, so EventEnvelope + Timestamp are inlined as NESTED messages
// inside the single top-level event message.
//
// SOLUTION (Path C, locked 2026-05-10): generate one self-contained .proto
// per (domain, aggregate, event_type) tuple = one per event subject, at
// proto/events-flat/{domain}/{aggregate}.{event_type}.proto. Each file:
//   - Uses the same package name as the source (chora.{domain}.v1).
//   - Has exactly ONE top-level message, named after the event type
//     (e.g., BookingCreated).
//   - Has EventEnvelope + Timestamp NESTED inside that top-level message,
//     with `Envelope` + `Timestamp` as the local short names.
//   - Carries an inline-copy of every enum referenced by that message
//     (also as nested types).
//   - Drops every `import` statement and language-specific options
//     (go_package / python_package — the flat consumer doesn't read them).
//
// The source-of-truth at proto/events/ is unchanged. buf-driven Go/Python
// codegen still consumes proto/events/ directly. proto/events-flat/ is a
// committed generated artifact. The flatten step was introduced for a legacy
// managed schema registry (which rejected `import` statements); the platform
// now uses NATS JetStream, and the artifact is retained broker-neutral.
//
// Approach: read a FileDescriptorSet built by `buf build --as-file-descriptor-set`
// covering all of proto/, then walk every event-domain file's top-level
// messages. For each message that has `envelope` as field 1 referencing
// chora.common.v1.EventEnvelope, emit a self-contained one-message proto.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	envelopeFile      = "chora/common/v1/envelope.proto"
	envelopeFullName  = ".chora.common.v1.EventEnvelope"
	envelopeShortName = "Envelope" // inlined nested name (avoid clash with sibling event types in consumer code only — the flat file is self-contained so collisions don't matter)
	wktTimestampFull  = ".google.protobuf.Timestamp"
	wktTimestampShort = "Timestamp"
	eventsRootPrefix  = "events/"
	flatRootDir       = "events-flat"
)

// timestampMessageBody is rendered as the body of a NESTED Timestamp message
// inside the per-event top-level message. Wire-compatible with
// google.protobuf.Timestamp because field numbers + types match.
//
// Lines are joined with "\n" and emitted with a per-line indent (added at
// render time), so each line here is unindented.
const timestampMessageBody = `int64 seconds = 1;
int32 nanos = 2;`

func main() {
	var (
		fdsPath = flag.String("fds", "", "Path to buf-built FileDescriptorSet (binpb)")
		outDir  = flag.String("out", "proto/events-flat", "Output root directory for flattened protos")
		quiet   = flag.Bool("quiet", false, "Suppress per-file output")
		// Frozen-generation sources (ruling 44): event generations whose current
		// source was renamed or deleted while their topics stayed declared and
		// LIVE. They are a SEPARATE buf module because a frozen file can
		// redeclare a helper the rename never touched (RitualStepStamp), which
		// one module cannot hold twice. FLATTEN-INPUT ONLY: `buf generate`
		// targets the proto module explicitly, so no bindings come from here.
		fdsFrozen = flag.String("fds-frozen", "", "Path to the FileDescriptorSet built from proto-frozen (optional)")
	)
	flag.Parse()

	if *fdsPath == "" {
		fatalf("missing -fds (path to FileDescriptorSet from `buf build --as-file-descriptor-set`)")
	}

	raw, err := os.ReadFile(*fdsPath)
	if err != nil {
		fatalf("read FileDescriptorSet %s: %v", *fdsPath, err)
	}

	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &fds); err != nil {
		fatalf("unmarshal FileDescriptorSet: %v", err)
	}

	// Merge the frozen module's descriptor set, remembering which files came
	// from it. Names never collide with the current module's ("consumption/
	// ritual.proto" vs "events/consumption/ritual.proto").
	frozenFiles := map[string]bool{}
	if *fdsFrozen != "" {
		frozenRaw, err := os.ReadFile(*fdsFrozen)
		if err != nil {
			fatalf("read frozen FileDescriptorSet %s: %v", *fdsFrozen, err)
		}
		var frozenFDS descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(frozenRaw, &frozenFDS); err != nil {
			fatalf("unmarshal frozen FileDescriptorSet: %v", err)
		}
		existing := map[string]bool{}
		for _, f := range fds.GetFile() {
			existing[f.GetName()] = true
		}
		for _, f := range frozenFDS.GetFile() {
			if existing[f.GetName()] {
				continue // shared dep (envelope, timestamp) already present
			}
			frozenFiles[f.GetName()] = true
			fds.File = append(fds.File, f)
		}
	}

	if _, _, err := generate(&fds, frozenFiles, *outDir, *quiet, os.Stdout, os.Stderr); err != nil {
		fatalf("%v", err)
	}
}

// generate walks the descriptor set and emits one self-contained flat proto per
// event message. Split out of main so it can be driven directly by a test:
// main() is otherwise a 200-line function that no test could reach, which is
// how a generator can ship unexercised end to end.
func generate(fds *descriptorpb.FileDescriptorSet, frozenFiles map[string]bool, outDir string, quiet bool, stdout, stderr io.Writer) (totalFiles, totalMessages int, err error) {
	envelope := findFile(fds, envelopeFile)
	if envelope == nil {
		return totalFiles, totalMessages, fmt.Errorf("envelope file %s not found in descriptor set", envelopeFile)
	}
	envelopeMsg := findMessage(envelope, "EventEnvelope")
	if envelopeMsg == nil {
		return totalFiles, totalMessages, fmt.Errorf("EventEnvelope message not found in %s", envelopeFile)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return totalFiles, totalMessages, fmt.Errorf("mkdir %s: %v", outDir, err)
	}

	// Build a global type-name -> descriptor index for cross-file message
	// resolution (rare but exists — e.g., one event message references a
	// shared enum/message defined in the same package but a different file).
	// In practice today, every reference resolves within the same file or
	// to the EventEnvelope/Timestamp WKT.
	typeIndex := buildTypeIndex(fds)

	for _, f := range fds.GetFile() {
		name := f.GetName()
		// Eligible source files are per-aggregate event protos under
		// proto/events/{domain}/{aggregate}.proto plus the relocated
		// knowledge_graph proto under proto/chora/consumption/v1/knowledge_graph.proto.
		var domain, aggregate string
		switch {
		case frozenFiles[name]:
			// proto-frozen module: "consumption/familiar.proto" -> "consumption", "familiar".
			// Keyed on the descriptor set the file came from, never on its path,
			// so a frozen file never has to be spelled differently to be found.
			rel := strings.TrimSuffix(name, ".proto")
			parts := strings.SplitN(rel, "/", 2)
			if len(parts) != 2 {
				continue
			}
			domain = parts[0]
			aggregate = parts[1]
		case strings.HasPrefix(name, eventsRootPrefix):
			// "events/delivery/booking.proto" -> domain "delivery", aggregate "booking"
			rel := strings.TrimPrefix(name, eventsRootPrefix)
			rel = strings.TrimSuffix(rel, ".proto")
			parts := strings.SplitN(rel, "/", 2)
			if len(parts) != 2 {
				continue
			}
			domain = parts[0]
			aggregate = parts[1]
		case name == "chora/consumption/v1/knowledge_graph.proto":
			domain = "consumption"
			aggregate = "knowledge_graph"
		default:
			continue
		}

		totalFiles++

		// Build a per-message-index → topic comment lookup once per file via
		// SourceCodeInfo. Every event message in the source-of-truth proto
		// has a `// Topic: chora.{domain}.{aggregate}.{event_type}.v{N}`
		// header (or the equivalent em-dash form); that's the authoritative
		// (domain, aggregate, event_type) tuple. The message-name heuristic
		// (messageNameToEventType) is a fallback only.
		topicByMsgIdx := topicCommentsByMessageIndex(f)

		// For each top-level message in the source file, check if it's an
		// event message (has `envelope` field 1 referencing EventEnvelope).
		// If yes, emit a one-message flat proto for it.
		for msgIdx, m := range f.GetMessageType() {
			if !isEventMessage(m) {
				continue
			}

			// Resolve (domain, aggregate, event_type):
			//   1. Topic comment (authoritative) — overrides file-derived
			//      domain/aggregate to handle the case where one source
			//      proto carries events for SIBLING aggregates (e.g.,
			//      consumption/knowledge_graph.proto carries events for
			//      kg_map_cluster, kg_exploration, kg_hexagon_fog,
			//      kg_junction, plus knowledge_graph itself).
			//   2. Heuristic fallback from message name + file-derived
			//      aggregate (for protos without topic comments).
			outDomain := domain
			outAggregate := aggregate
			var eventType string
			// Major version of the topic. Defaults to 1 (the heuristic /
			// header-less path = a legacy v1 proto). The version-aware flat
			// filename (flatFileName) keeps v1 unsuffixed so the committed flat
			// tree is byte-identical, and suffixes v2+ so distinct majors never
			// overwrite one another (ADR-195 WS7).
			version := 1
			if topic, ok := topicByMsgIdx[int32(msgIdx)]; ok {
				if td, ta, te, ver := splitTopic(topic); te != "" {
					outDomain = td
					outAggregate = ta
					eventType = te
					version = ver
				}
			}
			if eventType == "" {
				eventType = messageNameToEventType(m.GetName(), aggregate)
			}
			if eventType == "" {
				fmt.Fprintf(stderr, "protoflatten: warning: cannot derive event_type from %s.%s; skipping\n", f.GetPackage(), m.GetName())
				continue
			}

			flatPath := filepath.Join(outDir, outDomain, outAggregate, flatFileName(eventType, version))
			if err := os.MkdirAll(filepath.Dir(flatPath), 0o755); err != nil {
				return totalFiles, totalMessages, fmt.Errorf("mkdir %s: %v", filepath.Dir(flatPath), err)
			}

			var buf bytes.Buffer
			sourceLabel := name
			if frozenFiles[name] {
				sourceLabel = eventsRootPrefix + name
			}
			if err := writeFlat(&buf, f, sourceLabel, m, envelopeMsg, typeIndex); err != nil {
				return totalFiles, totalMessages, fmt.Errorf("flatten %s.%s: %v", name, m.GetName(), err)
			}

			if err := os.WriteFile(flatPath, buf.Bytes(), 0o644); err != nil {
				return totalFiles, totalMessages, fmt.Errorf("write %s: %v", flatPath, err)
			}
			if !quiet {
				fmt.Fprintf(stdout, "wrote %s (%d bytes)\n", flatPath, buf.Len())
			}
			totalMessages++
		}
	}

	fmt.Fprintf(stdout, "protoflatten: emitted %d self-contained per-topic protos from %d source files under %s\n", totalMessages, totalFiles, outDir)
	return totalFiles, totalMessages, nil
}

// topicCommentRe matches any reference to a chora topic name. Two shapes
// supported:
//
//   - 5-component canonical: chora.{domain}.{aggregate}.{event_type}.v{N}
//     (e.g., chora.consumption.companion.bonded.v1)
//   - 4-component anomaly:   chora.{domain}.{event_type}.v{N}
//     (e.g., chora.closure.requested.v1 — closure saga omits aggregate
//     because the domain itself IS the saga aggregate; per
//     project_chora_concerns.md the closure orchestrator owns chora.closure.*).
//
// Two header conventions exist in the source-of-truth proto tree:
//  1. // Topic: chora.consumption.companion.bonded.v1
//  2. // CompanionBonded — chora.consumption.companion.bonded.v1
var topicCommentRe = regexp.MustCompile(`chora(?:\.[a-z0-9_]+){2,3}\.v[0-9]+`)

// topicHeaderRe matches the canonical `Topic: chora.x.y.z.vN` header
// (form 1). This is the authoritative per-message topic declaration. Prose
// elsewhere in the leading comment may cross-reference SIBLING topics
// (e.g., tenancy.companion_egg.payment_succeeded.proto's leading comment
// mentions `chora.consumption.companion.egg_purchased.v1` as a downstream
// effect) — picking the first generic topic match in that case silently
// routes the flat proto to the wrong path and overwrites the sibling
// schema. Always prefer the `Topic:` header when present.
var topicHeaderRe = regexp.MustCompile(`Topic:\s*(chora(?:\.[a-z0-9_]+){2,3}\.v[0-9]+)`)

// topicCommentsByMessageIndex returns a map of top-level message index →
// topic name, as parsed from the source proto's leading-comment SourceCodeInfo.
//
// Per descriptor-proto convention, top-level messages live at path [4, idx]
// and their leading_comments string carries the human-readable comment block
// preceding the message. We grep that block for chora.x.y.z.vN.
//
// Resolution order:
//  1. `Topic: chora.x.y.z.vN` canonical header — authoritative.
//  2. First generic `chora.x.y.z.vN` token — fallback for form-2 headers
//     (e.g., `// CompanionBonded — chora.consumption.companion.bonded.v1`).
//
// Files lacking SourceCodeInfo or topic comments return an empty map; the
// caller falls back to the heuristic name-derivation. This keeps the tool
// robust against partial source-info availability.
func topicCommentsByMessageIndex(f *descriptorpb.FileDescriptorProto) map[int32]string {
	out := map[int32]string{}
	info := f.GetSourceCodeInfo()
	if info == nil {
		return out
	}
	for _, loc := range info.GetLocation() {
		path := loc.GetPath()
		// Top-level message: path = [4, msg_idx].
		if len(path) != 2 || path[0] != 4 {
			continue
		}
		comment := loc.GetLeadingComments()
		if comment == "" {
			continue
		}
		// Prefer the canonical `Topic: chora.x.y.z.vN` header when present.
		// Otherwise fall back to the first generic topic-name token.
		if m := topicHeaderRe.FindStringSubmatch(comment); m != nil {
			out[path[1]] = m[1]
		} else if topic := topicCommentRe.FindString(comment); topic != "" {
			out[path[1]] = topic
		}
	}
	return out
}

// splitTopic decomposes a topic name into (domain, aggregate, event_type).
// Two shapes are recognised:
//
//   - chora.{domain}.{aggregate}.{event_type}.v{N}  — 5-component canonical
//   - chora.{domain}.{event_type}.v{N}              — 4-component (closure saga only;
//     aggregate is implicit / shared
//     with the domain).
//
// For the 4-component form, aggregate defaults to "saga" — matching the
// chora.closure.* saga convention (domain=closure, aggregate=saga).
//
// Returns zero values (and version 0) if the topic doesn't conform (defensive
// — caller falls back to heuristic). version is the parsed major from the
// trailing `.v{N}` segment (always >= 1 on a conforming topic).
func splitTopic(topic string) (domain, aggregate, eventType string, version int) {
	parts := strings.Split(topic, ".")
	if len(parts) < 4 || parts[0] != "chora" {
		return "", "", "", 0
	}
	last := parts[len(parts)-1]
	if !strings.HasPrefix(last, "v") {
		return "", "", "", 0
	}
	// Major version follows the leading 'v' (v1, v2, ...). A non-numeric or
	// non-positive suffix (e.g. "vX", "v0") is not a valid topic version.
	ver, err := strconv.Atoi(strings.TrimPrefix(last, "v"))
	if err != nil || ver < 1 {
		return "", "", "", 0
	}
	domain = parts[1]
	switch len(parts) {
	case 4:
		// chora.{domain}.{event_type}.v{N} — closure saga shape.
		aggregate = "saga"
		eventType = parts[2]
	case 5:
		// chora.{domain}.{aggregate}.{event_type}.v{N} — canonical.
		aggregate = parts[2]
		eventType = parts[3]
	default:
		return "", "", "", 0
	}
	// Sanity: only [a-z0-9_] allowed in event_type + aggregate.
	for _, segment := range []string{aggregate, eventType} {
		for _, r := range segment {
			if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
				return "", "", "", 0
			}
		}
	}
	return domain, aggregate, eventType, ver
}

// flatFileName returns the per-topic flat proto filename. v1 (and the
// unversioned heuristic default, version 0 or 1) keeps the bare
// {event_type}.proto name so the existing committed flat tree stays
// byte-identical across regenerations; v2+ gets a {event_type}.v{N}.proto
// suffix so each major version is registered as its OWN immutable schema
// revision and they never overwrite one another (ADR-195 WS7).
// protoflatten emits the v2 flat directly from the v2 source message — the
// flatten-event-schemas.sh cp-alias trick only works when v2 wire bytes equal
// v1 (the atom case), which compose-v2 does NOT (it drops job_type and adds the
// compose trio).
func flatFileName(eventType string, version int) string {
	if version >= 2 {
		return fmt.Sprintf("%s.v%d.proto", eventType, version)
	}
	return eventType + ".proto"
}

// isEventMessage reports whether a message is a domain event (has
// `envelope` as field 1 referencing chora.common.v1.EventEnvelope).
func isEventMessage(m *descriptorpb.DescriptorProto) bool {
	for _, f := range m.GetField() {
		if f.GetNumber() == 1 && f.GetName() == "envelope" && f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE && f.GetTypeName() == envelopeFullName {
			return true
		}
	}
	return false
}

// messageNameToEventType reverses the convention used in proto/events/.
// Event messages are PascalCase; topic event_types are snake_case. Common
// shapes:
//
//   - "BookingCreated"        + aggregate "booking"        -> "created"
//   - "CompanionBonded"        + aggregate "companion"       -> "bonded"
//   - "PathEnrolled"          + aggregate "learning_path"  -> "enrolled"   (PascalCased aggregate prefix variant)
//   - "NodeTraversed"         + aggregate "knowledge_graph"-> "node_traversed" (no aggregate prefix)
//   - "CompanionSkinEquipped"  + aggregate "companion"       -> "skin_equipped"
//
// Strategy:
//
//  1. PascalCase the aggregate (full).
//  2. PascalCase the aggregate's last underscore-separated word (covers
//     "learning_path" -> "Path" pattern).
//  3. If the message name starts with one of those, strip + snake_case the rest.
//  4. Otherwise snake_case the whole message name (covers
//     knowledge_graph's "NodeTraversed" -> "node_traversed").
func messageNameToEventType(messageName, aggregate string) string {
	candidates := []string{pascalCase(aggregate)}
	if last := lastSegment(aggregate); last != aggregate {
		candidates = append(candidates, pascalCase(last))
	}
	for _, prefix := range candidates {
		if strings.HasPrefix(messageName, prefix) {
			rest := messageName[len(prefix):]
			if rest == "" {
				continue
			}
			return pascalToSnake(rest)
		}
	}
	return pascalToSnake(messageName)
}

func pascalCase(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

func lastSegment(s string) string {
	if i := strings.LastIndex(s, "_"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// pascalToSnake converts CamelCase / PascalCase to snake_case.
// Handles consecutive uppercase letters as a single segment (e.g., "GCID" -> "gcid").
func pascalToSnake(s string) string {
	var out strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper {
			lower := r + ('a' - 'A')
			// Insert underscore before this run of uppercase if:
			// - not at start, AND
			// - prev was lowercase, OR
			// - this is the start of a new word (next is lowercase).
			if i > 0 {
				prev := runes[i-1]
				prevIsLower := prev >= 'a' && prev <= 'z'
				nextIsLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				if prevIsLower || (prev >= 'A' && prev <= 'Z' && nextIsLower) {
					out.WriteByte('_')
				}
			}
			out.WriteRune(lower)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// buildTypeIndex constructs a map from fully-qualified type name (with leading
// dot) to its descriptor + containing file. Used to resolve same-package
// references that live in a sibling file (rare).
type typeRef struct {
	file *descriptorpb.FileDescriptorProto
	msg  *descriptorpb.DescriptorProto
	enum *descriptorpb.EnumDescriptorProto
}

func buildTypeIndex(fds *descriptorpb.FileDescriptorSet) map[string]typeRef {
	idx := map[string]typeRef{}
	for _, f := range fds.GetFile() {
		pkg := f.GetPackage()
		for _, m := range f.GetMessageType() {
			indexMessage(idx, f, "."+pkg+".", m)
		}
		for _, e := range f.GetEnumType() {
			idx["."+pkg+"."+e.GetName()] = typeRef{file: f, enum: e}
		}
	}
	return idx
}

func indexMessage(idx map[string]typeRef, f *descriptorpb.FileDescriptorProto, prefix string, m *descriptorpb.DescriptorProto) {
	name := prefix + m.GetName()
	idx[name] = typeRef{file: f, msg: m}
	innerPrefix := name + "."
	for _, n := range m.GetNestedType() {
		indexMessage(idx, f, innerPrefix, n)
	}
	for _, e := range m.GetEnumType() {
		idx[innerPrefix+e.GetName()] = typeRef{file: f, enum: e}
	}
}

// writeFlat renders a self-contained one-message proto for the given event
// message. The output keeps the source's package + emits exactly one
// top-level message (the event), with EventEnvelope + Timestamp + every
// referenced enum/message inlined as NESTED types.
// writeFlat renders one self-contained flat proto. sourceLabel is the path
// printed in the header. It is the descriptor's own name for a current-
// generation source, and the events/-prefixed equivalent for a frozen one, so a
// frozen generation reproduces the header its LIVE schema already carries
// ("events/consumption/familiar.proto").
//
// ⚠ The emitted header text below is baked into every committed file under
// proto/events-flat/. Changing it requires re-running the flatten step so the
// whole tree is regenerated together (tests/test_events_flat_up_to_date.sh
// re-runs the generator and diffs the committed tree).
func writeFlat(w io.Writer, src *descriptorpb.FileDescriptorProto, sourceLabel string, event *descriptorpb.DescriptorProto, envelope *descriptorpb.DescriptorProto, idx map[string]typeRef) error {
	pkg := src.GetPackage()

	// Discover which extra types from the SOURCE file the event message
	// transitively references, so we can inline them as nested types
	// alongside Envelope + Timestamp. Same-package references are common
	// (e.g., shared enum like BookingStatus referenced by every Booking*
	// event message in the same file).
	deps := collectDeps(event, src, idx)

	fmt.Fprintf(w, "// =============================================================================\n")
	fmt.Fprintf(w, "// GENERATED — DO NOT EDIT.\n")
	fmt.Fprintf(w, "// Source : %s message %s\n", sourceLabel, event.GetName())
	fmt.Fprintf(w, "// Tool   : chora-contracts/internal/protoflatten\n")
	fmt.Fprintf(w, "// Why    : Legacy managed schema-registry consumers reject (a) schemas\n")
	fmt.Fprintf(w, "//          with `import` statements and (b) schemas with more than one\n")
	fmt.Fprintf(w, "//          top-level message. This flat copy contains exactly ONE\n")
	fmt.Fprintf(w, "//          top-level message; EventEnvelope + Timestamp + transitive\n")
	fmt.Fprintf(w, "//          deps are nested inside.\n")
	fmt.Fprintf(w, "// Path C : see chora-contracts/CHANGELOG.md and the flatten tooling docs.\n")
	fmt.Fprintf(w, "// =============================================================================\n\n")
	fmt.Fprintf(w, "syntax = \"proto3\";\n\n")
	fmt.Fprintf(w, "package %s;\n\n", pkg)

	// Emit the single top-level event message with everything nested.
	if err := writeEventMessageWithNested(w, event, envelope, deps); err != nil {
		return err
	}
	return nil
}

// collectDeps traverses the event message and returns the set of:
//   - same-package message types it references (excluding itself + nested),
//   - same-package enum types it references.
//
// Returned in declaration order from the source file (deterministic).
type collectedDeps struct {
	messages []*descriptorpb.DescriptorProto
	enums    []*descriptorpb.EnumDescriptorProto
}

func collectDeps(event *descriptorpb.DescriptorProto, src *descriptorpb.FileDescriptorProto, idx map[string]typeRef) collectedDeps {
	visited := map[string]bool{}
	out := collectedDeps{}
	srcPkg := "." + src.GetPackage() + "."

	var visit func(m *descriptorpb.DescriptorProto)
	visit = func(m *descriptorpb.DescriptorProto) {
		for _, f := range m.GetField() {
			if f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
				tn := f.GetTypeName()
				if tn == envelopeFullName || tn == wktTimestampFull {
					continue
				}
				if !strings.HasPrefix(tn, srcPkg) {
					continue
				}
				simple := strings.TrimPrefix(tn, srcPkg)
				// Skip if the type is nested INSIDE the event message (already inline).
				if strings.HasPrefix(simple, event.GetName()+".") {
					continue
				}
				// Skip if it equals the event itself.
				if simple == event.GetName() {
					continue
				}
				// Same-package top-level (or further-nested in another top-level).
				if visited[tn] {
					continue
				}
				visited[tn] = true
				if ref, ok := idx[tn]; ok && ref.msg != nil {
					out.messages = append(out.messages, ref.msg)
					visit(ref.msg)
				}
			} else if f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_ENUM {
				tn := f.GetTypeName()
				if !strings.HasPrefix(tn, srcPkg) {
					continue
				}
				simple := strings.TrimPrefix(tn, srcPkg)
				if strings.HasPrefix(simple, event.GetName()+".") {
					continue
				}
				if visited[tn] {
					continue
				}
				visited[tn] = true
				if ref, ok := idx[tn]; ok && ref.enum != nil {
					out.enums = append(out.enums, ref.enum)
				}
			}
		}
		for _, n := range m.GetNestedType() {
			visit(n)
		}
	}
	visit(event)
	return out
}

// writeEventMessageWithNested renders the top-level event message, with
// EventEnvelope + Timestamp + transitive deps inlined as NESTED messages.
// Field references in the body are rewritten:
//   - .chora.common.v1.EventEnvelope -> Envelope
//   - .google.protobuf.Timestamp     -> Timestamp
//   - .chora.{domain}.v1.Foo         -> Foo (it's now a nested type)
func writeEventMessageWithNested(w io.Writer, event *descriptorpb.DescriptorProto, envelope *descriptorpb.DescriptorProto, deps collectedDeps) error {
	fmt.Fprintf(w, "message %s {\n", event.GetName())
	inner := "  "

	// Inline Envelope first.
	fmt.Fprintf(w, "%s// Inlined chora.common.v1.EventEnvelope (nested for self-contained schema).\n", inner)
	if err := writeMessageBody(w, envelope, "Envelope", inner); err != nil {
		return err
	}
	fmt.Fprintln(w)

	// Inline Timestamp. Body lines are unindented in the source const; we
	// prefix with the field-indent (`inner + "  "`) so they align with the
	// rest of the nested message body.
	fmt.Fprintf(w, "%s// Inlined google.protobuf.Timestamp (nested; wire-compatible).\n", inner)
	fmt.Fprintf(w, "%smessage Timestamp {\n", inner)
	bodyIndent := inner + "  "
	for _, line := range strings.Split(timestampMessageBody, "\n") {
		fmt.Fprintf(w, "%s%s\n", bodyIndent, line)
	}
	fmt.Fprintf(w, "%s}\n", inner)
	fmt.Fprintln(w)

	// Inline same-package message deps as nested types.
	depMessages := append([]*descriptorpb.DescriptorProto(nil), deps.messages...)
	sort.Slice(depMessages, func(i, j int) bool { return depMessages[i].GetName() < depMessages[j].GetName() })
	for _, dm := range depMessages {
		fmt.Fprintf(w, "%s// Inlined dep: %s\n", inner, dm.GetName())
		if err := writeMessageBody(w, dm, dm.GetName(), inner); err != nil {
			return err
		}
		fmt.Fprintln(w)
	}

	// Inline same-package enum deps.
	depEnums := append([]*descriptorpb.EnumDescriptorProto(nil), deps.enums...)
	sort.Slice(depEnums, func(i, j int) bool { return depEnums[i].GetName() < depEnums[j].GetName() })
	for _, de := range depEnums {
		fmt.Fprintf(w, "%s// Inlined enum dep: %s\n", inner, de.GetName())
		writeEnum(w, de, inner)
		fmt.Fprintln(w)
	}

	// Now the event message's own nested enums + messages (kept).
	for _, e := range event.GetEnumType() {
		writeEnum(w, e, inner)
	}
	for _, n := range event.GetNestedType() {
		if n.GetOptions().GetMapEntry() {
			continue
		}
		if err := writeMessageBody(w, n, n.GetName(), inner); err != nil {
			return err
		}
		fmt.Fprintln(w)
	}

	// Finally the event message's own fields.
	if err := writeFields(w, event, inner); err != nil {
		return err
	}

	for _, r := range event.GetReservedRange() {
		if r.GetEnd()-r.GetStart() == 1 {
			fmt.Fprintf(w, "%sreserved %d;\n", inner, r.GetStart())
		} else {
			fmt.Fprintf(w, "%sreserved %d to %d;\n", inner, r.GetStart(), r.GetEnd()-1)
		}
	}
	for _, n := range event.GetReservedName() {
		fmt.Fprintf(w, "%sreserved %q;\n", inner, n)
	}

	fmt.Fprintln(w, "}")
	return nil
}

// writeMessageBody renders a complete `message Name { ... }` block at the
// given indent. nameOverride lets callers rename the message (e.g., to
// "Envelope" instead of "EventEnvelope") on inline.
func writeMessageBody(w io.Writer, m *descriptorpb.DescriptorProto, nameOverride, indent string) error {
	fmt.Fprintf(w, "%smessage %s {\n", indent, nameOverride)
	inner := indent + "  "

	for _, e := range m.GetEnumType() {
		writeEnum(w, e, inner)
	}
	for _, n := range m.GetNestedType() {
		if n.GetOptions().GetMapEntry() {
			continue
		}
		if err := writeMessageBody(w, n, n.GetName(), inner); err != nil {
			return err
		}
	}
	if err := writeFields(w, m, inner); err != nil {
		return err
	}
	for _, r := range m.GetReservedRange() {
		if r.GetEnd()-r.GetStart() == 1 {
			fmt.Fprintf(w, "%sreserved %d;\n", inner, r.GetStart())
		} else {
			fmt.Fprintf(w, "%sreserved %d to %d;\n", inner, r.GetStart(), r.GetEnd()-1)
		}
	}
	for _, n := range m.GetReservedName() {
		fmt.Fprintf(w, "%sreserved %q;\n", inner, n)
	}
	fmt.Fprintf(w, "%s}\n", indent)
	return nil
}

// writeFields renders the field list for a message. Honours oneofs
// (declaration-order grouped) and proto3 explicit `optional`.
func writeFields(w io.Writer, m *descriptorpb.DescriptorProto, indent string) error {
	type fieldInfo struct {
		fd *descriptorpb.FieldDescriptorProto
	}
	var nonOneof []fieldInfo
	oneofs := map[int32][]fieldInfo{}
	for _, f := range m.GetField() {
		if f.OneofIndex != nil && f.GetProto3Optional() == false {
			oneofs[f.GetOneofIndex()] = append(oneofs[f.GetOneofIndex()], fieldInfo{f})
			continue
		}
		nonOneof = append(nonOneof, fieldInfo{f})
	}
	for _, fi := range nonOneof {
		if err := writeField(w, m, fi.fd, indent); err != nil {
			return err
		}
	}
	if len(oneofs) > 0 {
		idxs := make([]int32, 0, len(oneofs))
		for i := range oneofs {
			idxs = append(idxs, i)
		}
		sort.Slice(idxs, func(i, j int) bool { return idxs[i] < idxs[j] })
		for _, idx := range idxs {
			oneofName := m.GetOneofDecl()[idx].GetName()
			fmt.Fprintf(w, "%soneof %s {\n", indent, oneofName)
			for _, fi := range oneofs[idx] {
				if err := writeField(w, m, fi.fd, indent+"  "); err != nil {
					return err
				}
			}
			fmt.Fprintf(w, "%s}\n", indent)
		}
	}
	return nil
}

func writeEnum(w io.Writer, e *descriptorpb.EnumDescriptorProto, indent string) {
	fmt.Fprintf(w, "%senum %s {\n", indent, e.GetName())
	for _, v := range e.GetValue() {
		fmt.Fprintf(w, "%s  %s = %d;\n", indent, v.GetName(), v.GetNumber())
	}
	fmt.Fprintf(w, "%s}\n", indent)
}

func writeField(w io.Writer, parent *descriptorpb.DescriptorProto, f *descriptorpb.FieldDescriptorProto, indent string) error {
	if f.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE && f.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
		mapEntry := findMapEntry(parent, f.GetTypeName())
		if mapEntry != nil {
			keyT, valT := mapKVTypes(mapEntry)
			fmt.Fprintf(w, "%smap<%s, %s> %s = %d;\n", indent, keyT, valT, f.GetName(), f.GetNumber())
			return nil
		}
	}
	prefix := ""
	switch f.GetLabel() {
	case descriptorpb.FieldDescriptorProto_LABEL_REPEATED:
		prefix = "repeated "
	case descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL:
		if f.GetProto3Optional() {
			prefix = "optional "
		}
	}
	typ, err := fieldType(f)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s%s%s %s = %d;\n", indent, prefix, typ, f.GetName(), f.GetNumber())
	return nil
}

func findMapEntry(parent *descriptorpb.DescriptorProto, fullTypeName string) *descriptorpb.DescriptorProto {
	idx := strings.LastIndex(fullTypeName, ".")
	short := fullTypeName
	if idx >= 0 {
		short = fullTypeName[idx+1:]
	}
	for _, n := range parent.GetNestedType() {
		if n.GetName() == short && n.GetOptions().GetMapEntry() {
			return n
		}
	}
	return nil
}

func mapKVTypes(entry *descriptorpb.DescriptorProto) (string, string) {
	var keyT, valT string
	for _, f := range entry.GetField() {
		t, _ := fieldType(f)
		if f.GetName() == "key" {
			keyT = t
		} else if f.GetName() == "value" {
			valT = t
		}
	}
	return keyT, valT
}

func fieldType(f *descriptorpb.FieldDescriptorProto) (string, error) {
	switch f.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:
		return "double", nil
	case descriptorpb.FieldDescriptorProto_TYPE_FLOAT:
		return "float", nil
	case descriptorpb.FieldDescriptorProto_TYPE_INT64:
		return "int64", nil
	case descriptorpb.FieldDescriptorProto_TYPE_UINT64:
		return "uint64", nil
	case descriptorpb.FieldDescriptorProto_TYPE_INT32:
		return "int32", nil
	case descriptorpb.FieldDescriptorProto_TYPE_FIXED64:
		return "fixed64", nil
	case descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		return "fixed32", nil
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		return "bool", nil
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		return "string", nil
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		return "bytes", nil
	case descriptorpb.FieldDescriptorProto_TYPE_UINT32:
		return "uint32", nil
	case descriptorpb.FieldDescriptorProto_TYPE_SFIXED32:
		return "sfixed32", nil
	case descriptorpb.FieldDescriptorProto_TYPE_SFIXED64:
		return "sfixed64", nil
	case descriptorpb.FieldDescriptorProto_TYPE_SINT32:
		return "sint32", nil
	case descriptorpb.FieldDescriptorProto_TYPE_SINT64:
		return "sint64", nil
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE:
		return rewriteTypeName(f.GetTypeName()), nil
	case descriptorpb.FieldDescriptorProto_TYPE_GROUP:
		return "", fmt.Errorf("proto2 GROUP type not supported (field %s)", f.GetName())
	}
	return "", fmt.Errorf("unknown field type %v on %s", f.GetType(), f.GetName())
}

// rewriteTypeName converts a fully qualified type name to a form valid
// within the flat per-event proto. Every type the event references is
// either:
//   - .chora.common.v1.EventEnvelope -> nested as `Envelope`
//   - .google.protobuf.Timestamp     -> nested as `Timestamp`
//   - same-package type              -> simple short name (it's also nested)
func rewriteTypeName(name string) string {
	if name == envelopeFullName {
		return envelopeShortName
	}
	if name == wktTimestampFull {
		return wktTimestampShort
	}
	trimmed := strings.TrimPrefix(name, ".")
	if i := strings.LastIndex(trimmed, "."); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

func findFile(fds *descriptorpb.FileDescriptorSet, name string) *descriptorpb.FileDescriptorProto {
	for _, f := range fds.GetFile() {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

func findMessage(f *descriptorpb.FileDescriptorProto, name string) *descriptorpb.DescriptorProto {
	for _, m := range f.GetMessageType() {
		if m.GetName() == name {
			return m
		}
	}
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "protoflatten: "+format+"\n", args...)
	os.Exit(1)
}
