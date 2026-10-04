// Package schemaguard compares Chora's event protos against the schema
// revisions actually COMMITTED in the GCP Pub/Sub Schema Registry.
//
// The invariant it enforces:
//
//	every field in a repo event proto MUST be present, with a wire-compatible
//	shape, in the topic's committed schema revision.
//
// Violating it does not degrade gracefully. Pub/Sub's validator is strict: a
// binary message carrying a field the committed revision lacks is REJECTED AT
// PUBLISH with HTTP 400 ("Message failed schema validation"). Protobuf's normal
// unknown-field tolerance does not apply. The message never enters the topic,
// so it never dead-letters — it is simply gone, and the only trace is the
// topic/send_request_count metric with response_code=invalid_argument.
package schemaguard

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"
)

// Kind is a field's wire class. Scalar/message/enum are NOT interchangeable:
// an enum is a varint, a message is length-delimited. Collapsing them would let
// a real wire break through.
type Kind string

const (
	KindScalar  Kind = "scalar"
	KindMessage Kind = "message"
	KindEnum    Kind = "enum"
)

// Field is the wire-relevant shape of one top-level field. Two fields are
// wire-compatible iff they are ==.
type Field struct {
	Number   int32
	Name     string
	Kind     Kind
	Scalar   string // set only when Kind == KindScalar ("string", "int32", ...)
	Repeated bool
}

func (f Field) String() string {
	lbl := ""
	if f.Repeated {
		lbl = "repeated "
	}
	typ := f.Scalar
	if f.Kind != KindScalar {
		typ = string(f.Kind)
	}
	return fmt.Sprintf("%s%s %s = %d", lbl, typ, f.Name, f.Number)
}

// Message is one top-level event message: the thing a Pub/Sub schema registers
// and the thing a producer puts on the wire.
type Message struct {
	Package string
	Name    string
	Fields  map[int32]Field

	// Nested holds the fields of every NESTED message, keyed by scope path
	// relative to the top-level message ("OutputSelection", "Envelope",
	// "Outer.Inner"). Without this the guard is blind to a rename inside an
	// inlined sub-message: it never saw
	// WeaknessAnalyzed.OutputSelection.familiar_coaching move to
	// companion_coaching, while it did see the top-level
	// CampaignFocusAssigned.familiar_id move. Pub/Sub refuses BOTH, so a guard
	// that only compares top-level fields reports a green build for half the
	// defect class (ruling 44).
	Nested map[string]map[int32]Field
}

// FullName is the package-qualified name, e.g. chora.delivery.v1.SubmissionGraded.
// It is the join key between the repo descriptor and the committed schema —
// deliberately NOT the filename or the schema name, both of which are
// hand-maintained conventions that drift.
func (m Message) FullName() string { return m.Package + "." + m.Name }

// -----------------------------------------------------------------------------
// Side A — the committed schema, parsed from its registered .proto text.
// -----------------------------------------------------------------------------

var (
	rePackage = regexp.MustCompile(`^\s*package\s+([\w.]+)\s*;`)
	reMessage = regexp.MustCompile(`^\s*message\s+(\w+)\s*\{`)
	reEnum    = regexp.MustCompile(`^\s*enum\s+(\w+)\s*\{`)
	reField   = regexp.MustCompile(`^\s*(?:(repeated|optional)\s+)?([A-Za-z_][\w.]*)\s+([a-z_]\w*)\s*=\s*(\d+)\s*;`)

	// A map field must be matched BEFORE reField, whose type group cannot span
	// the `<K, V>` punctuation and would otherwise skip the line entirely —
	// silently dropping the field and reporting a healthy schema as broken.
	reMapField = regexp.MustCompile(`^\s*map\s*<\s*[\w.]+\s*,\s*[\w.]+\s*>\s+([a-z_]\w*)\s*=\s*(\d+)\s*;`)
)

// protoScalars are the proto3 scalar type names. Anything else at field
// position is a message or an enum, resolved against the declarations found in
// the same definition.
var protoScalars = map[string]bool{
	"double": true, "float": true, "int32": true, "int64": true,
	"uint32": true, "uint64": true, "sint32": true, "sint64": true,
	"fixed32": true, "fixed64": true, "sfixed32": true, "sfixed64": true,
	"bool": true, "string": true, "bytes": true,
}

// ParseSchemaDefinition parses a registered Pub/Sub schema definition (the
// self-contained proto text protoflatten emits) into its ONE top-level message.
//
// The hard part is nesting: the flat schema inlines EventEnvelope, Timestamp
// and any enums as NESTED declarations inside the top-level message. Their
// fields (event_id = 1, seconds = 1, ...) sit at brace depth >= 2 and must not
// be mistaken for top-level fields, or every comparison downstream is garbage.
func ParseSchemaDefinition(def string) (Message, error) {
	msg := Message{Fields: map[int32]Field{}, Nested: map[string]map[int32]Field{}}
	enums := map[string]bool{}

	// Enum names can be declared after the field that uses them, so collect
	// them in a first pass.
	for _, raw := range strings.Split(def, "\n") {
		if m := reEnum.FindStringSubmatch(stripComment(raw)); m != nil {
			enums[m[1]] = true
		}
	}

	depth := 0
	var scope []string  // nested MESSAGE names, innermost last
	var enumDepth []int // depths at which an enum scope was opened
	for _, raw := range strings.Split(def, "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}

		if m := rePackage.FindStringSubmatch(line); m != nil && msg.Package == "" {
			msg.Package = m[1]
			continue
		}

		if m := reMessage.FindStringSubmatch(line); m != nil {
			if depth == 0 {
				if msg.Name != "" {
					return Message{}, fmt.Errorf("schemaguard: definition has more than one top-level message (%q and %q); "+
						"Pub/Sub schemas must contain exactly one", msg.Name, m[1])
				}
				msg.Name = m[1]
			} else {
				scope = append(scope, m[1])
			}
			depth++
			continue
		}
		if reEnum.MatchString(line) {
			depth++
			enumDepth = append(enumDepth, depth)
			continue
		}

		if f, ok := parseFieldLine(line, enums); ok {
			switch {
			case depth == 1:
				if prev, dup := msg.Fields[f.Number]; dup {
					return Message{}, fmt.Errorf("schemaguard: field number %d declared twice (%s, %s)", f.Number, prev.Name, f.Name)
				}
				msg.Fields[f.Number] = f
			case depth > 1 && len(scope) > 0:
				key := strings.Join(scope, ".")
				if msg.Nested[key] == nil {
					msg.Nested[key] = map[int32]Field{}
				}
				msg.Nested[key][f.Number] = f
			}
		}

		opens := strings.Count(line, "{")
		closes := strings.Count(line, "}")
		depth += opens - closes
		for i := 0; i < closes; i++ {
			// An enum scope closing pops no message scope.
			if n := len(enumDepth); n > 0 && enumDepth[n-1] > depth {
				enumDepth = enumDepth[:n-1]
				continue
			}
			if len(scope) > 0 && depth < len(scope)+1 {
				scope = scope[:len(scope)-1]
			}
		}
		if depth < 0 {
			depth = 0
		}
	}

	if msg.Name == "" {
		return Message{}, errors.New("schemaguard: definition declares no top-level message")
	}
	if msg.Package == "" {
		return Message{}, fmt.Errorf("schemaguard: message %q declares no package", msg.Name)
	}
	return msg, nil
}

func parseFieldLine(line string, enums map[string]bool) (Field, bool) {
	// map<K, V> first. protoc lowers a map field to a REPEATED MESSAGE field of
	// a synthetic nested <Name>Entry type, so that is exactly how the descriptor
	// side reports it — match that representation or every map field looks like
	// drift.
	if m := reMapField.FindStringSubmatch(line); m != nil {
		num, err := strconv.ParseInt(m[2], 10, 32)
		if err != nil {
			return Field{}, false
		}
		return Field{
			Number:   int32(num),
			Name:     m[1],
			Kind:     KindMessage,
			Repeated: true,
		}, true
	}

	m := reField.FindStringSubmatch(line)
	if m == nil {
		return Field{}, false
	}
	// Guard against `message Foo {` / `enum Foo {` slipping through as a field.
	if strings.HasSuffix(strings.TrimSpace(line), "{") {
		return Field{}, false
	}
	num, err := strconv.ParseInt(m[4], 10, 32)
	if err != nil {
		return Field{}, false
	}
	typ := m[2]
	f := Field{
		Number:   int32(num),
		Name:     m[3],
		Repeated: m[1] == "repeated",
	}
	switch {
	case protoScalars[typ]:
		f.Kind = KindScalar
		f.Scalar = typ
	case enums[bareType(typ)]:
		f.Kind = KindEnum
	default:
		f.Kind = KindMessage
	}
	return f, true
}

// bareType drops any qualification: chora.delivery.v1.SubmissionState -> SubmissionState.
func bareType(t string) string {
	if i := strings.LastIndex(t, "."); i >= 0 {
		return t[i+1:]
	}
	return t
}

func stripComment(s string) string {
	if i := strings.Index(s, "//"); i >= 0 {
		return s[:i]
	}
	return s
}

// -----------------------------------------------------------------------------
// Side B — the repo, derived from the GENERATED descriptor.
//
// Derived, never hand-listed: a hand-maintained map of "events we care about"
// is exactly the artifact that drifts silently. The FileDescriptorSet is
// produced by `buf build proto`, so it cannot disagree with what codegen emits.
// -----------------------------------------------------------------------------

// scalarNames maps descriptor field types to their proto3 source spelling, so
// the descriptor side and the parsed-text side speak the same language.
var scalarNames = map[descriptorpb.FieldDescriptorProto_Type]string{
	descriptorpb.FieldDescriptorProto_TYPE_DOUBLE:   "double",
	descriptorpb.FieldDescriptorProto_TYPE_FLOAT:    "float",
	descriptorpb.FieldDescriptorProto_TYPE_INT64:    "int64",
	descriptorpb.FieldDescriptorProto_TYPE_UINT64:   "uint64",
	descriptorpb.FieldDescriptorProto_TYPE_INT32:    "int32",
	descriptorpb.FieldDescriptorProto_TYPE_FIXED64:  "fixed64",
	descriptorpb.FieldDescriptorProto_TYPE_FIXED32:  "fixed32",
	descriptorpb.FieldDescriptorProto_TYPE_BOOL:     "bool",
	descriptorpb.FieldDescriptorProto_TYPE_STRING:   "string",
	descriptorpb.FieldDescriptorProto_TYPE_BYTES:    "bytes",
	descriptorpb.FieldDescriptorProto_TYPE_UINT32:   "uint32",
	descriptorpb.FieldDescriptorProto_TYPE_SFIXED32: "sfixed32",
	descriptorpb.FieldDescriptorProto_TYPE_SFIXED64: "sfixed64",
	descriptorpb.FieldDescriptorProto_TYPE_SINT32:   "sint32",
	descriptorpb.FieldDescriptorProto_TYPE_SINT64:   "sint64",
}

// MessagesFromFDS indexes every top-level message in the descriptor set by its
// package-qualified name.
func MessagesFromFDS(fds *descriptorpb.FileDescriptorSet) (map[string]Message, error) {
	out := map[string]Message{}
	for _, file := range fds.GetFile() {
		pkg := file.GetPackage()
		for _, dm := range file.GetMessageType() {
			m := Message{Package: pkg, Name: dm.GetName(), Fields: map[int32]Field{}}
			for _, df := range dm.GetField() {
				m.Fields[df.GetNumber()] = fieldFromDescriptor(df)
			}
			out[m.FullName()] = m
		}
	}
	if len(out) == 0 {
		return nil, errors.New("schemaguard: FileDescriptorSet contains no messages — " +
			"the buf build produced nothing, which would make this guard vacuously green")
	}
	return out, nil
}

func fieldFromDescriptor(df *descriptorpb.FieldDescriptorProto) Field {
	f := Field{
		Number:   df.GetNumber(),
		Name:     df.GetName(),
		Repeated: df.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED,
	}
	switch t := df.GetType(); t {
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_TYPE_GROUP:
		f.Kind = KindMessage
	case descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		f.Kind = KindEnum
	default:
		f.Kind = KindScalar
		f.Scalar = scalarNames[t]
	}
	return f
}

// -----------------------------------------------------------------------------
// The comparison.
// -----------------------------------------------------------------------------

// FieldChange is one field number whose shape differs between repo and registry.
type FieldChange struct {
	Number    int32
	Repo      Field
	Committed Field
	// Scope is "" for a top-level field, else the nested message path
	// ("OutputSelection"). A rename at the same tag inside a nested message is
	// refused by the registry exactly as a top-level one is.
	Scope string
}

// Drift is the verdict for one event message.
type Drift struct {
	SchemaName    string
	RevisionID    string
	MessageName   string
	FlatProtoPath string

	// MissingFromRegistry: the repo declares it, the committed revision does
	// not. FATAL — the instant a producer populates one of these, Pub/Sub
	// rejects the publish with HTTP 400.
	MissingFromRegistry []Field

	// MissingFromRepo: the committed revision carries it, the repo has never
	// heard of it — someone hand-edited the registry out-of-band. NOT fatal
	// (proto3 simply omits it), but it means the repo is no longer the source
	// of truth, and a future regen would silently drop the field.
	MissingFromRepo []Field

	// Incompatible: same field number, different wire shape. FATAL.
	Incompatible []FieldChange
}

// Compare checks the repo message against the committed schema revision.
func Compare(repo, committed Message) Drift {
	d := Drift{MessageName: repo.FullName()}

	// Nested scopes first: same tag, different name or type, inside an inlined
	// sub-message. Invisible to a flat top-level comparison, and refused by the
	// registry all the same.
	for scope, rfs := range repo.Nested {
		cfs := committed.Nested[scope]
		for num, rf := range rfs {
			cf, ok := cfs[num]
			if !ok || rf == cf {
				continue
			}
			d.Incompatible = append(d.Incompatible, FieldChange{Number: num, Repo: rf, Committed: cf, Scope: scope})
		}
	}

	for num, rf := range repo.Fields {
		cf, ok := committed.Fields[num]
		switch {
		case !ok:
			d.MissingFromRegistry = append(d.MissingFromRegistry, rf)
		case rf != cf:
			d.Incompatible = append(d.Incompatible, FieldChange{Number: num, Repo: rf, Committed: cf})
		}
	}
	for num, cf := range committed.Fields {
		if _, ok := repo.Fields[num]; !ok {
			d.MissingFromRepo = append(d.MissingFromRepo, cf)
		}
	}

	sortFields(d.MissingFromRegistry)
	sortFields(d.MissingFromRepo)
	sort.Slice(d.Incompatible, func(i, j int) bool {
		if d.Incompatible[i].Scope != d.Incompatible[j].Scope {
			return d.Incompatible[i].Scope < d.Incompatible[j].Scope
		}
		return d.Incompatible[i].Number < d.Incompatible[j].Number
	})
	return d
}

func sortFields(fs []Field) {
	sort.Slice(fs, func(i, j int) bool { return fs[i].Number < fs[j].Number })
}

// Fatal reports whether this drift breaks the wire — i.e. whether a publish
// can be rejected with HTTP 400 because of it. Only the repo-ahead-of-registry
// direction and incompatible changes qualify.
func (d Drift) Fatal() bool {
	return len(d.MissingFromRegistry) > 0 || len(d.Incompatible) > 0
}

// HasDrift reports any disagreement at all, fatal or not.
func (d Drift) HasDrift() bool {
	return d.Fatal() || len(d.MissingFromRepo) > 0
}

// Remediation is the exact command an operator runs to close the drift.
//
// Deliberately NOT `terraform apply`: this project's TF state is ~228 resources
// behind and the standing rule is to provision Pub/Sub out-of-band. A full
// apply would be far more dangerous than the drift it fixes. Committing a
// revision is additive and backward-compatible — messages built against the
// OLD revision still validate against the new one — and it propagates in a few
// seconds. Topics pin no revision range, so nothing needs re-pointing.
func (d Drift) Remediation() string {
	if !d.Fatal() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "commit a new schema revision from the flat proto (run from chora-contracts/):\n\n")
	fmt.Fprintf(&b, "    gcloud pubsub schemas commit %s \\\n", d.SchemaName)
	fmt.Fprintf(&b, "      --type=protocol-buffer \\\n")
	fmt.Fprintf(&b, "      --definition-file=%s\n", d.FlatProtoPath)
	return b.String()
}

// Report renders a human-readable verdict.
func (d Drift) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", d.MessageName)
	fmt.Fprintf(&b, "  schema   : %s (committed revision %s)\n", d.SchemaName, d.RevisionID)
	for _, f := range d.MissingFromRegistry {
		fmt.Fprintf(&b, "  BREAKS   : %-44s declared in the repo, ABSENT from the committed revision\n", f.String())
	}
	for _, c := range d.Incompatible {
		fmt.Fprintf(&b, "  BREAKS   : field %d changed shape: registry has %q, repo has %q\n",
			c.Number, c.Committed.String(), c.Repo.String())
	}
	for _, f := range d.MissingFromRepo {
		fmt.Fprintf(&b, "  drift    : %-44s in the committed revision, ABSENT from the repo (registry hand-edited OOB)\n", f.String())
	}
	if r := d.Remediation(); r != "" {
		fmt.Fprintf(&b, "  fix      : %s", strings.ReplaceAll(r, "\n", "\n  "))
	}
	return b.String()
}
