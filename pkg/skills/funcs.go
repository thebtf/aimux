package skills

import (
	"strings"
	"text/template"
)

// SkillFuncMap returns a template.FuncMap with helper functions that operate on SkillData.
func SkillFuncMap(data *SkillData) template.FuncMap {
	return template.FuncMap{
		// CallerHasSkill reports whether the caller possesses the named skill (case-insensitive).
		"CallerHasSkill": func(name string) bool {
			for _, s := range data.CallerSkills {
				if strings.EqualFold(s, name) {
					return true
				}
			}
			return false
		},

		// JoinCLIs returns all enabled CLIs joined by ", ".
		"JoinCLIs": func() string {
			return strings.Join(data.EnabledCLIs, ", ")
		},

		// RoleFor returns the CLI assigned to the given role, or "unknown" if not found.
		"RoleFor": func(role string) string {
			if cli, ok := data.RoleRouting[role]; ok {
				return cli
			}
			return "unknown"
		},

		// HasCLI reports whether the named CLI is in the enabled list (case-insensitive).
		"HasCLI": func(name string) bool {
			for _, cli := range data.EnabledCLIs {
				if strings.EqualFold(cli, name) {
					return true
				}
			}
			return false
		},
	}
}
