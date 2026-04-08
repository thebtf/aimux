package server

import (
	"io/fs"

	aimuxconfig "github.com/thebtf/aimux/config"
)

// skillsEmbedFS exposes the embedded config/skills.d filesystem to the server package.
// The embed directive lives in config/skillsembed.go, adjacent to skills.d, because
// Go's go:embed does not allow ".." in path patterns.
//
// The embedded FS is rooted at the module root, so files are accessible under
// "skills.d/<name>.md". We sub-tree it so the engine sees bare filenames.
var skillsEmbedFS fs.FS = mustSubFS(aimuxconfig.SkillsFS, "skills.d")

func mustSubFS(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("skillsEmbedFS: cannot sub into " + dir + ": " + err.Error())
	}
	return sub
}
