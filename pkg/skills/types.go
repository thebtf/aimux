package skills

import "strings"

// ArgDef describes a single MCP prompt argument.
type ArgDef struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// SkillMeta holds parsed YAML frontmatter for a skill file.
type SkillMeta struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Prompt      *bool    `yaml:"prompt"`
	Args        []ArgDef `yaml:"args"`
	Related     []string `yaml:"related"`
	Tags        []string `yaml:"tags"`

	// Not serialized — populated by the loader.
	FilePath   string `yaml:"-"`
	IsFragment bool   `yaml:"-"`
	Body       string `yaml:"-"`
}

// IsPrompt reports whether this skill should be exposed as an MCP prompt.
// Defaults to true when the Prompt field is omitted.
func (m *SkillMeta) IsPrompt() bool {
	return m.Prompt == nil || *m.Prompt
}

// SkillData carries runtime data injected into skill templates.
type SkillData struct {
	EnabledCLIs     []string
	CLICount        int
	HasMultipleCLIs bool
	HasGemini       bool
	RoleRouting     map[string]string
	TotalRequests   int64
	ErrorRate       float64
	PastReports     []ReportInfo
	Agents          []AgentInfo
	ThinkPatterns   []string
	CallerSkills    []string
	RelatedSkills   []RelatedSkill
	Args            map[string]string
}

// CallerHasSkill reports whether the caller's skill list contains name
// (case-insensitive).
func (d *SkillData) CallerHasSkill(name string) bool {
	for _, s := range d.CallerSkills {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

// ReportInfo describes a past investigation report available for recall.
type ReportInfo struct {
	Topic    string
	Date     string
	Filename string
}

// AgentInfo describes a registered agent.
type AgentInfo struct {
	Name        string
	Description string
	Role        string
}

// RelatedSkill is a lightweight reference to a related skill.
type RelatedSkill struct {
	Name        string
	Description string
}
