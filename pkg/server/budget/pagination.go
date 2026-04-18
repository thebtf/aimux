package budget

// Counter is an optional interface for efficient counting.
type Counter interface {
	Count(filter any) (int, error)
}

// PaginationMeta is pagination metadata for list responses.
type PaginationMeta struct {
	Total        int  `json:"total"`
	Limit        int  `json:"limit"`
	Offset       int  `json:"offset"`
	HasMore      bool `json:"has_more"`
	LimitClamped bool `json:"limit_clamped,omitempty"`
}

// PaginateSingle returns a stable copy of items[offset:offset+limit] and pagination metadata.
// Uses append([]T(nil), ...) for immutable copy.
func PaginateSingle[T any](items []T, limit, offset int) (page []T, meta PaginationMeta) {
	meta = PaginationMeta{
		Total:  len(items),
		Limit:  limit,
		Offset: offset,
	}

	if len(items) == 0 || offset >= len(items) || limit <= 0 {
		return []T{}, meta
	}

	end := offset + limit
	if end > len(items) {
		end = len(items)
	}

	meta.HasMore = (offset + limit) < len(items)
	page = append([]T(nil), items[offset:end]...)
	return page, meta
}
