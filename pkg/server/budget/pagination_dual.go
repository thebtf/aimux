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
//	sessionsOffset = params.SessionsOffset if SessionsLimit>0, else params.Offset
//	loomLimit = params.LoomLimit if >0, else params.Limit if >0, else DefaultLimit
//	loomOffset = params.LoomOffset if LoomLimit>0, else params.Offset
func PaginateDualSource[S any, L any](sessions []S, loomTasks []L, params BudgetParams) DualSourceResponse[S, L] {
	sessionsLimit := params.SessionsLimit
	sessionsOffset := params.SessionsOffset
	if sessionsLimit <= 0 {
		sessionsLimit = params.Limit
		if sessionsLimit <= 0 {
			sessionsLimit = DefaultLimit
		}
		sessionsOffset = params.Offset
	}

	loomLimit := params.LoomLimit
	loomOffset := params.LoomOffset
	if loomLimit <= 0 {
		loomLimit = params.Limit
		if loomLimit <= 0 {
			loomLimit = DefaultLimit
		}
		loomOffset = params.Offset
	}

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
