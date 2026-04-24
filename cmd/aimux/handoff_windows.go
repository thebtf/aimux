//go:build windows

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
)

const muxcoreWindowsHandoffPipePrefix = `\\.\pipe\mcp-mux-handoff-`

func normalizePlatformHandoffSocket(socketPath string) string {
	if strings.HasPrefix(socketPath, `\\.\pipe\`) {
		return socketPath
	}
	return muxcoreWindowsHandoffPipePrefix + socketPath
}

func dialPlatformHandoffConn(ctx context.Context, socketPath string) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, normalizePlatformHandoffSocket(socketPath))
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func listenPlatformHandoffRelay() (net.Listener, string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		suffix := fmt.Sprintf("%d", time.Now().UnixNano())
		listener, listenErr := listenPlatformHandoffRelayForTest(suffix)
		if listenErr != nil {
			return nil, "", listenErr
		}
		return listener, suffix, nil
	}
	suffix := hex.EncodeToString(b)
	listener, err := listenPlatformHandoffRelayForTest(suffix)
	if err != nil {
		return nil, "", err
	}
	return listener, suffix, nil
}

func listenPlatformHandoffRelayForTest(socketPath string) (net.Listener, error) {
	listener, err := winio.ListenPipe(normalizePlatformHandoffSocket(socketPath), nil)
	if err != nil {
		return nil, fmt.Errorf("listen relay pipe %q: %w", socketPath, err)
	}
	return listener, nil
}

func removePlatformHandoffRelay(string) error {
	return nil
}
