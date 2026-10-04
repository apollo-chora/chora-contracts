// Species-registry contract guards (CHO-2037).
//
// chora-contracts/companion/species_registry.json is the single canonical
// roster for Companion species. These tests pin the CompanionSpecies proto enum
// to it at every layer this repo owns: the generated Go stubs (what services
// compile against), the source proto (what codegen consumes), and the
// committed events-flat copies (what the Pub/Sub Schema Registry validates).
// Onboarding species N+1 without touching any one of these goes RED here
// (docs/references/familiar-species-onboarding.md).
package contracttest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	consumptionv1 "github.com/locoroco-git/Chora-LMS/chora-contracts/gen/go/chora/consumption/v1"
)

type registrySpecies struct {
	Key       string `json:"key"`
	WireValue int32  `json:"wire_value"`
	Status    string `json:"status"`
}

type speciesRegistryDoc struct {
	SchemaVersion int               `json:"schema_version"`
	Species       []registrySpecies `json:"species"`
}

// loadSpeciesRegistry walks up from this file until it finds the registry,
// returning the parsed doc plus the chora-contracts root (for locating the
// proto sources). Works from both the monorepo layout and a standalone
// chora-contracts checkout.
func loadSpeciesRegistry(t *testing.T) (speciesRegistryDoc, string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("species registry: runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		for _, c := range []struct{ path, contractsRoot string }{
			{filepath.Join(dir, "chora-contracts", "companion", "species_registry.json"), filepath.Join(dir, "chora-contracts")},
			{filepath.Join(dir, "companion", "species_registry.json"), dir},
		} {
			raw, err := os.ReadFile(c.path)
			if err != nil {
				continue
			}
			var doc speciesRegistryDoc
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("species registry %s: malformed JSON: %v", c.path, err)
			}
			if len(doc.Species) == 0 {
				t.Fatalf("species registry %s: empty species list", c.path)
			}
			return doc, c.contractsRoot
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("companion/species_registry.json not found walking up from the test dir — the canonical species roster is missing (see docs/references/familiar-species-onboarding.md)")
		}
		dir = parent
	}
}

// enumNameFor maps a registry key to its proto enum member name.
func enumNameFor(key string) string {
	return "COMPANION_SPECIES_" + strings.ToUpper(key)
}

func wantEnum(doc speciesRegistryDoc) map[string]int32 {
	want := map[string]int32{"COMPANION_SPECIES_UNSPECIFIED": 0}
	for _, s := range doc.Species {
		want[enumNameFor(s.Key)] = s.WireValue
	}
	return want
}

func diffEnums(got, want map[string]int32) []string {
	var problems []string
	for name, v := range want {
		g, ok := got[name]
		switch {
		case !ok:
			problems = append(problems, name+" missing")
		case g != v:
			problems = append(problems, name+" has wrong value")
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			problems = append(problems, name+" not in registry")
		}
	}
	sort.Strings(problems)
	return problems
}

func TestCompanionSpecies_GeneratedEnumMatchesRegistry(t *testing.T) {
	doc, _ := loadSpeciesRegistry(t)
	got := map[string]int32{}
	for name, v := range consumptionv1.CompanionSpecies_value {
		got[name] = v
	}
	if problems := diffEnums(got, wantEnum(doc)); len(problems) > 0 {
		t.Errorf("generated CompanionSpecies enum drifted from species registry: %v — edit proto/events/consumption/companion.proto, run protoflatten + codegen (docs/references/familiar-species-onboarding.md)", problems)
	}
}

var enumMemberRe = regexp.MustCompile(`COMPANION_SPECIES_([A-Z0-9_]+)\s*=\s*(\d+)\s*;`)

func parseProtoEnum(t *testing.T, path string) (map[string]int32, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read proto %s: %v", path, err)
	}
	if !strings.Contains(string(raw), "enum CompanionSpecies") {
		return nil, false
	}
	out := map[string]int32{}
	for _, m := range enumMemberRe.FindAllStringSubmatch(string(raw), -1) {
		var v int32
		for _, ch := range m[2] {
			v = v*10 + int32(ch-'0')
		}
		out["COMPANION_SPECIES_"+m[1]] = v
	}
	if len(out) == 0 {
		t.Fatalf("proto %s declares enum CompanionSpecies but no members parsed", path)
	}
	return out, true
}

func TestCompanionSpecies_ProtoSourcesMatchRegistry(t *testing.T) {
	doc, contractsRoot := loadSpeciesRegistry(t)
	want := wantEnum(doc)

	source := filepath.Join(contractsRoot, "proto", "events", "consumption", "companion.proto")
	got, hasEnum := parseProtoEnum(t, source)
	if !hasEnum {
		t.Fatalf("source proto %s no longer declares enum CompanionSpecies — update this guard to the new home", source)
	}
	if problems := diffEnums(got, want); len(problems) > 0 {
		t.Errorf("source proto %s drifted from species registry: %v", source, problems)
	}

	// The committed events-flat copies are what the Pub/Sub Schema Registry
	// actually validates — each inlined enum copy must match too.
	flatDir := filepath.Join(contractsRoot, "proto", "events-flat", "consumption", "companion")
	flats, err := filepath.Glob(filepath.Join(flatDir, "*.proto"))
	if err != nil || len(flats) == 0 {
		t.Fatalf("no flat protos under %s (err=%v) — protoflatten output moved; update this guard", flatDir, err)
	}
	sort.Strings(flats)
	withEnum := 0
	for _, f := range flats {
		got, hasEnum := parseProtoEnum(t, f)
		if !hasEnum {
			continue
		}
		withEnum++
		if problems := diffEnums(got, want); len(problems) > 0 {
			t.Errorf("flat proto %s drifted from species registry: %v — rerun protoflatten after editing the source proto", filepath.Base(f), problems)
		}
	}
	if withEnum < 6 {
		t.Errorf("only %d flat companion protos carry enum CompanionSpecies (want >=6) — flat copies missing or restructured", withEnum)
	}
}
