// schemaguard — fail the build when an event proto's fields are not covered by
// the topic's COMMITTED Pub/Sub schema revision.
//
// Usage (from chora-contracts/):
//
//	bash scripts/verify-schema-registry.sh              # live check against GCP
//	bash scripts/verify-schema-registry.sh --refresh    # re-ground the snapshot
//
// Or directly:
//
//	GOWORK=off go run ./internal/schemaguard/cmd/schemaguard \
//	  -fds /tmp/chora-fds.binpb -flat proto/events-flat \
//	  -snapshot schema-registry/committed-schemas.json [-live -project chora-489812] [-refresh]
//
// Exit codes:
//
//	0  every repo event proto is covered by its committed schema revision
//	1  FATAL drift — a publish will be rejected with HTTP 400
//	2  the guard could not run (bad input); never a silent pass
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/api/pubsub/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/apollo-chora/chora-contracts/internal/schemaguard"
)

// snapshotEntry is one committed schema, frozen for hermetic CI.
type snapshotEntry struct {
	Schema     string `json:"schema"`
	RevisionID string `json:"revision_id"`
	Definition string `json:"definition"`
}

type snapshot struct {
	Project     string          `json:"project"`
	CapturedAt  string          `json:"captured_at"`
	Description string          `json:"description"`
	Schemas     []snapshotEntry `json:"schemas"`
}

func main() {
	var (
		fdsPath  = flag.String("fds", "", "path to the FileDescriptorSet built by `buf build proto` (required)")
		flatDir  = flag.String("flat", "proto/events-flat", "root of the flat protos (used to print an exact remediation)")
		snapPath = flag.String("snapshot", "schema-registry/committed-schemas.json", "checked-in snapshot of the committed registry")
		live     = flag.Bool("live", false, "fetch committed schemas from the Pub/Sub Schema Registry instead of the snapshot")
		refresh  = flag.Bool("refresh", false, "with -live: rewrite the snapshot from the registry")
		project  = flag.String("project", "chora-489812", "GCP project holding the Schema Registry")
	)
	flag.Parse()

	if *fdsPath == "" {
		fail("-fds is required (build it with: buf build proto --as-file-descriptor-set --output <path>)")
	}

	// --- side A: the repo, from the GENERATED descriptor ---------------------
	repo, err := loadRepoMessages(*fdsPath)
	if err != nil {
		fail("%v", err)
	}

	// --- side B: the committed registry --------------------------------------
	var entries []snapshotEntry
	if *live {
		entries, err = fetchLive(*project)
		if err != nil {
			fail("fetch live schema registry: %v", err)
		}
		if *refresh {
			if err := writeSnapshot(*snapPath, *project, entries); err != nil {
				fail("write snapshot: %v", err)
			}
			fmt.Printf("snapshot refreshed from live registry: %s (%d schemas)\n", *snapPath, len(entries))
		}
	} else {
		entries, err = readSnapshot(*snapPath)
		if err != nil {
			fail("read snapshot %s: %v\n\nRun with -live -refresh to create it.", *snapPath, err)
		}
	}
	if len(entries) == 0 {
		fail("0 committed schemas loaded — refusing to report a vacuous pass")
	}

	// Map SCHEMA NAME -> the flat proto that registers it, derived by parsing
	// the flat tree. Never a hand-written table.
	//
	// ⚠ The join key is the schema, NOT the message full-name (ruling 44). Two
	// generations of the same event legitimately share a full-name: the frozen
	// chora.consumption.v1.RitualPublished carries familiar_id at tag 3 (what
	// its live schema holds) while the current one carries companion_id. Joined
	// by full-name, whichever the map happened to hold was compared against the
	// other generation's schema and reported a break that does not exist.
	flatBySchema := indexFlatProtos(*flatDir)

	// --- compare -------------------------------------------------------------
	var (
		fatal    []schemaguard.Drift
		advisory []schemaguard.Drift
		checked  int
		orphaned []string
	)
	for _, e := range entries {
		committed, err := schemaguard.ParseSchemaDefinition(e.Definition)
		if err != nil {
			fail("parse committed schema %s: %v", e.Schema, err)
		}
		fe, ok := flatBySchema.lookup(e.Schema)
		if !ok {
			// No flat proto registers this schema: the registry holds an event
			// the repo does not produce.
			orphaned = append(orphaned, fmt.Sprintf("%s (%s)", e.Schema, committed.FullName()))
			continue
		}
		checked++

		d := schemaguard.Compare(fe.Msg, committed)
		d.SchemaName = e.Schema
		d.RevisionID = e.RevisionID
		d.FlatProtoPath = fe.Path

		switch {
		case d.Fatal():
			fatal = append(fatal, d)
		case d.HasDrift():
			advisory = append(advisory, d)
		}
	}

	if checked == 0 {
		fail("0 schemas matched a repo message — the join is broken, which would make this guard vacuously green")
	}

	// --- report --------------------------------------------------------------
	src := "snapshot " + *snapPath
	if *live {
		src = "LIVE registry " + *project
	}
	fmt.Printf("schemaguard: %d committed schemas (%s) vs %d repo messages — %d compared\n\n",
		len(entries), src, len(repo), checked)

	if len(advisory) > 0 {
		fmt.Printf("── ADVISORY: registry ahead of repo (%d) ─────────────────────────────\n", len(advisory))
		fmt.Println("   The committed revision carries fields the repo never declared — the")
		fmt.Println("   registry was hand-edited out-of-band. Publishing still works, but the")
		fmt.Println("   repo is no longer the source of truth and a regen would drop these.")
		fmt.Println()
		for _, d := range advisory {
			fmt.Println(indent(d.Report()))
		}
	}

	if len(orphaned) > 0 {
		sort.Strings(orphaned)
		fmt.Printf("── NOTE: %d committed schemas have no repo message (not compared) ────\n", len(orphaned))
		fmt.Println("   These are LIVE registry schemas the repo no longer declares a message for.")
		fmt.Println("   This is the SAFE drift direction and is NOT a failure — publishing is")
		fmt.Println("   unaffected. ⚠ Do NOT \"fix\" it by deleting them: a Pub/Sub schema may still")
		fmt.Println("   be bound to a live topic, and dropping it breaks every publish to that")
		fmt.Println("   topic. Reconcile by restoring the proto, or leave them alone.")
		fmt.Println()
		for _, o := range orphaned {
			fmt.Printf("   %s\n", o)
		}
		fmt.Println()
	}

	if len(fatal) == 0 {
		fmt.Println("✓ PASS — every repo event proto is covered by its committed schema revision.")
		return
	}

	fmt.Printf("── FATAL: repo ahead of registry (%d) ───────────────────────────────\n", len(fatal))
	fmt.Println("   These protos declare fields the committed schema revision DOES NOT HAVE.")
	fmt.Println("   Pub/Sub rejects such a message at publish with HTTP 400 (\"Message failed")
	fmt.Println("   schema validation\") — it never enters the topic and never dead-letters.")
	fmt.Println("   The break is data-dependent: it fires only once a producer POPULATES the")
	fmt.Println("   field, so it can sit armed for weeks and then surface as an outage.")
	fmt.Println()
	for _, d := range fatal {
		fmt.Println(indent(d.Report()))
	}
	fmt.Printf("%d event proto(s) are not covered by their committed schema revision.\n", len(fatal))
	os.Exit(1)
}

// loadRepoMessages reads the buf-built FileDescriptorSet. This is the
// "derive from the generated descriptor" half of the contract: no hand-written
// list of events exists anywhere in this tool.
func loadRepoMessages(path string) (map[string]schemaguard.Message, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read FileDescriptorSet: %w", err)
	}
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &fds); err != nil {
		return nil, fmt.Errorf("unmarshal FileDescriptorSet: %w", err)
	}
	return schemaguard.MessagesFromFDS(&fds)
}

// indexFlatProtos maps message full-name -> flat proto path by PARSING the flat
// tree, so the remediation command always names the right file even as the
// naming convention shifts.
//
// Paths are emitted relative to chora-contracts/ — the directory the operator
// runs the remediation from — regardless of whether -flat was given as an
// absolute or relative path. Printing a path that does not paste-and-run is how
// a guard loses the trust it needs to stay in the build.
type flatEntry struct {
	Path string
	Msg  schemaguard.Message
}

// normSchemaKey folds a schema name to a comparison key. Some schemas were
// registered live with a HYPHEN where the derivation keeps the event_type's
// UNDERSCORE (chora-consumption-familiar-persona-updated-v1), which Terraform
// carries as a schema_name_override. Same message either way, so the join
// treats the two separators as one.
func normSchemaKey(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", "-"))
}

// schemaNameForFlat mirrors Terraform's name derivation
// ("chora-{domain}-{aggregate}-{event_type}-v{N}") from the flat proto's path,
// so the join runs on the same key Terraform registers under.
func schemaNameForFlat(rel string) string {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	if len(parts) != 3 {
		return ""
	}
	event := strings.TrimSuffix(parts[2], ".proto")
	version := "v1"
	if strings.HasSuffix(event, ".v2") {
		event, version = strings.TrimSuffix(event, ".v2"), "v2"
	}
	return fmt.Sprintf("chora-%s-%s-%s-%s", parts[0], parts[1], event, version)
}

// flatIndex joins a committed schema to the flat proto that registers it.
//
// EXACT first, fuzzy only when unambiguous. The separator-folding fallback
// exists for the handful of schemas registered live with a HYPHEN where the
// derivation keeps an UNDERSCORE (chora-consumption-familiar-persona-updated-v1),
// but folding is NOT safe on its own: chora-consumption-kg-junction_detected-v1
// (aggregate "kg", event "junction_detected") and the flat proto
// consumption/kg_junction/detected.proto (aggregate "kg_junction", event
// "detected") fold to the SAME key while being different schemas. Folding alone
// joined that pair and invented eight field breaks. So a folded key is used
// only when exactly one flat proto claims it.
type flatIndex struct {
	exact  map[string]flatEntry
	folded map[string][]flatEntry
}

func (ix flatIndex) lookup(schema string) (flatEntry, bool) {
	if e, ok := ix.exact[schema]; ok {
		return e, true
	}
	if es := ix.folded[normSchemaKey(schema)]; len(es) == 1 {
		return es[0], true
	}
	return flatEntry{}, false
}

type aliasEntry struct {
	name string
	fe   flatEntry
}

func indexFlatProtos(root string) flatIndex {
	out := flatIndex{exact: map[string]flatEntry{}, folded: map[string][]flatEntry{}}
	var aliases []aliasEntry
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil //nolint:nilerr // a missing flat tree degrades the hint, not the verdict
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		m, perr := schemaguard.ParseSchemaDefinition(string(raw))
		if perr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		schema := schemaNameForFlat(rel)
		if schema == "" {
			return nil
		}
		fe := flatEntry{Path: filepath.Join("proto/events-flat", rel), Msg: m}
		out.exact[schema] = fe
		k := normSchemaKey(schema)
		out.folded[k] = append(out.folded[k], fe)
		// One flat proto can back a -v2 SCHEMA NAME while staying unsuffixed:
		// Terraform's observability_decision_v2 resource registers
		// chora-observability-agent_decision-logged-v2 from the ordinary
		// logged.proto (the additive field lives in the regular flat; only the
		// resource NAME carries -v2). Recorded as a deferred alias so a real
		// {event}.v2.proto always wins the exact key.
		if strings.HasSuffix(schema, "-v1") {
			aliases = append(aliases, aliasEntry{name: strings.TrimSuffix(schema, "-v1") + "-v2", fe: fe})
		}
		return nil
	})
	// Deferred: an alias never displaces a flat proto that claims the name itself.
	for _, a := range aliases {
		if _, taken := out.exact[a.name]; taken {
			continue
		}
		out.exact[a.name] = a.fe
		k := normSchemaKey(a.name)
		out.folded[k] = append(out.folded[k], a.fe)
	}
	return out
}

func fetchLive(project string) ([]snapshotEntry, error) {
	ctx := context.Background()
	svc, err := pubsub.NewService(ctx)
	if err != nil {
		return nil, err
	}
	var out []snapshotEntry
	parent := "projects/" + project
	err = svc.Projects.Schemas.List(parent).View("FULL").Pages(ctx, func(resp *pubsub.ListSchemasResponse) error {
		for _, s := range resp.Schemas {
			out = append(out, snapshotEntry{
				Schema:     s.Name[strings.LastIndex(s.Name, "/")+1:],
				RevisionID: s.RevisionId,
				Definition: s.Definition,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Schema < out[j].Schema })
	return out, nil
}

func readSnapshot(path string) ([]snapshotEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return s.Schemas, nil
}

func writeSnapshot(path, project string, entries []snapshotEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	s := snapshot{
		Project:    project,
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Description: "GENERATED — DO NOT EDIT. Snapshot of the COMMITTED GCP Pub/Sub schema revisions, " +
			"captured from deployed reality so CI can verify proto coverage without GCP credentials. " +
			"Refresh with: bash chora-contracts/scripts/verify-schema-registry.sh --refresh",
		Schemas: entries,
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func indent(s string) string {
	return "   " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n   ")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "schemaguard: "+format+"\n", args...)
	os.Exit(2)
}
