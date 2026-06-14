package recipes

import "testing"

func TestListReturnsDeterministicInitialReadOnlyRecipes(t *testing.T) {
	got := List()
	if len(got) != 2 {
		t.Fatalf("recipe count = %d, want 2", len(got))
	}
	if got[0].ID != "code-review" || got[1].ID != "second-opinion" {
		t.Fatalf("recipe order = [%s %s], want [code-review second-opinion]", got[0].ID, got[1].ID)
	}
	for _, recipe := range got {
		if recipe.Title == "" || recipe.Description == "" {
			t.Fatalf("recipe %s missing title/description: %#v", recipe.ID, recipe)
		}
		if recipe.TaskClass != TaskClassReview {
			t.Fatalf("recipe %s task_class = %q, want %q", recipe.ID, recipe.TaskClass, TaskClassReview)
		}
		if !recipe.ReadOnly {
			t.Fatalf("recipe %s ReadOnly = false, want true for CR-004", recipe.ID)
		}
		if len(recipe.Phases) == 0 {
			t.Fatalf("recipe %s phases empty", recipe.ID)
		}
		if len(recipe.PolicyNeeds) == 0 {
			t.Fatalf("recipe %s policy needs empty", recipe.ID)
		}
		if len(recipe.OutputResources) == 0 {
			t.Fatalf("recipe %s output resources empty", recipe.ID)
		}
	}
}

func TestListReturnsCopies(t *testing.T) {
	got := List()
	got[0].ID = "mutated"
	got[0].Phases[0] = "mutated"

	again := List()
	if again[0].ID != "code-review" {
		t.Fatalf("registry ID mutated through caller-owned slice: %q", again[0].ID)
	}
	if again[0].Phases[0] == "mutated" {
		t.Fatalf("registry phases mutated through caller-owned nested slice: %#v", again[0].Phases)
	}
}

func TestResolveKnownAndUnknownRecipe(t *testing.T) {
	recipe, ok := Resolve(" code-review ")
	if !ok {
		t.Fatal("Resolve(code-review) ok = false")
	}
	if recipe.ID != "code-review" {
		t.Fatalf("recipe ID = %q, want code-review", recipe.ID)
	}
	if !recipe.GateDefault {
		t.Fatalf("code-review GateDefault = false, want true")
	}

	if _, ok := Resolve("missing"); ok {
		t.Fatal("Resolve(missing) ok = true, want false")
	}
}

func TestAvailableIDs(t *testing.T) {
	got := AvailableIDs()
	want := []string{"code-review", "second-opinion"}
	if len(got) != len(want) {
		t.Fatalf("AvailableIDs len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AvailableIDs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	got[0] = "mutated"
	if again := AvailableIDs(); again[0] != "code-review" {
		t.Fatalf("AvailableIDs returned mutable backing array: %#v", again)
	}
}
