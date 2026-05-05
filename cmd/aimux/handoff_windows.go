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
		pipePath := normalizePlatformHandoffSocket(suffix)
		listener, listenErr := listenPlatformHandoffRelayForTest(pipePath)
		if listenErr != nil {
			return nil, "", listenErr
		}
		return listener, pipePath, nil
	}
	suffix := hex.EncodeToString(b)
	pipePath := normalizePlatformHandoffSocket(suffix)
	listener, err := listenPlatformHandoffRelayForTest(pipePath)
	if err != nil {
		return nil, "", err
	}
	return listener, pipePath, nil
}

func listenPlatformHandoffRelayForTest(pipePath string) (net.Listener, error) {
	listener, err := winio.ListenPipe(pipePath, nil)
	if err != nil {
		return nil, fmt.Errorf("listen relay pipe %q: %w", pipePath, err)
	}
	return listener, nil
}

func removePlatformHandoffRelay(string) error {
	return nil
}
