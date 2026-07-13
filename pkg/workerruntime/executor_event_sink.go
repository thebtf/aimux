package workerruntime

import (
	"errors"
	"strings"
	"sync"

	"github.com/thebtf/aimux/pkg/types"
)

type executorEventSink struct {
	writer   *EventWriter
	provider string
	format   string
	progress func(string)

	admitMu sync.Mutex
	mu      sync.Mutex
	formats map[string]struct{}
	err     error
}

// NewExecutorEventSink binds native executor bytes to the existing bounded
// EventWriter/eventPump. TryAdmit performs bounded CPU work only; durable I/O
// remains on EventWriter's single background writer.
func NewExecutorEventSink(writer *EventWriter, provider, format string, progress func(string)) types.ExecutorEventSink {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "text"
	} else if format == "json" {
		format = "jsonl"
	}
	return &executorEventSink{
		writer:   writer,
		provider: strings.TrimSpace(provider),
		format:   format,
		progress: progress,
		formats:  make(map[string]struct{}),
	}
}

func (sink *executorEventSink) TryAdmit(event types.ExecutorEvent) bool {
	if sink == nil || sink.writer == nil {
		return false
	}
	// EventExecutors may drain stdout and stderr concurrently. Normalizers and
	// terminal flushing are ordered state machines, so serialize admission here.
	sink.admitMu.Lock()
	defer sink.admitMu.Unlock()
	if event.Terminal {
		return sink.admitTerminal(event)
	}
	channel := ""
	switch event.Channel {
	case "stdout":
		channel = "process.stdout"
	case "stderr":
		channel = "process.stderr"
	default:
		sink.setErr(errors.New("unsupported executor event channel"))
		return false
	}
	format := sink.format
	if event.Type == "text-only" {
		format = "text"
	}
	normalizer, err := sink.writer.normalizer(sink.provider, format)
	if err != nil {
		sink.setErr(err)
		return false
	}
	sink.mu.Lock()
	sink.formats[format] = struct{}{}
	sink.mu.Unlock()
	events, err := normalizer.feed(channel, event.Content)
	if err != nil {
		sink.setErr(err)
		return false
	}
	return sink.admit(events)
}

func (sink *executorEventSink) admitTerminal(event types.ExecutorEvent) bool {
	sink.mu.Lock()
	formats := make([]string, 0, len(sink.formats))
	for format := range sink.formats {
		formats = append(formats, format)
	}
	sink.mu.Unlock()
	truncated := event.Truncated
	for _, format := range formats {
		normalizer, err := sink.writer.normalizer(sink.provider, format)
		if err != nil {
			sink.setErr(err)
			return false
		}
		for _, channel := range []string{"process.stdout", "process.stderr"} {
			events, err := normalizer.flush(channel)
			if err != nil {
				sink.setErr(err)
				return false
			}
			if !sink.admit(events) {
				truncated = true
			}
		}
	}
	status := strings.TrimSpace(event.Type)
	if status == "" {
		status = "terminal"
	}
	result := sink.writer.Admit(RuntimeEvent{
		Provider:  sink.provider,
		Channel:   "system",
		Type:      status,
		Payload:   map[string]any{"status": status},
		Terminal:  true,
		Truncated: truncated,
	})
	accepted := result.Status == admissionAdmitted || result.Status == admissionCoalesced
	if !accepted {
		if err := sink.writer.Err(); err != nil {
			sink.setErr(err)
		}
	}
	return accepted
}

func (sink *executorEventSink) admit(events []RuntimeEvent) bool {
	accepted := true
	for _, event := range events {
		result := sink.writer.Admit(event)
		if result.Status != admissionAdmitted && result.Status != admissionCoalesced {
			accepted = false
			if result.Status == admissionRejectedInvalid || result.Status == admissionRejectedClosed {
				sink.setErr(errors.New("executor event admission rejected"))
			}
			continue
		}
		if sink.progress != nil && event.Channel != "process.stderr" {
			if text, ok := event.Payload["text"].(string); ok && strings.TrimSpace(text) != "" {
				sink.progress(text)
			}
		}
	}
	return accepted
}

func (sink *executorEventSink) setErr(err error) {
	if err == nil {
		return
	}
	sink.mu.Lock()
	if sink.err == nil {
		sink.err = err
	}
	sink.mu.Unlock()
}

func (sink *executorEventSink) Err() error {
	if sink == nil {
		return nil
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.err != nil {
		return sink.err
	}
	if sink.writer == nil {
		return errors.New("executor event writer unavailable")
	}
	return sink.writer.Err()
}
