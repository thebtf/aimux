package picker

import "github.com/thebtf/aimux/pkg/executor/types"

const (
	familyOpenAI    = "openai"
	familyAnthropic = "anthropic"
	familyGoogle    = "google"
)

var cliFamilies = map[types.CLIName]string{
	"codex":  familyOpenAI,
	"claude": familyAnthropic,
	"gemini": familyGoogle,
}

var defaultPairNavigator = map[types.CLIName]types.CLIName{
	"codex":  "claude",
	"claude": "gemini",
	"gemini": "codex",
}

// FamilyOf returns the configured provider family for a CLI.
func FamilyOf(cli types.CLIName) (string, bool) {
	family, ok := cliFamilies[cli]
	return family, ok
}

func sameFamily(a, b types.CLIName) bool {
	familyA, okA := FamilyOf(a)
	familyB, okB := FamilyOf(b)
	return okA && okB && familyA == familyB
}
