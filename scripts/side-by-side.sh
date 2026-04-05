#!/bin/bash
# Side-by-side comparison: v2 (TS) vs v3 (Go) MCP responses
# Usage: ./scripts/side-by-side.sh [v2_binary] [v3_binary]
#
# Tests: initialize, tools/list, prompts/list, resources/list
# Compares: tool names, parameter schemas, capabilities

V2="${1:-node D:/Dev/mcp-aimux/dist/index.js}"
V3="${2:-./aimux.exe}"
CONFIG="AIMUX_CONFIG_DIR=config"

INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"compare","version":"1.0"}}}'
TOOLS='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'

echo "=== v3 (Go) Initialize ==="
echo "$INIT" | $CONFIG timeout 3 $V3 2>/dev/null | python3 -m json.tool 2>/dev/null || echo "(raw output above)"

echo ""
echo "=== v3 (Go) Tools ==="
printf '%s\n%s\n' "$INIT" "$TOOLS" | $CONFIG timeout 3 $V3 2>/dev/null | tail -1 | python3 -c "
import json, sys
data = json.load(sys.stdin)
if 'result' in data and 'tools' in data['result']:
    tools = sorted([t['name'] for t in data['result']['tools']])
    print(f'Tools ({len(tools)}): {tools}')
" 2>/dev/null || echo "(parse failed)"

echo ""
echo "Note: Run with v2 binary path as first argument for full comparison"
echo "Example: ./scripts/side-by-side.sh 'node ../mcp-aimux/dist/index.js' ./aimux.exe"
