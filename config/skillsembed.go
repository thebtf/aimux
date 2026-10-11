// Package config provides embedded skill files for aimux.
// This file lives in config/ so go:embed can reference skills.d directly.
package config

import "embed"

// SkillsFS contains skill packages, including their support files, from config/skills.d.
//
//go:embed all:skills.d
var SkillsFS embed.FS
