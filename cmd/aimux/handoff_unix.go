//go:build unix

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	// Create a private directory so the relay socket is protected from the
	// moment net.Listen creates it, without a post-create chmod window.
	dir, err := os.MkdirTemp("", fmt.Sprintf("aimux-relay-%d-*", time.Now().UnixNano()))
	if err != nil {
		return nil, "", fmt.Errorf("create relay socket dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return nil, "", fmt.Errorf("chmod relay socket dir: %w", err)
	}
	path := filepath.Join(dir, "relay.sock")
	listener, err := listenPlatformHandoffRelayForTest(path)
	if err != nil {
		_ = os.RemoveAll(dir)
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
	return listener, nil
}

func removePlatformHandoffRelay(socketPath string) error {
	dir := filepath.Dir(socketPath)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove relay socket dir %q: %w", dir, err)
	}
	return nil
}
