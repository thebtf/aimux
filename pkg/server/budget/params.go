package budget

import (
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// DefaultLimit is the default page size.
const DefaultLimit = 20

// MaxLimit is the hard upper bound for list limits.
const MaxLimit = 100

// BudgetParams holds parsed pagination and filtering parameters from a tool request.
// Dual-source fields (Sessions*, Loom*) are used by sessions/list for independent
// cursor control per data source. Zero values for limits signal "use global limit".
type BudgetParams struct {
	Fields         []string
	Limit          int
	Offset         int
	IncludeContent bool
	Tail           int
	LimitClamped   bool
	SessionsLimit  int
	SessionsOffset int
	LoomLimit      int
	LoomOffset     int
}

// ParseBudgetParams parses budget params from mcp.CallToolRequest.
// limit<1 -> error "limit must be >= 1".
// limit>100 -> clamp and set LimitClamped=true.
// sessions_limit>100 -> clamp to MaxLimit and set LimitClamped=true (0 = use global limit).
// loom_limit>100 -> clamp to MaxLimit and set LimitClamped=true (0 = use global limit).
// offset<0 -> error "offset must be >= 0".
// tail<=0 when supplied -> error "tail must be >= 1".
// fields="" treated as omitted.
func ParseBudgetParams(request mcp.CallToolRequest) (BudgetParams, error) {
	params := BudgetParams{}

	rawFields := request.GetString("fields", "")
	if strings.TrimSpace(rawFields) != "" {
		splitFields := strings.Split(rawFields, ",")
		fields := make([]string, 0, len(splitFields))
		for _, field := range splitFields {
			trimmed := strings.TrimSpace(field)
			if trimmed != "" {
				fields = append(fields, trimmed)
			}
		}
		params.Fields = fields
	}

	rawLimit := request.GetInt("limit", -1)
	if _, hasLimit := request.GetArguments()["limit"]; !hasLimit {
		params.Limit = DefaultLimit
	} else if rawLimit < 1 {
		return BudgetParams{}, fmt.Errorf("limit must be >= 1")
	} else if rawLimit > MaxLimit {
		params.Limit = MaxLimit
		params.LimitClamped = true
	} else {
		params.Limit = rawLimit
	}

	params.Offset = request.GetInt("offset", 0)
	if params.Offset < 0 {
		return BudgetParams{}, fmt.Errorf("offset must be >= 0")
	}

	params.IncludeContent = request.GetBool("include_content", false)

	if _, hasTail := request.GetArguments()["tail"]; hasTail {
		rawTail := request.GetInt("tail", 0)
		if rawTail <= 0 {
			return BudgetParams{}, fmt.Errorf("tail must be >= 1")
		}
		params.Tail = rawTail
	}

	params.SessionsLimit = request.GetInt("sessions_limit", 0)
	if params.SessionsLimit < 0 {
		return BudgetParams{}, fmt.Errorf("sessions_limit must be >= 0")
	} else if params.SessionsLimit > MaxLimit {
		params.SessionsLimit = MaxLimit
		params.LimitClamped = true
	}
	params.SessionsOffset = request.GetInt("sessions_offset", 0)
	if params.SessionsOffset < 0 {
		return BudgetParams{}, fmt.Errorf("sessions_offset must be >= 0")
	}

	params.LoomLimit = request.GetInt("loom_limit", 0)
	if params.LoomLimit < 0 {
		return BudgetParams{}, fmt.Errorf("loom_limit must be >= 0")
	} else if params.LoomLimit > MaxLimit {
		params.LoomLimit = MaxLimit
		params.LimitClamped = true
	}
	params.LoomOffset = request.GetInt("loom_offset", 0)
	if params.LoomOffset < 0 {
		return BudgetParams{}, fmt.Errorf("loom_offset must be >= 0")
	}

	return params, nil
}
