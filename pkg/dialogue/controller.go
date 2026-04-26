package dialogue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// Controller is the dialogue session factory and turn executor.
// It is safe to call from multiple goroutines. Each Dialogue produced
// by NewDialogue is NOT goroutine-safe — callers own synchronisation.
type Controller struct {
	nextID atomic.Uint64
}

// New creates a ready-to-use Controller.
func New() *Controller {
	return &Controller{}
}

// NewDialogue validates config and creates a new Dialogue session.
// Returns an error if config is invalid (e.g., no participants).
func (c *Controller) NewDialogue(config DialogueConfig) (*Dialogue, error) {
	if len(config.Participants) == 0 {
		return nil, errors.New("dialogue: at least one participant is required")
	}

	// Default MaxTurns: for unlimited callers pass 0, which we preserve.
	// Parallel mode with 0 MaxTurns: one round only (all participants once).

	id := c.nextID.Add(1)

	return &Dialogue{
		ID:                 fmt.Sprintf("dlg-%d", id),
		Config:             config,
		Turns:              nil,
		Status:             StatusActive,
		Synthesis:          nil,
		nextParticipantIdx: 0,
		totalTurns:         0,
	}, nil
}

// NextTurn executes the next turn(s) based on the dialogue mode.
//
// Returns ([]DialogueTurn, nil) with one or more turns appended to d.Turns.
// Returns (nil, nil) when the dialogue is complete (MaxTurns reached or all
// participants have spoken in Parallel mode with no further rounds).
// Returns (nil, err) on participant failure.
//
// The dialogue Status is updated to StatusCompleted when done.
func (c *Controller) NextTurn(ctx context.Context, d *Dialogue) ([]DialogueTurn, error) {
	if d.Status != StatusActive {
		return nil, nil
	}

	// Check global turn limit.
	if d.Config.MaxTurns > 0 && d.totalTurns >= d.Config.MaxTurns {
		d.Status = StatusCompleted
		return nil, nil
	}

	switch d.Config.Mode {
	case ModeSequential:
		return c.nextTurnSequential(ctx, d)
	case ModeParallel:
		return c.nextTurnParallel(ctx, d)
	case ModeRoundRobin:
		return c.nextTurnRoundRobin(ctx, d)
	case ModeStance:
		return c.nextTurnStance(ctx, d)
	default:
		return nil, fmt.Errorf("dialogue: unknown mode %d", d.Config.Mode)
	}
}

// Synthesize produces a combined verdict from all turns accumulated so far.
// It may be called multiple times; each call regenerates the Synthesis from
// the current turn history.
func (c *Controller) Synthesize(_ context.Context, d *Dialogue) (*Synthesis, error) {
	if len(d.Turns) == 0 {
		return nil, errors.New("dialogue: no turns to synthesize")
	}

	s := synthesize(d)
	d.Synthesis = s
	return s, nil
}

// Close marks the dialogue as completed. Idempotent — safe to call multiple times.
func (c *Controller) Close(d *Dialogue) error {
	if d == nil {
		return errors.New("dialogue: cannot close nil dialogue")
	}
	d.Status = StatusCompleted
	return nil
}

// --- mode implementations ---

// nextTurnSequential picks the next participant in insertion order and calls Respond.
// When all participants have spoken once, the cycle ends.
func (c *Controller) nextTurnSequential(ctx context.Context, d *Dialogue) ([]DialogueTurn, error) {
	participants := d.Config.Participants

	// All participants have spoken — done.
	if d.nextParticipantIdx >= len(participants) {
		d.Status = StatusCompleted
		return nil, nil
	}

	p := participants[d.nextParticipantIdx]
	turn, err := c.callParticipant(ctx, d, p, "")
	if err != nil {
		d.Status = StatusFailed
		return nil, err
	}

	d.nextParticipantIdx++
	d.totalTurns++
	d.Turns = append(d.Turns, turn)

	// Check if we just finished the last participant.
	if d.nextParticipantIdx >= len(participants) {
		if d.Config.MaxTurns == 0 {
			d.Status = StatusCompleted
		}
	}

	return []DialogueTurn{turn}, nil
}

// nextTurnParallel dispatches all participants concurrently and collects responses.
// After one parallel round, the dialogue completes (unless MaxTurns allows more).
func (c *Controller) nextTurnParallel(ctx context.Context, d *Dialogue) ([]DialogueTurn, error) {
	participants := d.Config.Participants
	n := len(participants)

	type result struct {
		turn DialogueTurn
		err  error
	}

	results := make([]result, n)
	var wg sync.WaitGroup

	// Snapshot history before any of this round's turns are added.
	historySnapshot := make([]DialogueTurn, len(d.Turns))
	copy(historySnapshot, d.Turns)

	for i, p := range participants {
		wg.Add(1)
		go func(idx int, participant Participant) {
			defer wg.Done()
			turn, err := c.callParticipantWithHistory(ctx, d, participant, "", historySnapshot)
			results[idx] = result{turn: turn, err: err}
		}(i, p)
	}

	wg.Wait()

	// Collect results in participant order; abort on first error.
	var turns []DialogueTurn
	for _, r := range results {
		if r.err != nil {
			d.Status = StatusFailed
			return nil, r.err
		}
		turns = append(turns, r.turn)
	}

	d.totalTurns += n
	d.Turns = append(d.Turns, turns...)

	// After a parallel round, check MaxTurns. If unlimited or limit not yet
	// reached, mark completed (parallel = one round unless caller loops).
	if d.Config.MaxTurns == 0 || d.totalTurns >= d.Config.MaxTurns {
		d.Status = StatusCompleted
	}

	return turns, nil
}

// nextTurnRoundRobin cycles through participants, one per call to NextTurn.
// Unlike Sequential, it does not stop after all participants have spoken once —
// it continues until MaxTurns is reached or Close is called.
func (c *Controller) nextTurnRoundRobin(ctx context.Context, d *Dialogue) ([]DialogueTurn, error) {
	participants := d.Config.Participants
	p := participants[d.nextParticipantIdx%len(participants)]

	turn, err := c.callParticipant(ctx, d, p, "")
	if err != nil {
		d.Status = StatusFailed
		return nil, err
	}

	d.nextParticipantIdx++
	d.totalTurns++
	d.Turns = append(d.Turns, turn)

	if d.Config.MaxTurns > 0 && d.totalTurns >= d.Config.MaxTurns {
		d.Status = StatusCompleted
	}

	return []DialogueTurn{turn}, nil
}

// nextTurnStance behaves like Sequential but prepends each participant's stance
// to the prompt before calling Respond.
func (c *Controller) nextTurnStance(ctx context.Context, d *Dialogue) ([]DialogueTurn, error) {
	participants := d.Config.Participants

	if d.nextParticipantIdx >= len(participants) {
		d.Status = StatusCompleted
		return nil, nil
	}

	p := participants[d.nextParticipantIdx]
	stance := ""
	if d.Config.Stances != nil {
		stance = d.Config.Stances[p.Name()]
	}

	turn, err := c.callParticipant(ctx, d, p, stance)
	if err != nil {
		d.Status = StatusFailed
		return nil, err
	}

	d.nextParticipantIdx++
	d.totalTurns++
	d.Turns = append(d.Turns, turn)

	if d.nextParticipantIdx >= len(participants) {
		if d.Config.MaxTurns == 0 {
			d.Status = StatusCompleted
		}
	}

	return []DialogueTurn{turn}, nil
}

// --- helpers ---

// callParticipant invokes p.Respond with the current dialogue state as history.
// stance is prepended to the topic prompt when non-empty.
func (c *Controller) callParticipant(ctx context.Context, d *Dialogue, p Participant, stance string) (DialogueTurn, error) {
	return c.callParticipantWithHistory(ctx, d, p, stance, d.Turns)
}

// callParticipantWithHistory is like callParticipant but uses an explicit
// history snapshot (needed for parallel mode to avoid data races).
func (c *Controller) callParticipantWithHistory(ctx context.Context, d *Dialogue, p Participant, stance string, history []DialogueTurn) (DialogueTurn, error) {
	prompt := d.Config.Topic
	if stance != "" {
		prompt = buildStancePrompt(d.Config.Topic, p.Name(), stance)
	}

	content, err := p.Respond(ctx, prompt, history)
	if err != nil {
		return DialogueTurn{}, fmt.Errorf("participant %q failed: %w", p.Name(), err)
	}

	turn := DialogueTurn{
		Participant: p.Name(),
		Role:        p.Role(),
		Stance:      stance,
		Content:     content,
		TurnNumber:  d.totalTurns + 1,
	}

	return turn, nil
}
