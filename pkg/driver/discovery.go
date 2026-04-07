package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// DiscoverBinary searches for a CLI binary beyond just PATH.
// Search order: PATH → well-known dirs → version manager dirs → profile search_paths.
// Returns the full path if found, or empty string if not.
func DiscoverBinary(name string, profileSearchPaths []string) string {
	// Level 0: standard PATH lookup (fastest)
	if path, err := exec.LookPath(name); err == nil {
		return path
	}

	// Level 1: well-known installation directories per platform
	for _, dir := range wellKnownDirs() {
		if p := probeInDir(dir, name); p != "" {
			return p
		}
	}

	// Level 2: version manager directories (glob patterns)
	for _, pattern := range versionManagerGlobs() {
		expanded := os.ExpandEnv(pattern)
		matches, err := filepath.Glob(expanded)
		if err != nil {
			continue
		}
		for _, dir := range matches {
			if p := probeInDir(dir, name); p != "" {
				return p
			}
		}
	}

	// Level 3: profile-specific search paths from YAML config
	for _, pattern := range profileSearchPaths {
		expanded := os.ExpandEnv(pattern)
		matches, err := filepath.Glob(expanded)
		if err != nil {
			// Not a glob — try as literal directory
			if p := probeInDir(expanded, name); p != "" {
				return p
			}
			continue
		}
		for _, dir := range matches {
			if p := probeInDir(dir, name); p != "" {
				return p
			}
		}
	}

	return ""
}

// probeInDir checks if a binary exists in the given directory.
func probeInDir(dir, name string) string {
	candidates := binaryCandidates(name)
	for _, candidate := range candidates {
		path := filepath.Join(dir, candidate)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		// On Unix, check executable bit
		if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
			continue
		}
		return path
	}
	return ""
}

// binaryCandidates returns possible filenames for a binary name.
// On Windows, appends common extensions (.exe, .cmd, .bat).
func binaryCandidates(name string) []string {
	if runtime.GOOS == "windows" {
		// If name already has extension, use as-is too
		if strings.Contains(name, ".") {
			return []string{name}
		}
		return []string{name + ".exe", name + ".cmd", name + ".bat", name + ".ps1", name}
	}
	return []string{name}
}

// wellKnownDirs returns platform-specific directories where package managers install binaries.
func wellKnownDirs() []string {
	home := homeDir()
	var dirs []string

	// Cross-platform
	dirs = append(dirs,
		filepath.Join(home, ".local", "bin"),        // pip --user, pipx
		filepath.Join(home, ".cargo", "bin"),         // cargo install
		filepath.Join(home, "go", "bin"),             // go install
		filepath.Join(home, ".deno", "bin"),          // deno install
	)

	switch runtime.GOOS {
	case "windows":
		appdata := os.Getenv("APPDATA")
		localAppdata := os.Getenv("LOCALAPPDATA")
		if appdata != "" {
			dirs = append(dirs, filepath.Join(appdata, "npm"))              // npm -g on Windows
			dirs = append(dirs, filepath.Join(appdata, "Python", "Scripts")) // pip on Windows
		}
		if localAppdata != "" {
			dirs = append(dirs, filepath.Join(localAppdata, "Programs"))
		}
		// GOPATH/bin on Windows
		if gopath := os.Getenv("GOPATH"); gopath != "" {
			dirs = append(dirs, filepath.Join(gopath, "bin"))
		}

	case "darwin":
		dirs = append(dirs,
			"/opt/homebrew/bin",   // Homebrew Apple Silicon
			"/usr/local/bin",      // Homebrew Intel + manual installs
		)

	case "linux":
		dirs = append(dirs,
			"/usr/local/bin",                                // manual installs, go tarball
			"/snap/bin",                                     // snap packages
			filepath.Join(home, ".nix-profile", "bin"),      // nix
			"/home/linuxbrew/.linuxbrew/bin",                // linuxbrew
		)
	}

	return dirs
}

// versionManagerGlobs returns glob patterns for version manager binary directories.
func versionManagerGlobs() []string {
	home := homeDir()

	return []string{
		// Node version managers
		filepath.Join(home, ".nvm", "versions", "node", "*", "bin"),
		filepath.Join(home, ".volta", "bin"),
		filepath.Join(home, ".fnm", "node-versions", "*", "installation", "bin"),

		// Python version managers
		filepath.Join(home, ".pyenv", "shims"),
		filepath.Join(home, ".pyenv", "versions", "*", "bin"),

		// Multi-language version managers
		filepath.Join(home, ".local", "share", "mise", "installs", "*", "*", "bin"),
		filepath.Join(home, ".asdf", "shims"),
	}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}
