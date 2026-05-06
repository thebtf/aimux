package harness

type CognitiveMove struct {
	Name           string    `json:"name"`
	Pattern        string    `json:"pattern"`
	Group          MoveGroup `json:"group"`
	Purpose        string    `json:"purpose"`
	RequiredInputs []string  `json:"required_inputs,omitempty"`
	DomainWorkflow string    `json:"domain_workflow,omitempty"`
}

type MoveRecommendation struct {
	Name                  string    `json:"name"`
	Pattern               string    `json:"pattern"`
	Group                 MoveGroup `json:"group"`
	Reason                string    `json:"reason"`
	ExpectedArtifactDelta string    `json:"expected_artifact_delta"`
}

type MoveCatalog struct {
	moves []CognitiveMove
}

func NewDefaultMoveCatalog() MoveCatalog {
	return MoveCatalog{moves: []CognitiveMove{
		{Name: "problem_decomposition", Pattern: "problem_decomposition", Group: MoveGroupFrame, Purpose: "Break the task into tractable sub-problems.", RequiredInputs: []string{"problem"}},
		{Name: "domain_modeling", Pattern: "domain_modeling", Group: MoveGroupFrame, Purpose: "Identify entities, relationships, rules, and constraints.", RequiredInputs: []string{"domainName"}},
		{Name: "recursive_thinking", Pattern: "recursive_thinking", Group: MoveGroupFrame, Purpose: "Model self-similar nested problem structure.", RequiredInputs: []string{"problem"}},

		{Name: "architecture_analysis", Pattern: "architecture_analysis", Group: MoveGroupExplore, Purpose: "Inspect structural coupling and tradeoffs.", RequiredInputs: []string{"components"}},
		{Name: "debugging_approach", Pattern: "debugging_approach", Group: MoveGroupExplore, Purpose: "Narrow a fault with hypotheses and observations.", RequiredInputs: []string{"issue"}},
		{Name: "literature_review", Pattern: "literature_review", Group: MoveGroupExplore, Purpose: "Survey sources and identify themes or gaps.", RequiredInputs: []string{"topic"}},
		{Name: "mental_model", Pattern: "mental_model", Group: MoveGroupExplore, Purpose: "Apply a different perspective to the same problem.", RequiredInputs: []string{"modelName", "problem"}},
		{Name: "temporal_thinking", Pattern: "temporal_thinking", Group: MoveGroupExplore, Purpose: "Analyze states, events, and transitions over time.", RequiredInputs: []string{"timeFrame"}},
		{Name: "visual_reasoning", Pattern: "visual_reasoning", Group: MoveGroupExplore, Purpose: "Reason about visual or spatial structures.", RequiredInputs: []string{"operation"}},

		{Name: "experimental_loop", Pattern: "experimental_loop", Group: MoveGroupTest, Purpose: "Iterate through hypothesis, test, measure, and adjust.", RequiredInputs: []string{"hypothesis"}},
		{Name: "replication_analysis", Pattern: "replication_analysis", Group: MoveGroupTest, Purpose: "Check whether a claim or method can be reproduced.", RequiredInputs: []string{"claim"}},
		{Name: "scientific_method", Pattern: "scientific_method", Group: MoveGroupTest, Purpose: "Use observation, hypothesis, experiment, analysis, and conclusion.", RequiredInputs: []string{"stage"}},
		{Name: "source_comparison", Pattern: "source_comparison", Group: MoveGroupTest, Purpose: "Compare sources for agreement, disagreement, and uncertainty.", RequiredInputs: []string{"topic", "sources"}},

		{Name: "critical_thinking", Pattern: "critical_thinking", Group: MoveGroupEvaluate, Purpose: "Look for bias, weak assumptions, and missing counter-evidence.", RequiredInputs: []string{"issue"}},
		{Name: "decision_framework", Pattern: "decision_framework", Group: MoveGroupEvaluate, Purpose: "Score options against weighted criteria.", RequiredInputs: []string{"decision"}},
		{Name: "peer_review", Pattern: "peer_review", Group: MoveGroupEvaluate, Purpose: "Stress-test a proposed solution from a reviewer stance.", RequiredInputs: []string{"artifact"}},
		{Name: "structured_argumentation", Pattern: "structured_argumentation", Group: MoveGroupEvaluate, Purpose: "Separate claims, evidence, rebuttals, and severity.", RequiredInputs: []string{"argument"}},

		{Name: "collaborative_reasoning", Pattern: "collaborative_reasoning", Group: MoveGroupCalibrate, Purpose: "Expose the task to multiple explicit perspectives.", RequiredInputs: []string{"stage", "contribution"}},
		{Name: "metacognitive_monitoring", Pattern: "metacognitive_monitoring", Group: MoveGroupCalibrate, Purpose: "Check overconfidence, uncertainty, and reasoning quality.", RequiredInputs: []string{"task"}},
		{Name: "stochastic_algorithm", Pattern: "stochastic_algorithm", Group: MoveGroupCalibrate, Purpose: "Analyze probabilistic decisions and uncertainty models.", RequiredInputs: []string{"algorithmType", "problemDefinition"}},

		{Name: "research_synthesis", Pattern: "research_synthesis", Group: MoveGroupFinalize, Purpose: "Integrate findings into supported claims.", RequiredInputs: []string{"topic", "findings"}},
		{Name: "sequential_thinking", Pattern: "sequential_thinking", Group: MoveGroupFinalize, Purpose: "Walk the final chain step by step and revise if needed.", RequiredInputs: []string{"thought", "thoughtNumber", "totalThoughts"}},
	}}
}

func (c MoveCatalog) AllMoves() []CognitiveMove {
	out := make([]CognitiveMove, len(c.moves))
	for i, move := range c.moves {
		out[i] = move.clone()
	}
	return out
}

func (c MoveCatalog) MovesForGroup(group MoveGroup) []CognitiveMove {
	var out []CognitiveMove
	for _, move := range c.moves {
		if move.Group == group {
			out = append(out, move.clone())
		}
	}
	return out
}

func (c MoveCatalog) Find(name string) (CognitiveMove, bool) {
	for _, move := range c.moves {
		if move.Name == name || move.Pattern == name {
			return move.clone(), true
		}
	}
	return CognitiveMove{}, false
}

func (c MoveCatalog) AllowedGroups(phase Phase) []MoveGroup {
	switch phase {
	case PhaseFinalize:
		return []MoveGroup{MoveGroupEvaluate, MoveGroupCalibrate, MoveGroupFinalize}
	case PhaseTest:
		return []MoveGroup{MoveGroupTest, MoveGroupEvaluate, MoveGroupCalibrate}
	case PhaseIntegrate:
		return []MoveGroup{MoveGroupEvaluate, MoveGroupCalibrate, MoveGroupFinalize}
	default:
		return []MoveGroup{
			MoveGroupFrame,
			MoveGroupExplore,
			MoveGroupTest,
			MoveGroupEvaluate,
			MoveGroupCalibrate,
			MoveGroupFinalize,
		}
	}
}

func (c MoveCatalog) Recommend(session ThinkingSession) []MoveRecommendation {
	if len(session.Observations) == 0 {
		return []MoveRecommendation{
			recommend(c, "problem_decomposition", "clarify the problem shape before choosing a solution", "frame and ledger gain sub-problems, dependencies, or risks"),
			recommend(c, "metacognitive_monitoring", "calibrate uncertainty before first closure", "confidence factors gain uncertainty and blind-spot checks"),
		}
	}
	if len(session.GateReports) == 0 {
		return []MoveRecommendation{
			recommend(c, "critical_thinking", "test the current work product for bias and missing evidence", "gate report gains blockers or warnings"),
			recommend(c, "source_comparison", "compare support across visible sources", "ledger gains conflicts or verified support"),
		}
	}
	return []MoveRecommendation{
		recommend(c, "research_synthesis", "integrate supported claims before finalization", "stop decision gains finalization readiness"),
		recommend(c, "peer_review", "surface remaining objections before accepting the answer", "objections are added, resolved, or downgraded"),
	}
}

func recommend(c MoveCatalog, name, reason, delta string) MoveRecommendation {
	move, ok := c.Find(name)
	if !ok {
		return MoveRecommendation{Name: name, Pattern: name, Reason: reason, ExpectedArtifactDelta: delta}
	}
	return MoveRecommendation{
		Name:                  move.Name,
		Pattern:               move.Pattern,
		Group:                 move.Group,
		Reason:                reason,
		ExpectedArtifactDelta: delta,
	}
}

func (m CognitiveMove) clone() CognitiveMove {
	m.RequiredInputs = cloneStrings(m.RequiredInputs)
	return m
}
