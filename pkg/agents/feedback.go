package agents

// FeedbackTracker wraps DispatchHistory to provide score adjustment for agent selection.
// Formula: adjustedScore = 0.7*baseScore + 0.3*successRate, floored at 0.1.
// This prevents a historically-failing agent from receiving a score of 0 when
// baseScore > 0, preserving the agent as a candidate of last resort.
type FeedbackTracker struct {
	history *DispatchHistory
}

// NewFeedbackTracker creates a FeedbackTracker backed by the given DispatchHistory.
func NewFeedbackTracker(history *DispatchHistory) *FeedbackTracker {
	return &FeedbackTracker{history: history}
}

// OnDispatchComplete records a completed dispatch into the backing history.
// outcome should be "success" or "failure".
// taskCategory is a short label for the type of task dispatched (e.g. "coding", "review").
func (f *FeedbackTracker) OnDispatchComplete(agentName, taskCategory, outcome string) {
	if f == nil || f.history == nil {
		return
	}
	// Duration is not available at completion time in the current call path;
	// record 0 as a sentinel for unknown duration.
	_ = f.history.Record(agentName, taskCategory, outcome, 0, "")
}

// AdjustScore blends the BM25 semantic score with the historical success rate.
// adjustedScore = 0.7*baseScore + 0.3*successRate, floored at 0.1.
func (f *FeedbackTracker) AdjustScore(baseScore float64, agentName, taskCategory string) float64 {
	if f == nil || f.history == nil {
		// No history available — pass through base score unchanged, floor at 0.1.
		if baseScore < 0.1 {
			return 0.1
		}
		return baseScore
	}
	successRate := f.history.GetSuccessRate(agentName, taskCategory)
	adjusted := 0.7*baseScore + 0.3*successRate
	if adjusted < 0.1 {
		adjusted = 0.1
	}
	return adjusted
}
