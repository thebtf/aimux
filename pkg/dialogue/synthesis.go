package dialogue

import (
	"fmt"
	"strings"
)

// synthesize produces a combined Synthesis from all turns in d.
// Agreement scoring is deferred to a future milestone (set to -1 for M3).
func synthesize(d *Dialogue) *Synthesis {
	var sb strings.Builder

	for _, t := range d.Turns {
		sb.WriteString(fmt.Sprintf("## %s (%s)\n\n%s\n\n", t.Participant, t.Role, t.Content))
	}

	return &Synthesis{
		Content:      sb.String(),
		Agreement:    -1, // Not computed in M3
		Participants: participantNames(d),
		TurnCount:    len(d.Turns),
	}
}

// participantNames returns a deduplicated, ordered list of participant names
// that actually contributed turns, preserving first-appearance order.
func participantNames(d *Dialogue) []string {
	seen := make(map[string]bool)
	names := make([]string, 0, len(d.Config.Participants))

	for _, t := range d.Turns {
		if !seen[t.Participant] {
			seen[t.Participant] = true
			names = append(names, t.Participant)
		}
	}

	return names
}
