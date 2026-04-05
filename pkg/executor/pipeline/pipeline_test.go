package pipeline_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/executor/pipeline"
	"github.com/thebtf/aimux/pkg/types"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"\x1b[32mgreen\x1b[0m", "green"},
		{"\x1b[1;31mbold red\x1b[0m", "bold red"},
		{"no ansi", "no ansi"},
		{"\x1b]0;title\x07text", "text"},
		{"", ""},
	}

	for _, tt := range tests {
		got := pipeline.StripANSI(tt.input)
		if got != tt.want {
			t.Errorf("StripANSI(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFilterEvents(t *testing.T) {
	in := make(chan types.Event, 4)
	in <- types.Event{Type: types.EventTypeContent, Content: "hello"}
	in <- types.Event{Type: types.EventTypeProgress, Content: "50%"}
	in <- types.Event{Type: types.EventTypeComplete}
	in <- types.Event{Type: types.EventTypeError}
	close(in)

	out := pipeline.FilterEvents(in)

	var events []types.Event
	for evt := range out {
		events = append(events, evt)
	}

	if len(events) != 4 {
		t.Errorf("expected 4 events (all pass default filter), got %d", len(events))
	}
}

func TestRouteEvents(t *testing.T) {
	in := make(chan types.Event, 4)
	in <- types.Event{Type: types.EventTypeContent, Content: "hello"}
	in <- types.Event{Type: types.EventTypeProgress, Content: "50%"}
	in <- types.Event{Type: types.EventTypeComplete}
	in <- types.Event{Type: types.EventTypeError}
	close(in)

	result := pipeline.RouteEvents(in)

	// Read from each channel
	content := <-result.Content
	if content.Content != "hello" {
		t.Errorf("content = %q, want hello", content.Content)
	}

	progress := <-result.Progress
	if progress.Content != "50%" {
		t.Errorf("progress = %q, want 50%%", progress.Content)
	}

	complete := <-result.Complete
	if complete.Type != types.EventTypeComplete {
		t.Errorf("complete type = %v, want complete", complete.Type)
	}

	errEvt := <-result.Errors
	if errEvt.Type != types.EventTypeError {
		t.Errorf("error type = %v, want error", errEvt.Type)
	}
}
