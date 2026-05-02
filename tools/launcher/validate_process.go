package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type processMeasurement struct {
	PeakBytes uint64
	Method    string
	Available bool
}

func runLauncherMeasured(ctx context.Context, launcher string, args []string, stdin string) (string, string, int, processMeasurement, error) {
	cmd := exec.CommandContext(ctx, launcher, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return stdout.String(), stderr.String(), -1, processMeasurement{}, err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	measurement := processMeasurement{}
	recordSample := func() {
		bytes, method, ok := sampleProcessMemory(cmd.Process.Pid)
		if ok && bytes > measurement.PeakBytes {
			measurement = processMeasurement{PeakBytes: bytes, Method: method, Available: true}
		}
	}
	recordSample()

	var err error
	for {
		select {
		case err = <-done:
			recordSample()
			code := 0
			if err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					err = fmt.Errorf("%w: %v", ctxErr, err)
				}
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					code = exitErr.ExitCode()
				} else {
					code = -1
				}
			}
			return stdout.String(), stderr.String(), code, measurement, err
		case <-ticker.C:
			recordSample()
		}
	}
}
