package dialogue

import (
	"context"
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
)

// Participant is anything that can speak in a dialogue.
// Executors, thinking patterns, and external agents are all equal behind
// this interface — the Controller never inspects what is underneath.
type Participant interface {
	// Name returns a stable identifier for this participant (e.g., "codex",
	// "peer_review", "alice").
	Name() string

	// Role returns a human-readable role label for this participant
	// (e.g., "critic", "advocate", "moderator").
	Role() string

	// Respond is called once per turn. The participant receives the current
	// prompt and the full history of prior turns so it can build context.
	// It returns its response as a plain string.
	Respond(ctx context.Context, prompt string, history []DialogueTurn) (string, error)
}

// --- SwarmParticipant ---

// SwarmParticipant wraps a Swarm-managed ExecutorV2 as a Participant.
// It converts the dialogue prompt + history into a types.Message and
// delegates to Swarm.Send.
type SwarmParticipant struct {
	s      *swarm.Swarm
	handle *swarm.Handle
	name   string
	role   string
}

// NewSwarmParticipant creates a Participant backed by an executor managed in s.
// name is used as the participant identifier; role is the human-readable label.
func NewSwarmParticipant(s *swarm.Swarm, handle *swarm.Handle, name, role string) *SwarmParticipant {
	return &SwarmParticipant{
		s:      s,
		handle: handle,
		name:   name,
		role:   role,
	}
}

// Name implements Participant.
func (p *SwarmParticipant) Name() string { return p.name }

// Role implements Participant.
func (p *SwarmParticipant) Role() string { return p.role }

// Respond sends prompt (with history as conversation context) to the underlying
// executor via the Swarm and returns the response content.
func (p *SwarmParticipant) Respond(ctx context.Context, prompt string, history []DialogueTurn) (string, error) {
	msg := types.Message{
		Content: prompt,
		History: historyToTurns(history),
	}

	resp, err := p.s.Send(ctx, p.handle, msg)
	if err != nil {
		return "", fmt.Errorf("swarm participant %q: %w", p.name, err)
	}

	return resp.Content, nil
}

// historyToTurns converts DialogueTurn slice into types.Turn slice suitable
// for inclusion in a types.Message.History.
//
// Each turn is wrapped in XML-style <dialogue-turn> delimiters to prevent
// cross-participant prompt injection: a participant cannot fabricate a
// prior turn by emitting "[OtherName]: ..." in its output because the
// structured tags are hard to replicate accidentally in natural language.
// Turns are presented as role "user" so the LLM treats them as external
// context rather than its own prior output (prevents "assistant" role abuse).
func historyToTurns(history []DialogueTurn) []types.Turn {
	if len(history) == 0 {
		return nil
	}

	turns := make([]types.Turn, 0, len(history))
	for _, dt := range history {
		// Use structured XML-style delimiters resistant to injection.
		// %q quotes the participant name, escaping special characters.
		content := fmt.Sprintf("<dialogue-turn participant=%q role=%q>\n%s\n</dialogue-turn>",
			sanitizeName(dt.Participant), dt.Role, dt.Content)
		turns = append(turns, types.Turn{
			Role:    "user",
			Content: content,
		})
	}

	return turns
}

// buildStancePrompt prepends a stance declaration to the base prompt.
// Both participantName and stance are sanitized to strip control characters
// that could be used for prompt injection (e.g. "\n\nIgnore instructions...").
func buildStancePrompt(basePrompt, participantName, stance string) string {
	if stance == "" {
		return basePrompt
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You are %s. Your stance: %s.\n\n",
		sanitizeName(participantName), strings.ToUpper(sanitizeStance(stance))))
	sb.WriteString(basePrompt)
	return sb.String()
}
