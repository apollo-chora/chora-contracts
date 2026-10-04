// requiredset.go — derive the REQUIRED flat-proto set from the DECLARED topic
// list, and fail loud when a declared topic has no source.
//
// WHY THIS EXISTS (ruling 44, 2026-09-04). protoflatten used to derive its
// output set from whichever source protos happened to exist. Terraform derives
// its INPUT set from the declared topic list and resolves each one with
// file(). Those two sets are not the same set, and nothing checked that they
// agreed:
//
//   - 8f5f2de0a renamed 32 sources for the ADR-254 Companion cut, so the
//     flattener stopped emitting consumption/familiar/*, payments/
//     familiar_egg_purchase/* and tenancy/familiar_egg/*.
//   - bca674d98 deleted the legacy duel sources, so it stopped emitting the 7
//     sharing/duel/* protos.
//
// Both were ratified renames. Neither touched Terraform, which still declares
// those topics, and 37 of the 39 orphaned flat protos back a LIVE
// BINARY-bound schema. Because the wrapper wipes the tree before regenerating,
// running the generator DELETED live wire, and the dev root module could then
// not plan at all: 39 identical file() errors, hand-restored by 27d187ffb.
//
// The fix is to make the required set the driver. A declared topic whose source
// has gone is now a named failure at the moment of the rename, instead of a
// silent deletion that surfaces weeks later as a plan outage.
//
// ⚠ This file MIRRORS Terraform's path derivations. A mirror can drift from
// what it mirrors, and a guard that quietly narrows is worse than no guard, so
// assertKnownSchemaResources refuses to run when a google_pubsub_schema
// resource appears whose derivation is not mirrored here.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// requiredPath is one flat proto that Terraform will resolve with file().
type requiredPath struct {
	Rel      string // path relative to proto/events-flat
	Topic    string // the declared topic (or schema key) that requires it
	Resource string // the google_pubsub_schema resource that resolves it
}

// knownSchemaResources are the google_pubsub_schema resources whose proto_path
// derivation is mirrored below. Adding a resource in Terraform without adding
// it here must break the build, not narrow the guard.
var knownSchemaResources = map[string]string{
	"aggregate":                 "m10-data-plane local.pubsub_schemas",
	"atom_v2":                   "m10-data-plane local.pubsub_schemas_v2",
	"observability_decision_v2": "m10-data-plane local.pubsub_schemas_v2_observability",
	"compose_v2":                "m10-data-plane local.pubsub_schemas_compose_v2",
	"companion":                 "_root companion_topic_estate local.companion_topic_schema",
}

var schemaResourceRe = regexp.MustCompile(`resource\s+"google_pubsub_schema"\s+"([A-Za-z0-9_]+)"`)

// assertKnownSchemaResources fails loud, naming the resource, when Terraform
// declares a google_pubsub_schema whose derivation this file does not mirror.
func assertKnownSchemaResources(hclByFile map[string]string) error {
	var unknown []string
	seen := map[string]bool{}
	for file, hcl := range hclByFile {
		for _, m := range schemaResourceRe.FindAllStringSubmatch(hcl, -1) {
			name := m[1]
			if _, ok := knownSchemaResources[name]; ok || seen[name] {
				continue
			}
			seen[name] = true
			unknown = append(unknown, fmt.Sprintf("%s (in %s)", name, file))
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf(
		"protoflatten: %d google_pubsub_schema resource(s) have no mirrored proto_path derivation: %s\n"+
			"  The required-set check would silently NOT cover them, which is the failure this check exists to end.\n"+
			"  Mirror the new derivation in internal/protoflatten/requiredset.go (knownSchemaResources + requiredFromTerraform).",
		len(unknown), strings.Join(unknown, ", "))
}

var lineLeadingKeyRe = regexp.MustCompile(`^\s*"([^"]+)"`)

// parseHCLBlockKeys returns the keys of `<localName> = { ... }` or the elements
// of `<localName> = toset([ ... ])`. Only a quoted string that OPENS a line is
// taken, so a nested map value on the same line (`= { domain = "creation" }`)
// is never mistaken for a key. Comment lines are skipped, so a commented-out
// declaration does not read as declared. A missing block is an error, never an
// empty set: an empty set would read as "nothing is declared" and pass.
func parseHCLBlockKeys(hcl, localName string) ([]string, error) {
	openRe := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(localName) + `\s*=\s*(toset\(\[|\{|\[)`)
	loc := openRe.FindStringIndex(hcl)
	if loc == nil {
		return nil, fmt.Errorf("protoflatten: local %q not found in the Terraform source; "+
			"the required-set check cannot be derived from a block that does not exist", localName)
	}

	// `\s*` in openRe can swallow a preceding blank line, so the match may start
	// BEFORE the opening delimiter. Only close the block once it has opened,
	// otherwise the very first line ends the scan and the block reads as empty.
	depth, opened := 0, false
	var keys []string
	for _, line := range strings.Split(hcl[loc[0]:], "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			continue
		}
		if k := lineLeadingKeyRe.FindStringSubmatch(line); k != nil && depth > 0 {
			keys = append(keys, k[1])
		}
		depth += strings.Count(line, "{") + strings.Count(line, "[")
		depth -= strings.Count(line, "}") + strings.Count(line, "]")
		if depth > 0 {
			opened = true
		}
		if opened && depth <= 0 {
			break
		}
	}
	return keys, nil
}

// aggregateFlatRel mirrors m10-data-plane local.pubsub_schemas:
//
//	proto_path = events-flat/{split[1]}/{len==5 ? split[2] : "saga"}/{split[len-2]}.proto
func aggregateFlatRel(topic string) string {
	p := strings.Split(topic, ".")
	if len(p) < 4 {
		return ""
	}
	aggregate := "saga"
	if len(p) == 5 {
		aggregate = p[2]
	}
	return path3(p[1], aggregate, p[len(p)-2]+".proto")
}

// versionedFlatRel mirrors local.pubsub_schemas_v2 and _compose_v2:
//
//	proto_path = events-flat/{split[1]}/{split[2]}/{split[3]}.v2.proto
func versionedFlatRel(topic string) string {
	p := strings.Split(topic, ".")
	if len(p) < 5 {
		return ""
	}
	return path3(p[1], p[2], p[3]+".v2.proto")
}

// observabilityFlatRel mirrors local.pubsub_schemas_v2_observability, which
// binds the UNSUFFIXED flat proto (the additive field lives in the regular flat;
// only the GCP schema resource name carries the -v2 suffix).
func observabilityFlatRel(topic string) string {
	p := strings.Split(topic, ".")
	if len(p) < 5 {
		return ""
	}
	return path3(p[1], p[2], p[3]+".proto")
}

func path3(a, b, c string) string { return a + "/" + b + "/" + c }

var estateProtoRe = regexp.MustCompile(`^\s*"([^"]+)"\s*=\s*\{[^}]*\bproto\s*=\s*"([^"]+)"`)

// parseEstateProtos reads the _root companion estate, which does NOT derive its
// path from the topic name but states it outright.
func parseEstateProtos(hcl string) ([]requiredPath, error) {
	openRe := regexp.MustCompile(`(?m)^\s*companion_topic_schema\s*=\s*\{`)
	loc := openRe.FindStringIndex(hcl)
	if loc == nil {
		return nil, fmt.Errorf("protoflatten: local %q not found in the companion estate source", "companion_topic_schema")
	}
	depth, opened := 0, false
	var out []requiredPath
	for _, line := range strings.Split(hcl[loc[0]:], "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
			continue
		}
		if m := estateProtoRe.FindStringSubmatch(line); m != nil && depth > 0 {
			out = append(out, requiredPath{Rel: m[2], Topic: m[1], Resource: "google_pubsub_schema.companion"})
		}
		depth += strings.Count(line, "{") + strings.Count(line, "[")
		depth -= strings.Count(line, "}") + strings.Count(line, "]")
		if depth > 0 {
			opened = true
		}
		if opened && depth <= 0 {
			break
		}
	}
	return out, nil
}

// requiredFromTerraform builds the complete required set across all five
// google_pubsub_schema resources.
func requiredFromTerraform(m10HCL, estateHCL string) ([]requiredPath, error) {
	topics, err := parseHCLBlockKeys(m10HCL, "pubsub_topics")
	if err != nil {
		return nil, err
	}
	sets := map[string]map[string]bool{}
	for _, name := range []string{"atom_v2_topics", "observability_decision_v2_topics", "compose_v2_topics", "schemaless_topics"} {
		keys, err := parseHCLBlockKeys(m10HCL, name)
		if err != nil {
			return nil, err
		}
		s := map[string]bool{}
		for _, k := range keys {
			s[k] = true
		}
		sets[name] = s
	}

	var out []requiredPath
	add := func(rel, topic, resource string) {
		if rel == "" {
			return
		}
		out = append(out, requiredPath{Rel: rel, Topic: topic, Resource: resource})
	}
	for _, t := range topics {
		switch {
		case sets["schemaless_topics"][t]:
			// Deliberately bound to NO schema, so it requires no flat proto.
			continue
		case sets["atom_v2_topics"][t]:
			add(versionedFlatRel(t), t, "google_pubsub_schema.atom_v2")
		case sets["compose_v2_topics"][t]:
			add(versionedFlatRel(t), t, "google_pubsub_schema.compose_v2")
		case sets["observability_decision_v2_topics"][t]:
			add(observabilityFlatRel(t), t, "google_pubsub_schema.observability_decision_v2")
		default:
			add(aggregateFlatRel(t), t, "google_pubsub_schema.aggregate")
		}
	}

	estate, err := parseEstateProtos(estateHCL)
	if err != nil {
		return nil, err
	}
	out = append(out, estate...)

	seen := map[string]bool{}
	deduped := out[:0]
	for _, r := range out {
		if seen[r.Rel] {
			continue
		}
		seen[r.Rel] = true
		deduped = append(deduped, r)
	}
	return deduped, nil
}

// checkRequired fails loud, naming every declared topic whose flat proto is
// absent, together with the resource that resolves it.
func checkRequired(outDir string, required []requiredPath) error {
	var missing []requiredPath
	for _, r := range required {
		if _, err := os.Stat(filepath.Join(outDir, filepath.FromSlash(r.Rel))); err != nil {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Rel < missing[j].Rel })
	var b strings.Builder
	fmt.Fprintf(&b, "protoflatten: %d declared topic(s) have NO flat proto, so `terraform plan` would fail on file():\n", len(missing))
	for _, m := range missing {
		fmt.Fprintf(&b, "  %-58s needs %s\n      (%s)\n", m.Topic, m.Rel, m.Resource)
	}
	b.WriteString("  Either the source proto was renamed or deleted while the topic stayed declared,\n")
	b.WriteString("  or the topic should be retired from Terraform. A frozen source under\n")
	b.WriteString("  proto-frozen/ keeps an old-generation wire generated rather than hand-preserved.")
	return fmt.Errorf("%s", b.String())
}
