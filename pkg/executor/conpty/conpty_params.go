// Package conpty — platform-neutral parameter types shared by the Windows
// real implementation and the non-Windows stub.
//
// Defined in a build-tag-free file so that callers (conpty.go) link against
// a single openParams type regardless of GOOS. The actual openWindowsConPTY
// function is defined per-platform in conpty_windows.go / conpty_other.go.
package conpty

// openParams carries the inputs needed to spawn a pseudo-console process.
// Lifted out of Executor.Run / .Start so the Windows real path and the
// non-Windows stub share a single openWindowsConPTY signature.
type openParams struct {
	command string
	args    []string
	cwd     string
	envList []string
	envMap  map[string]string
}

// defaultConPTYWidth / defaultConPTYHeight are the initial pseudo-console
// dimensions set on Open. 120×30 mirrors typical interactive terminal
// default; live resize-on-event is out of scope for CR-004 (spec — single
// sane default).
const (
	defaultConPTYWidth  = 120
	defaultConPTYHeight = 30
)
