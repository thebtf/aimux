package logger

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// TestD1_LogEntry_AlwaysUTC verifies that LogEntry's JSON marshaling always
// outputs timestamps in UTC, regardless of the local timezone.
// D1 AIMUX-11: TZ standardize on UTC in all log entries.
func TestD1_LogEntry_AlwaysUTC(t *testing.T) {
	// Create entries with various timezone offsets.
	locations := []struct {
		name   string
		offset int // seconds east of UTC
	}{
		{"UTC", 0},
		{"Moscow", 3 * 3600},
		{"US_Eastern", -5 * 3600},
		{"India", 5*3600 + 1800},
		{"Tokyo", 9 * 3600},
	}

	for _, loc := range locations {
		t.Run(loc.name, func(t *testing.T) {
			tz := time.FixedZone(loc.name, loc.offset)
			entry := LogEntry{
				Level:   LevelInfo,
				Time:    time.Date(2026, 7, 3, 12, 0, 0, 0, tz),
				Message: "tz test",
			}

			data, err := json.Marshal(entry)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			// Parse the JSON and check the time field ends with "Z" (UTC).
			var wire struct {
				Time string `json:"time"`
			}
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}

			// RFC3339 UTC times end with "Z".
			if wire.Time[len(wire.Time)-1] != 'Z' {
				t.Errorf("time %q is not UTC (expected Z suffix)", wire.Time)
			}
		})
	}
}

// TestD1_IPCSink_BuildLogForwardNotification_UTC verifies that the notification
// envelope built by the drain loop always contains UTC timestamps.
func TestD1_IPCSink_BuildLogForwardNotification_UTC(t *testing.T) {
	// Use a non-UTC timezone.
	tokyo := time.FixedZone("Tokyo", 9*3600)
	entry := LogEntry{
		Level:   LevelWarn,
		Time:    time.Date(2026, 7, 3, 21, 0, 0, 0, tokyo),
		Message: "notification tz test",
	}

	notification, err := buildLogForwardNotification(entry)
	if err != nil {
		t.Fatalf("buildLogForwardNotification: %v", err)
	}

	// The notification should contain UTC time.
	if !bytes.Contains(notification, []byte("12:00:00")) {
		// 21:00 Tokyo = 12:00 UTC
		t.Logf("notification: %s", notification)
		t.Error("expected UTC time (12:00:00) in notification, got non-UTC")
	}
}
