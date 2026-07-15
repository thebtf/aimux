package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/thebtf/aimux/pkg/executor"
	"github.com/thebtf/aimux/pkg/executor/pipe"
	"github.com/thebtf/aimux/pkg/swarm"
	"github.com/thebtf/aimux/pkg/types"
	"github.com/thebtf/aimux/pkg/workerruntime"
)

func runT016SourceOutputScenario(t *testing.T, binary string, spec t016ScenarioSpec) string {
	t.Helper()

	var args []string
	var stdin []byte
	var wantArgv []string
	switch spec.ID {
	case "stream":
		args = []string{"generic-worker", "--mode", "stream"}
	case "flood":
		args = []string{"generic-worker", "--mode", "flood", "--count", "32", "--chunk-bytes", "256"}
	case "byte_edge":
		args = []string{"generic-worker", "--mode", "framing"}
	case "typed_input":
		wantArgv = []string{"space value", "$HOME; & | < >", "quote\"'\\", "unicode-β"}
		stdin = []byte{0x00, 0xff, 0x1b, 'A', '\n', 0xce, 0xb2}
		args = append([]string{"generic-worker", "--mode", "typed-input", "--"}, wantArgv...)
	case "invalid_input":
		args = []string{"generic-worker", "--mode", "invalid-input"}
	case "oversize_input":
		args = []string{"generic-worker", "--mode", "typed-input"}
		stdin = bytes.Repeat([]byte{0xa5}, (1<<16)+1)
	default:
		t.Fatalf("unknown source output scenario %q", spec.ID)
		return ""
	}

	adapter := executor.NewCLIPipeAdapter(pipe.New())
	s := swarm.New(func(string) (types.ExecutorV2, error) { return adapter, nil }, nil)
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := s.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown source output scenario %q: %v", spec.ID, err)
		}
	}()

	runtime, err := workerruntime.New(s)
	if err != nil {
		t.Fatalf("create worker runtime: %v", err)
	}
	scope := "t016-source-output-" + spec.ID
	handle, err := s.Get(context.Background(), "pipe", swarm.Stateful, swarm.WithScope(scope))
	if err != nil {
		t.Fatalf("get scoped pipe handle: %v", err)
	}

	var events []types.ExecutorEvent
	var eventsMu sync.Mutex
	response, err := runtime.Execute(
		context.Background(),
		handle,
		scope,
		types.ExecutionID("t016-source-output-"+spec.ID),
		types.Message{Spawn: &types.SpawnArgs{Command: binary, Args: args, Stdin: string(stdin)}},
		types.ExecutorEventSinkFunc(func(event types.ExecutorEvent) bool {
			event.Content = append([]byte(nil), event.Content...)
			eventsMu.Lock()
			events = append(events, event)
			eventsMu.Unlock()
			return true
		}),
	)

	eventsMu.Lock()
	snapshot := append([]types.ExecutorEvent(nil), events...)
	eventsMu.Unlock()
	var stdout, stderr []byte
	terminalIndex := -1
	var terminal types.ExecutorEvent
	for index, event := range snapshot {
		if event.Terminal {
			if terminalIndex >= 0 {
				t.Fatalf("scenario %q emitted multiple terminal events: %#v", spec.ID, snapshot)
			}
			terminalIndex = index
			terminal = event
			continue
		}
		switch event.Channel {
		case "stdout":
			stdout = append(stdout, event.Content...)
		case "stderr":
			stderr = append(stderr, event.Content...)
		}
	}
	if terminalIndex < 0 {
		t.Fatalf("scenario %q emitted no terminal event: %#v", spec.ID, snapshot)
	}
	if terminal.Type != spec.Expected {
		t.Fatalf("scenario %q terminal type = %q, want %q", spec.ID, terminal.Type, spec.Expected)
	}

	switch spec.ID {
	case "stream":
		if err != nil || response == nil || response.ExitCode != 0 {
			t.Fatalf("stream response = %#v, error = %v", response, err)
		}
		if got, want := string(stdout), "stdout:alpha\nstdout:omega\n"; got != want {
			t.Fatalf("stream stdout = %q, want %q", got, want)
		}
		if got, want := string(stderr), "stderr:alpha\nstderr:omega\n"; got != want {
			t.Fatalf("stream stderr = %q, want %q", got, want)
		}
		seen := map[string]bool{}
		for index, event := range snapshot[:terminalIndex] {
			if event.Channel == "stdout" || event.Channel == "stderr" {
				seen[event.Channel] = true
			}
			if event.Terminal {
				t.Fatalf("stream terminal appeared before output at event %d", index)
			}
		}
		if !seen["stdout"] || !seen["stderr"] {
			t.Fatalf("stream events = %#v, want stdout and stderr before terminal", snapshot)
		}
	case "flood":
		if err != nil || response == nil || response.ExitCode != 0 || response.Partial || terminal.Truncated {
			t.Fatalf("flood response = %#v, terminal = %#v, error = %v", response, terminal, err)
		}
		wantStdout := bytes.Repeat(append(bytes.Repeat([]byte("O"), 255), '\n'), 32)
		wantStderr := bytes.Repeat(append(bytes.Repeat([]byte("E"), 255), '\n'), 32)
		if !bytes.Equal(stdout, wantStdout) || !bytes.Equal(stderr, wantStderr) {
			t.Fatalf("flood output lengths = stdout %d stderr %d, want %d each", len(stdout), len(stderr), len(wantStdout))
		}
		for _, event := range snapshot {
			if event.Truncated {
				t.Fatalf("flood event was truncated: %#v", event)
			}
		}
	case "byte_edge":
		if err != nil || response == nil || response.ExitCode != 0 {
			t.Fatalf("byte edge response = %#v, error = %v", response, err)
		}
		wantStdout := []byte("utf8:\xce\xb2\r\ncr-only\rline-feed\ninvalid:\xff\xfe\ncontrol:\x00\x1b\nno-final-newline")
		wantStderr := []byte("stderr-crlf\r\nstderr-invalid:\xff\nstderr-control:\x00\nstderr-no-final-newline")
		if !bytes.Equal(stdout, wantStdout) || !bytes.Equal(stderr, wantStderr) {
			t.Fatalf("byte edge output = stdout %v stderr %v", stdout, stderr)
		}
	case "typed_input":
		if err != nil || response == nil || response.ExitCode != 0 || len(stderr) != 0 {
			t.Fatalf("typed input response = %#v, stderr = %q, error = %v", response, stderr, err)
		}
		var event struct {
			Event       string   `json:"event"`
			Argv        []string `json:"argv"`
			StdinBytes  int      `json:"stdin_bytes"`
			StdinBase64 string   `json:"stdin_base64"`
		}
		decoder := json.NewDecoder(bytes.NewReader(stdout))
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode typed input stdout: %v; stdout=%q", err, stdout)
		}
		var extra json.RawMessage
		if err := decoder.Decode(&extra); err != io.EOF {
			t.Fatalf("typed input stdout contains more than one JSON object: %q; error=%v", stdout, err)
		}
		decoded, decodeErr := base64.StdEncoding.DecodeString(event.StdinBase64)
		if event.Event != "typed_input" || !slices.Equal(event.Argv, wantArgv) || event.StdinBytes != len(stdin) || decodeErr != nil || !bytes.Equal(decoded, stdin) {
			t.Fatalf("typed input event = %#v, decoded = %v, decode error = %v", event, decoded, decodeErr)
		}
	case "invalid_input", "oversize_input":
		if response == nil || response.ExitCode == 0 {
			t.Fatalf("%s response = %#v, want nonzero exit", spec.ID, response)
		}
		if len(stdout) != 0 {
			t.Fatalf("%s stdout = %q, want empty", spec.ID, stdout)
		}
		wantDiagnostic := []byte("generic-worker: unknown mode \"invalid-input\"\n")
		if spec.ID == "oversize_input" {
			wantDiagnostic = []byte("generic-worker: typed-input bounds: stdin-bytes<=65536\n")
		}
		if !bytes.Contains(stderr, wantDiagnostic) {
			t.Fatalf("%s stderr = %q, want diagnostic %q", spec.ID, stderr, wantDiagnostic)
		}
	}

	return terminal.Type
}
