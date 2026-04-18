package budget

// DualSourceResponse is the sessions(action=list) response shape per FR-11/C3.
type DualSourceResponse[S any, L any] struct {
	Sessions           []S            `json:"sessions"`
	LoomTasks          []L            `json:"loom_tasks"`
	SessionsPagination PaginationMeta `json:"sessions_pagination"`
	LoomPagination     PaginationMeta `json:"loom_pagination"`
}

// PaginateDualSource paginates sessions and loomTasks independently using params.
// Cursor logic:
//
//	sessionsLimit = params.SessionsLimit if >0, else params.Limit if >0, else DefaultLimit
//	sessionsOffset = params.SessionsOffset if SessionsOffset>0 or SessionsLimit>0, else params.Offset
//	loomLimit = params.LoomLimit if >0, else params.Limit if >0, else DefaultLimit
//	loomOffset = params.LoomOffset if LoomOffset>0 or LoomLimit>0, else params.Offset
func PaginateDualSource[S any, L any](sessions []S, loomTasks []L, params BudgetParams) DualSourceResponse[S, L] {
	// resolveLimit returns the effective page size: per-source > global > DefaultLimit.
	resolveLimit := func(sourceLimit int) int {
		if sourceLimit > 0 {
			return sourceLimit
		}
		if params.Limit > 0 {
			return params.Limit
		}
		return DefaultLimit
	}

	// resolveOffset returns the per-source offset when it is explicitly set
	// (sourceOffset > 0 or sourceLimit > 0 signals per-source mode),
	// otherwise falls back to the global offset.
	resolveOffset := func(sourceOffset, sourceLimit int) int {
		if sourceOffset > 0 || sourceLimit > 0 {
			return sourceOffset
		}
		return params.Offset
	}

	sessionsLimit := resolveLimit(params.SessionsLimit)
	sessionsOffset := resolveOffset(params.SessionsOffset, params.SessionsLimit)
	loomLimit := resolveLimit(params.LoomLimit)
	loomOffset := resolveOffset(params.LoomOffset, params.LoomLimit)

	sessionsPage, sessionsMeta := PaginateSingle(sessions, sessionsLimit, sessionsOffset)
	loomPage, loomMeta := PaginateSingle(loomTasks, loomLimit, loomOffset)

	sessionsMeta.LimitClamped = params.LimitClamped
	loomMeta.LimitClamped = params.LimitClamped

	return DualSourceResponse[S, L]{
		Sessions:           sessionsPage,
		LoomTasks:          loomPage,
		SessionsPagination: sessionsMeta,
		LoomPagination:     loomMeta,
	}
}
