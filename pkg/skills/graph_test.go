package skills

import (
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func boolPtr(b bool) *bool { return &b }

func makeMeta(name, desc string, related []string, isFragment bool) *SkillMeta {
	return &SkillMeta{
		Name:        name,
		Description: desc,
		Related:     related,
		IsFragment:  isFragment,
		Body:        "body",
	}
}

// ---- TestParseGraphMap ------------------------------------------------------

func TestParseGraphMap(t *testing.T) {
	raw := []byte(`
skills:
  debug:
    description: "Debug a problem"
    tools: [codex]
    escalates_to: [investigate]
    receives_from: [coding]
    related: [investigate]
    fragments: [_tools]
  investigate:
    description: "Deep investigation"
    tools: [gemini]
    escalates_to: [planner]
    receives_from: [debug]
    related: []
    fragments: []
fragments:
  _tools:
    description: "Tool usage fragment"
    used_by: [debug]
tool_usage:
  codex: [debug]
`)

	gm, err := ParseGraphMap(raw)
	if err != nil {
		t.Fatalf("ParseGraphMap: %v", err)
	}

	if len(gm.Skills) != 2 {
		t.Errorf("want 2 skills, got %d", len(gm.Skills))
	}
	if len(gm.Fragments) != 1 {
		t.Errorf("want 1 fragment, got %d", len(gm.Fragments))
	}

	dbg, ok := gm.Skills["debug"]
	if !ok {
		t.Fatal("skill 'debug' not found")
	}
	if dbg.Description != "Debug a problem" {
		t.Errorf("description mismatch: %q", dbg.Description)
	}
	if len(dbg.EscalatesTo) != 1 || dbg.EscalatesTo[0] != "investigate" {
		t.Errorf("escalates_to mismatch: %v", dbg.EscalatesTo)
	}
	if len(dbg.ReceivesFrom) != 1 || dbg.ReceivesFrom[0] != "coding" {
		t.Errorf("receives_from mismatch: %v", dbg.ReceivesFrom)
	}

	frag, ok := gm.Fragments["_tools"]
	if !ok {
		t.Fatal("fragment '_tools' not found")
	}
	if frag.Description != "Tool usage fragment" {
		t.Errorf("fragment description mismatch: %q", frag.Description)
	}

	users, ok := gm.ToolUsage["codex"]
	if !ok || len(users) != 1 || users[0] != "debug" {
		t.Errorf("tool_usage mismatch: %v", users)
	}
}

// ---- TestBuildBidirectionalGraph --------------------------------------------

// Scenario: A related:[B], B related:[C]
//   A → gets B as forward ref; nobody points back to A → A's list: [B]
//   B → gets C as forward ref; A points to B → B's list: [A, C]
//   C → no forward refs; B points to C → C's list: [B]
func TestBuildBidirectionalGraph(t *testing.T) {
	skills := map[string]*SkillMeta{
		"a": makeMeta("Skill A", "Desc A", []string{"b"}, false),
		"b": makeMeta("Skill B", "Desc B", []string{"c"}, false),
		"c": makeMeta("Skill C", "Desc C", nil, false),
	}

	graph := BuildBidirectionalGraph(skills)

	// Helper to extract names from RelatedSkill slice.
	names := func(slug string) []string {
		rs := graph[slug]
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.Name
		}
		return out
	}

	// A's forward ref is B; no reverse refs for A.
	aRels := names("a")
	if len(aRels) != 1 || aRels[0] != "Skill B" {
		t.Errorf("A relations: want [Skill B], got %v", aRels)
	}

	// B's forward ref is C; reverse ref is A.
	bRels := graph["b"]
	if len(bRels) != 2 {
		t.Errorf("B relations: want 2 entries, got %d: %v", len(bRels), bRels)
	}
	bNames := map[string]bool{}
	for _, r := range bRels {
		bNames[r.Name] = true
	}
	if !bNames["Skill A"] || !bNames["Skill C"] {
		t.Errorf("B relations: want Skill A + Skill C, got %v", bRels)
	}

	// C has no forward refs; B reverse-references C.
	cRels := names("c")
	if len(cRels) != 1 || cRels[0] != "Skill B" {
		t.Errorf("C relations: want [Skill B], got %v", cRels)
	}

	// Descriptions are populated from SkillMeta.
	for _, r := range graph["a"] {
		if r.Name == "Skill B" && r.Description != "Desc B" {
			t.Errorf("A→B description: want 'Desc B', got %q", r.Description)
		}
	}
}

// ---- TestValidateMap_AllGood ------------------------------------------------

func TestValidateMap_AllGood(t *testing.T) {
	gm := &GraphMap{
		Skills: map[string]GraphSkill{
			"debug": {
				Description:  "Debug",
				EscalatesTo:  []string{"investigate"},
				ReceivesFrom: []string{"coding"},
				Related:      []string{"investigate"},
			},
			"investigate": {
				Description:  "Investigate",
				EscalatesTo:  []string{"planner"},
				ReceivesFrom: []string{"debug"},
				Related:      []string{},
			},
		},
		Fragments: map[string]GraphFragment{},
		ToolUsage: map[string][]string{},
	}

	loaded := map[string]*SkillMeta{
		"debug":      makeMeta("Debug", "Debug a problem", []string{"investigate"}, false),
		"investigate": makeMeta("Investigate", "Deep investigation", nil, false),
	}

	warnings := ValidateMap(gm, loaded)
	if len(warnings) != 0 {
		t.Errorf("expected zero warnings, got: %v", warnings)
	}
}

// ---- TestValidateMap_MissingTemplate ----------------------------------------

func TestValidateMap_MissingTemplate(t *testing.T) {
	gm := &GraphMap{
		Skills: map[string]GraphSkill{
			"debug": {
				Description:  "Debug",
				EscalatesTo:  []string{"x"},
				ReceivesFrom: []string{"y"},
			},
		},
		Fragments: map[string]GraphFragment{},
		ToolUsage: map[string][]string{},
	}

	// "debug" is in the map but there's no loaded skill for it.
	loaded := map[string]*SkillMeta{}

	warnings := ValidateMap(gm, loaded)
	if !containsSubstr(warnings, `"debug" in map but no template found`) {
		t.Errorf("expected missing-template warning; got: %v", warnings)
	}
}

// ---- TestValidateMap_P20_NoEscalation ---------------------------------------

func TestValidateMap_P20_NoEscalation(t *testing.T) {
	gm := &GraphMap{
		Skills: map[string]GraphSkill{
			"coding": {
				Description:  "Coding",
				EscalatesTo:  []string{}, // empty — P20 violation
				ReceivesFrom: []string{"planner"},
			},
		},
		Fragments: map[string]GraphFragment{},
		ToolUsage: map[string][]string{},
	}

	loaded := map[string]*SkillMeta{
		"coding": makeMeta("Coding", "Write code", nil, false),
	}

	warnings := ValidateMap(gm, loaded)
	if !containsSubstr(warnings, `"coding" has no escalation target (P20 violation)`) {
		t.Errorf("expected P20 escalation warning; got: %v", warnings)
	}
}

// ---- TestValidateMap_P20_NoReceives -----------------------------------------

func TestValidateMap_P20_NoReceives(t *testing.T) {
	gm := &GraphMap{
		Skills: map[string]GraphSkill{
			"planner": {
				Description:  "Planner",
				EscalatesTo:  []string{"coding"},
				ReceivesFrom: []string{}, // empty — P20 violation
			},
		},
		Fragments: map[string]GraphFragment{},
		ToolUsage: map[string][]string{},
	}

	loaded := map[string]*SkillMeta{
		"planner": makeMeta("Planner", "Plan a task", nil, false),
	}

	warnings := ValidateMap(gm, loaded)
	if !containsSubstr(warnings, `"planner" receives from nobody (P20 violation)`) {
		t.Errorf("expected P20 receives warning; got: %v", warnings)
	}
}

// ---- TestValidateMap_RelatedMismatch ----------------------------------------

func TestValidateMap_RelatedMismatch(t *testing.T) {
	// The .md frontmatter says related:[extra], but the map's Related for "debug"
	// does not include "extra".
	gm := &GraphMap{
		Skills: map[string]GraphSkill{
			"debug": {
				Description:  "Debug",
				EscalatesTo:  []string{"investigate"},
				ReceivesFrom: []string{"coding"},
				Related:      []string{"investigate"}, // "extra" is NOT listed
			},
		},
		Fragments: map[string]GraphFragment{},
		ToolUsage: map[string][]string{},
	}

	loaded := map[string]*SkillMeta{
		"debug": makeMeta("Debug", "Debug a problem", []string{"investigate", "extra"}, false),
	}

	warnings := ValidateMap(gm, loaded)
	if !containsSubstr(warnings, `"debug": related "extra" in frontmatter but not in map`) {
		t.Errorf("expected related-mismatch warning; got: %v", warnings)
	}
	// "investigate" IS in the map, so no warning for it.
	if containsSubstr(warnings, `"debug": related "investigate"`) {
		t.Errorf("unexpected warning for 'investigate': %v", warnings)
	}
}

// ---- helpers ----------------------------------------------------------------

// containsSubstr reports whether any string in ss contains sub.
func containsSubstr(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
