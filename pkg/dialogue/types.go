// Package dialogue implements a participant-agnostic dialogue controller.
// It manages structured conversations between Participants — CLI executors,
// API executors, thinking patterns, or external agents. The controller
// operates entirely through the Participant interface and knows nothing
// about the underlying transport or execution mechanism.
package dialogue

// DialogueMode determines the turn-taking strategy for a dialogue session.
type DialogueMode int

const (
	// ModeSequential advances participants in insertion order, one at a time.
	ModeSequential DialogueMode = iota

	// ModeParallel dispatches all participants simultaneously and collects
	// their responses concurrently. One call to NextTurn completes the round.
	ModeParallel

	// ModeRoundRobin cycles through participants circularly, one per NextTurn call.
	ModeRoundRobin

	// ModeStance is like ModeSequential but prepends each participant's stance
	// to the prompt so they argue from an assigned position.
	ModeStance
)

// DialogueConfig configures a new dialogue session.
type DialogueConfig struct {
	// Participants is the ordered list of speakers. Must not be empty.
	Participants []Participant

	// Mode controls the turn-taking strategy.
	Mode DialogueMode

	// MaxTurns is the maximum total turns before the dialogue ends.
	// Zero means unlimited (caller must call Close to finish).
	MaxTurns int

	// Topic describes what the dialogue is about. Forwarded to participants
	// as context in the initial prompt.
	Topic string

	// Stances maps participant Name() → stance label (e.g., "pro", "con",
	// "neutral"). Only meaningful for ModeStance; ignored in other modes.
	Stances map[string]string

	// Synthesize controls whether Synthesize() is called automatically when
	// Close() is invoked. Defaults to true when the zero value is used by
	// callers who use NewDialogue directly.
	Synthesize bool
}

// DialogueStatus tracks the lifecycle of a Dialogue.
type DialogueStatus int

const (
	// StatusActive means the dialogue is open and accepting new turns.
	StatusActive DialogueStatus = iota

	// StatusCompleted means the dialogue finished normally (Close called or
	// MaxTurns reached).
	StatusCompleted

	// StatusFailed means the dialogue ended due to an unrecoverable error.
	StatusFailed
)

// String returns the human-readable status label.
func (s DialogueStatus) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// DialogueTurn records one participant's contribution during a dialogue.
type DialogueTurn struct {
	// Participant is the Name() of the speaker.
	Participant string

	// Role is the Role() of the speaker.
	Role string

	// Stance is the assigned stance (only set in ModeStance, empty otherwise).
	Stance string

	// Content is the response text produced by the participant.
	Content string

	// TurnNumber is a 1-based index of this turn within the dialogue.
	TurnNumber int
}

// Synthesis is the combined verdict produced from all dialogue turns.
type Synthesis struct {
	// Content is the synthesized text — one section per participant.
	Content string

	// Agreement is a 0.0-1.0 agreement score.
	// -1 means not computed (M3 — future enhancement).
	Agreement float64

	// Participants lists all participant names that contributed.
	Participants []string

	// TurnCount is the total number of turns included in the synthesis.
	TurnCount int
}

// Dialogue is an active dialogue session managed by the Controller.
// Fields are not goroutine-safe; callers must synchronise access when
// sharing a *Dialogue across goroutines.
type Dialogue struct {
	// ID uniquely identifies this dialogue within the controller.
	ID string

	// Config is the immutable configuration used to create this dialogue.
	Config DialogueConfig

	// Turns holds all completed turns in chronological order.
	Turns []DialogueTurn

	// Status is the current lifecycle state.
	Status DialogueStatus

	// Synthesis is set after a successful call to Controller.Synthesize.
	Synthesis *Synthesis

	// nextParticipantIdx tracks which participant speaks next (Sequential /
	// RoundRobin / Stance modes).
	nextParticipantIdx int

	// totalTurns is the running count used to enforce MaxTurns.
	totalTurns int
}
