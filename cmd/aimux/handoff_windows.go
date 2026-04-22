//go:build windows

package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

func dialPlatformHandoffConn(ctx context.Context, socketPath string) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, socketPath)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func listenPlatformHandoffRelay() (net.Listener, string, error) {
	path := fmt.Sprintf(`\\.\pipe\aimux-handoff-relay-%d`, time.Now().UnixNano())
	listener, err := listenPlatformHandoffRelayForTest(path)
	if err != nil {
		return nil, "", err
	}
	return listener, path, nil
}

func listenPlatformHandoffRelayForTest(path string) (net.Listener, error) {
	listener, err := winio.ListenPipe(path, nil)
	if err != nil {
		return nil, fmt.Errorf("listen relay pipe %q: %w", path, err)
	}
	return listener, nil
}

func removePlatformHandoffRelay(string) error {
	return nil
}
