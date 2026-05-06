package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/thebtf/mcp-mux/muxcore/control"
	"github.com/thebtf/mcp-mux/muxcore/serverid"
)

var errUnsafeGracefulRestart = errors.New(
	"graceful-restart is blocked for aimux until the target daemon is confirmed to run the muxcore SessionHandler snapshot restore fix (Engram issue #190); use shutdown/deferred restart, or pass -allow-unsafe-graceful-restart if you intentionally accept the risk",
)

func main() {
	cmd := flag.String("cmd", "status", "control command: status, shutdown, graceful-restart")
	drainMs := flag.Int("drain-ms", 10000, "drain timeout for shutdown/graceful-restart")
	name := flag.String("name", "aimux", "daemon name (default: aimux)")
	timeoutSec := flag.Int("timeout", 60, "client timeout in seconds")
	allowUnsafeGracefulRestart := flag.Bool("allow-unsafe-graceful-restart", false, "allow raw muxcore graceful-restart when the target daemon is known to include the SessionHandler restore fix tracked as Engram issue #190")
	flag.Parse()

	sock := serverid.DaemonControlPath("", *name)
	fmt.Fprintf(os.Stderr, "sock: %s\ncmd:  %s\n", sock, *cmd)

	req, reqErr := buildControlRequest(*cmd, *drainMs, *allowUnsafeGracefulRestart)
	if reqErr != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", reqErr)
		os.Exit(2)
	}

	resp, err := control.SendWithTimeout(sock, req, time.Duration(*timeoutSec)*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	if resp == nil {
		fmt.Fprintln(os.Stderr, "nil response")
		os.Exit(1)
	}
	fmt.Printf("ok=%v message=%s\n", resp.OK, resp.Message)
	if len(resp.Data) > 0 {
		fmt.Printf("data: %s\n", string(resp.Data))
	}
}

func buildControlRequest(cmd string, drainMs int, allowUnsafeGracefulRestart bool) (control.Request, error) {
	if cmd == "graceful-restart" && !allowUnsafeGracefulRestart {
		return control.Request{}, errUnsafeGracefulRestart
	}

	req := control.Request{Cmd: cmd}
	if cmd == "shutdown" || cmd == "graceful-restart" {
		req.DrainTimeoutMs = drainMs
	}
	return req, nil
}
