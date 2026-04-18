package budget

import "testing"

func TestPaginateDualSource(t *testing.T) {
	t.Run("independent cursors", func(t *testing.T) {
		sessions := []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10"}
		loom := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7"}

		got := PaginateDualSource(sessions, loom, BudgetParams{SessionsLimit: 5, LoomLimit: 3})

		if len(got.Sessions) != 5 {
			t.Fatalf("len(got.Sessions) = %d", len(got.Sessions))
		}
		if len(got.LoomTasks) != 3 {
			t.Fatalf("len(got.LoomTasks) = %d", len(got.LoomTasks))
		}
	})

	t.Run("legacy fallback", func(t *testing.T) {
		sessions := []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10"}
		loom := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9", "l10"}

		got := PaginateDualSource(sessions, loom, BudgetParams{Limit: 10})

		if got.SessionsPagination.Limit != 10 {
			t.Fatalf("Sessions limit = %d", got.SessionsPagination.Limit)
		}
		if got.LoomPagination.Limit != 10 {
			t.Fatalf("Loom limit = %d", got.LoomPagination.Limit)
		}
		if len(got.Sessions) != 10 {
			t.Fatalf("len(got.Sessions) = %d", len(got.Sessions))
		}
		if len(got.LoomTasks) != 10 {
			t.Fatalf("len(got.LoomTasks) = %d", len(got.LoomTasks))
		}
	})

	t.Run("sessions past end", func(t *testing.T) {
		sessions := []string{"s1", "s2", "s3"}
		loom := []string{"l1", "l2", "l3", "l4", "l5"}

		got := PaginateDualSource(sessions, loom, BudgetParams{SessionsLimit: 5, SessionsOffset: 10, LoomLimit: 3, LoomOffset: 1})

		if len(got.Sessions) != 0 {
			t.Fatalf("sessions = %#v", got.Sessions)
		}
		if len(got.LoomTasks) != 3 {
			t.Fatalf("len(got.LoomTasks) = %d", len(got.LoomTasks))
		}
		if got.LoomTasks[0] != "l2" {
			t.Fatalf("first loom item = %q", got.LoomTasks[0])
		}
	})

	t.Run("both sources empty", func(t *testing.T) {
		got := PaginateDualSource([]int{}, []int{}, BudgetParams{})

		if got.SessionsPagination.Total != 0 {
			t.Fatalf("sessions total = %d", got.SessionsPagination.Total)
		}
		if got.LoomPagination.Total != 0 {
			t.Fatalf("loom total = %d", got.LoomPagination.Total)
		}
		if len(got.Sessions) != 0 {
			t.Fatalf("sessions = %#v", got.Sessions)
		}
		if len(got.LoomTasks) != 0 {
			t.Fatalf("loom = %#v", got.LoomTasks)
		}
	})

	t.Run("limit clamped", func(t *testing.T) {
		got := PaginateDualSource([]int{1, 2, 3}, []int{1, 2, 3}, BudgetParams{Limit: 10, LimitClamped: true})

		if !got.SessionsPagination.LimitClamped {
			t.Fatalf("sessions.limit_clamped = false")
		}
		if !got.LoomPagination.LimitClamped {
			t.Fatalf("loom.limit_clamped = false")
		}
	})
}
