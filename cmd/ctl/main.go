package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/thebtf/mcp-mux/muxcore/control"
	"github.com/thebtf/mcp-mux/muxcore/serverid"
)

func main() {
	cmd := flag.String("cmd", "status", "control command: status, shutdown, graceful-restart")
	drainMs := flag.Int("drain-ms", 10000, "drain timeout for shutdown/graceful-restart")
	name := flag.String("name", "aimux", "daemon name (default: aimux)")
	timeoutSec := flag.Int("timeout", 60, "client timeout in seconds")
	flag.Parse()

	sock := serverid.DaemonControlPath("", *name)
	fmt.Fprintf(os.Stderr, "sock: %s\ncmd:  %s\n", sock, *cmd)

	req := control.Request{Cmd: *cmd}
	if *cmd == "shutdown" || *cmd == "graceful-restart" {
		req.DrainTimeoutMs = *drainMs
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
