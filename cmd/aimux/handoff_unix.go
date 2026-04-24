//go:build unix

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"
)

func dialPlatformHandoffConn(ctx context.Context, socketPath string) (net.Conn, error) {
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func listenPlatformHandoffRelay() (net.Listener, string, error) {
	path := fmt.Sprintf("%s%caimux-handoff-relay-%d.sock", os.TempDir(), os.PathSeparator, time.Now().UnixNano())
	listener, err := listenPlatformHandoffRelayForTest(path)
	if err != nil {
		return nil, "", err
	}
	return listener, path, nil
}

func listenPlatformHandoffRelayForTest(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove stale relay socket %q: %w", path, err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen relay socket %q: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("chmod relay socket %q: %w", path, err)
	}
	return listener, nil
}

func removePlatformHandoffRelay(socketPath string) error {
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove relay socket %q: %w", socketPath, err)
	}
	return nil
}
