package server

import (
	"io/fs"

	aimuxconfig "github.com/thebtf/aimux/config"
)

// skillsEmbedFS exposes the embedded config/skills.d filesystem to the server package.
// The embed directive lives in config/skillsembed.go, adjacent to skills.d, because
// Go's go:embed does not allow ".." in path patterns.
//
// Files are embedded under "skills.d/<name>/SKILL.md", alongside package support
// files and legacy flat files. The subtree gives the engine the skills.d root.
var skillsEmbedFS fs.FS = mustSubFS(aimuxconfig.SkillsFS, "skills.d")

func mustSubFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("skillsEmbedFS: cannot sub into " + dir + ": " + err.Error())
	}
	return sub
}
