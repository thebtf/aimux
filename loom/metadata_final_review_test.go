package loom

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskStoreMetadataReloadRejectsTrailingJSONDocuments(t *testing.T) {
	store := newTestStore(t)
	task := makeTask("trailing-metadata", "metadata-final-review", TaskStatusPending)
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, tail := range []string{`{"ignored":2}`, ` 2`, ` garbage`} {
		t.Run(tail, func(t *testing.T) {
			if _, err := store.db.Exec(`UPDATE tasks SET metadata=? WHERE id=?`, `{"safe":1}`+tail, task.ID); err != nil {
				t.Fatalf("corrupt metadata: %v", err)
			}
			if _, err := store.Get(task.ID); err == nil {
				t.Fatal("Get accepted metadata with a trailing JSON document")
			}
		})
	}

	if _, err := store.db.Exec(`UPDATE tasks SET metadata=? WHERE id=?`, "{\"safe\":1} \n\t", task.ID); err != nil {
		t.Fatalf("write whitespace metadata: %v", err)
	}
	if _, err := store.Get(task.ID); err != nil {
		t.Fatalf("Get whitespace-tailed metadata: %v", err)
	}
}

func TestUnmarshalMetadataJSONRejectsTrailingDocumentWithoutPartialAssignment(t *testing.T) {
	metadata := map[string]any{"trusted": "old"}
	if err := unmarshalMetadataJSON(`{"safe":1}{"ignored":2}`, &metadata); err == nil {
		t.Fatal("unmarshalMetadataJSON accepted a trailing document")
	}
	if got := metadata["trusted"]; got != "old" || len(metadata) != 1 {
		t.Fatalf("metadata after rejected document = %#v, want original map", metadata)
	}
}

func TestTaskStoreMetadataWritersPreserveIntegralJSONNumberLexemes(t *testing.T) {
	metadata := func() map[string]any {
		return map[string]any{
			"decimal_integral":   json.Number("2.0"),
			"exponent_integral":  json.Number("1e3"),
			"overflow_integral":  json.Number("9223372036854775808"),
			"rounds_to_integral": json.Number("1.0000000000000000000000000001"),
			"fraction":           json.Number("2.5"),
			"small_fraction":     json.Number("1e-3"),
			"negative_safe":      json.Number("-9007199254740992"),
			"positive_safe":      json.Number("9007199254740992"),
			"unsafe":             json.Number("9007199254740993"),
			"nested":             map[string]any{"integral": json.Number("1e3"), "fraction": json.Number("2.5")},
			"list":               []any{json.Number("2.0"), json.Number("1e-3")},
		}
	}

	store := newTestStore(t)
	create := makeTask("metadata-number-create", "metadata-final-review", TaskStatusPending)
	create.Metadata = metadata()
	if err := store.Create(create); err != nil {
		t.Fatalf("Create: %v", err)
	}
	imported := makeTask("metadata-number-import", "metadata-final-review", TaskStatusCompleted)
	imported.Metadata = metadata()
	if err := store.Import(imported); err != nil {
		t.Fatalf("Import: %v", err)
	}
	active := makeTask("metadata-number-set", "metadata-final-review", TaskStatusRunning)
	if err := store.Create(active); err != nil {
		t.Fatalf("Create active: %v", err)
	}
	if err := store.SetMetadata(active.ID, metadata()); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	for _, id := range []string{create.ID, imported.ID, active.ID} {
		task, err := store.Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		assertMetadataJSONNumber(t, task.Metadata, "decimal_integral", "2.0")
		assertMetadataJSONNumber(t, task.Metadata, "exponent_integral", "1e3")
		assertMetadataJSONNumber(t, task.Metadata, "overflow_integral", "9223372036854775808")
		assertMetadataJSONNumber(t, task.Metadata, "rounds_to_integral", "1.0000000000000000000000000001")
		assertMetadataJSONNumber(t, task.Metadata, "unsafe", "9007199254740993")
		assertMetadataFloat64(t, task.Metadata, "fraction", 2.5)
		assertMetadataFloat64(t, task.Metadata, "small_fraction", 1e-3)
		assertMetadataFloat64(t, task.Metadata, "negative_safe", -9007199254740992)
		assertMetadataFloat64(t, task.Metadata, "positive_safe", 9007199254740992)
		nested := task.Metadata["nested"].(map[string]any)
		assertMetadataJSONNumber(t, nested, "integral", "1e3")
		assertMetadataFloat64(t, nested, "fraction", 2.5)
		list := task.Metadata["list"].([]any)
		if got, ok := list[0].(json.Number); !ok || got.String() != "2.0" {
			t.Fatalf("%s list[0] = %#v, want json.Number(2.0)", id, list[0])
		}
		if got, ok := list[1].(float64); !ok || got != 1e-3 {
			t.Fatalf("%s list[1] = %#v, want float64(1e-3)", id, list[1])
		}
	}
}

func assertMetadataJSONNumber(t *testing.T, metadata map[string]any, key, want string) {
	t.Helper()
	got, ok := metadata[key].(json.Number)
	if !ok || got.String() != want {
		t.Fatalf("%s = %#v, want json.Number(%s)", key, metadata[key], want)
	}
}

func assertMetadataFloat64(t *testing.T, metadata map[string]any, key string, want float64) {
	t.Helper()
	got, ok := metadata[key].(float64)
	if !ok || got != want {
		t.Fatalf("%s = %#v, want float64(%v)", key, metadata[key], want)
	}
}

func TestTaskStoreMetadataWriterKeepsIntegralControlFailClosed(t *testing.T) {
	store := newTestStore(t)
	task := makeTask("metadata-control", "metadata-final-review", TaskStatusPending)
	task.Metadata = map[string]any{"max_attempts": json.Number("2.0")}
	if err := store.Create(task); err != nil {
		t.Fatalf("Create: %v", err)
	}
	reloaded, err := store.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	value, ok := reloaded.Metadata["max_attempts"].(json.Number)
	if !ok || value.String() != "2.0" || strings.Contains(value.String(), " ") {
		t.Fatalf("max_attempts = %#v, want exact json.Number(2.0)", reloaded.Metadata["max_attempts"])
	}
}
