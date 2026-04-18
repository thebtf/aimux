package budget

import "testing"

func TestPaginateSingle(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		page, meta := PaginateSingle([]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, 5, 0)
		expected := []int{0, 1, 2, 3, 4}

		if len(page) != len(expected) {
			t.Fatalf("len(page) = %d", len(page))
		}
		for i, value := range expected {
			if page[i] != value {
				t.Fatalf("page[%d] = %d, want %d", i, page[i], value)
			}
		}
		if !meta.HasMore {
			t.Fatalf("HasMore = %v", meta.HasMore)
		}
		if meta.Total != 10 {
			t.Fatalf("Total = %d, want 10", meta.Total)
		}
	})

	t.Run("last page", func(t *testing.T) {
		page, meta := PaginateSingle([]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, 5, 8)
		expected := []int{8, 9}

		if len(page) != len(expected) {
			t.Fatalf("len(page) = %d", len(page))
		}
		for i, value := range expected {
			if page[i] != value {
				t.Fatalf("page[%d] = %d, want %d", i, page[i], value)
			}
		}
		if meta.HasMore {
			t.Fatalf("HasMore = %v", meta.HasMore)
		}
		if meta.Total != 10 {
			t.Fatalf("Total = %d, want 10", meta.Total)
		}
	})

	t.Run("fits all", func(t *testing.T) {
		page, meta := PaginateSingle([]int{0, 1, 2, 3, 4}, 20, 0)
		if len(page) != 5 {
			t.Fatalf("len(page) = %d", len(page))
		}
		if meta.HasMore {
			t.Fatalf("HasMore = %v", meta.HasMore)
		}
		if meta.Total != 5 {
			t.Fatalf("Total = %d, want 5", meta.Total)
		}
	})

	t.Run("offset > len", func(t *testing.T) {
		page, meta := PaginateSingle([]int{0, 1, 2}, 5, 10)
		if len(page) != 0 {
			t.Fatalf("len(page) = %d", len(page))
		}
		if meta.HasMore {
			t.Fatalf("HasMore = %v", meta.HasMore)
		}
		if meta.Total != 3 {
			t.Fatalf("Total = %d, want 3", meta.Total)
		}
	})

	t.Run("exact boundary", func(t *testing.T) {
		page, meta := PaginateSingle([]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, 5, 5)
		if len(page) != 5 {
			t.Fatalf("len(page) = %d", len(page))
		}
		if meta.HasMore {
			t.Fatalf("HasMore = %v", meta.HasMore)
		}
	})

	t.Run("single item", func(t *testing.T) {
		page, meta := PaginateSingle([]int{0}, 1, 0)
		if len(page) != 1 {
			t.Fatalf("len(page) = %d", len(page))
		}
		if meta.HasMore {
			t.Fatalf("HasMore = %v", meta.HasMore)
		}
	})

	t.Run("stable copy", func(t *testing.T) {
		page, _ := PaginateSingle([]int{1, 2, 3, 4}, 2, 1)
		expected := []int{2, 3}

		if len(page) != len(expected) {
			t.Fatalf("len(page) = %d", len(page))
		}
		for i, value := range expected {
			if page[i] != value {
				t.Fatalf("page[%d] = %d, want %d", i, page[i], value)
			}
		}
	})
}
