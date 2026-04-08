package skills

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// GraphMap represents the parsed _map.yaml skill graph.
type GraphMap struct {
	Skills    map[string]GraphSkill    `yaml:"skills"`
	Fragments map[string]GraphFragment `yaml:"fragments"`
	ToolUsage map[string][]string      `yaml:"tool_usage"`
}

// GraphSkill describes a skill node in the map.
type GraphSkill struct {
	Description  string   `yaml:"description"`
	Tools        []string `yaml:"tools"`
	Phases       []string `yaml:"phases"`
	Related      []string `yaml:"related"`
	Fragments    []string `yaml:"fragments"`
	EscalatesTo  []string `yaml:"escalates_to"`
	ReceivesFrom []string `yaml:"receives_from"`
}

// GraphFragment describes a fragment node in the map.
type GraphFragment struct {
	Description string   `yaml:"description"`
	UsedBy      []string `yaml:"used_by"`
}

// ParseGraphMap unmarshals YAML data into a GraphMap.
func ParseGraphMap(data []byte) (*GraphMap, error) {
	var gm GraphMap
	if err := yaml.Unmarshal(data, &gm); err != nil {
		return nil, fmt.Errorf("parse graph map: %w", err)
	}
	if gm.Skills == nil {
		gm.Skills = make(map[string]GraphSkill)
	}
	if gm.Fragments == nil {
		gm.Fragments = make(map[string]GraphFragment)
	}
	if gm.ToolUsage == nil {
		gm.ToolUsage = make(map[string][]string)
	}
	return &gm, nil
}

// BuildBidirectionalGraph computes for each skill slug the union of:
//   - forward refs:  skills listed in that skill's Related field
//   - reverse refs:  skills that list THIS slug in their Related field
//
// Duplicates are removed. Each RelatedSkill is populated with the Name and
// Description from the corresponding SkillMeta (if found).
func BuildBidirectionalGraph(skills map[string]*SkillMeta) map[string][]RelatedSkill {
	// Collect reverse references: for each skill B listed in A's Related,
	// record B → A (B is referenced by A).
	reverseRefs := make(map[string][]string) // slug → slugs that reference it
	for slug, meta := range skills {
		for _, rel := range meta.Related {
			reverseRefs[rel] = append(reverseRefs[rel], slug)
		}
	}

	result := make(map[string][]RelatedSkill, len(skills))

	for slug, meta := range skills {
		// Use a set to deduplicate slugs.
		seen := make(map[string]bool)
		var refs []string

		// Forward refs from this skill's Related field.
		for _, rel := range meta.Related {
			if rel != slug && !seen[rel] {
				seen[rel] = true
				refs = append(refs, rel)
			}
		}

		// Reverse refs: skills that reference this one.
		for _, rev := range reverseRefs[slug] {
			if rev != slug && !seen[rev] {
				seen[rev] = true
				refs = append(refs, rev)
			}
		}

		// Sort for deterministic output.
		sort.Strings(refs)

		related := make([]RelatedSkill, 0, len(refs))
		for _, ref := range refs {
			rs := RelatedSkill{Name: ref} // fallback: use slug as name
			if rm, ok := skills[ref]; ok {
				rs.Name = rm.Name
				rs.Description = rm.Description
			}
			related = append(related, rs)
		}

		result[slug] = related
	}

	return result
}

// ValidateMap cross-checks a parsed GraphMap against the set of loaded SkillMeta
// entries and returns a (possibly empty) slice of human-readable warning strings.
//
// Checks performed:
//   - Skill in map but no .md loaded
//   - .md loaded but not in map (non-fragments only)
//   - Fragment in map but no .md loaded
//   - Skill in map with empty escalates_to (P20 violation)
//   - Skill in map with empty receives_from (P20 violation)
//   - Related slug listed in .md frontmatter but not in map's Related list for that skill
func ValidateMap(gm *GraphMap, loadedSkills map[string]*SkillMeta) []string {
	var warnings []string

	// Map skills present in loadedSkills for quick lookup.
	loaded := func(slug string) bool {
		_, ok := loadedSkills[slug]
		return ok
	}

	// 1. Skill in map but no .md loaded.
	for slug := range gm.Skills {
		if !loaded(slug) {
			warnings = append(warnings, fmt.Sprintf("skill %q in map but no template found", slug))
		}
	}

	// 2. .md loaded but not in map (skip fragments).
	for slug, meta := range loadedSkills {
		if meta.IsFragment {
			continue
		}
		if _, inMap := gm.Skills[slug]; !inMap {
			warnings = append(warnings, fmt.Sprintf("skill %q has template but missing from map", slug))
		}
	}

	// 3. Fragment in map but no .md.
	for slug := range gm.Fragments {
		if !loaded(slug) {
			warnings = append(warnings, fmt.Sprintf("fragment %q in map but no template found", slug))
		}
	}

	// 4 & 5. P20 violations.
	for slug, gs := range gm.Skills {
		if len(gs.EscalatesTo) == 0 {
			warnings = append(warnings, fmt.Sprintf("skill %q has no escalation target (P20 violation)", slug))
		}
		if len(gs.ReceivesFrom) == 0 {
			warnings = append(warnings, fmt.Sprintf("skill %q receives from nobody (P20 violation)", slug))
		}
	}

	// 6. Related in .md frontmatter not matching map's Related list for that skill.
	for slug, meta := range loadedSkills {
		gs, inMap := gm.Skills[slug]
		if !inMap {
			continue // already warned above (or it's a fragment)
		}
		mapRelated := make(map[string]bool, len(gs.Related))
		for _, r := range gs.Related {
			mapRelated[r] = true
		}
		for _, rel := range meta.Related {
			if !mapRelated[rel] {
				warnings = append(warnings, fmt.Sprintf("skill %q: related %q in frontmatter but not in map", slug, rel))
			}
		}
	}

	sort.Strings(warnings)
	return warnings
}
