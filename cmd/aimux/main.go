package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "aimux v3.0.0-dev — MCP server for multi-CLI orchestration")
	os.Exit(0)
}
