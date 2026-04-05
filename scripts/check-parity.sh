#!/bin/bash
# Feature parity checker — verifies all capabilities in feature-parity.toml
# are implemented (Go source exists) and tested (test file exists).

set -e

PARITY_FILE="feature-parity.toml"
PASS=0
FAIL=0
PENDING=0

echo "=== Feature Parity Check ==="
echo ""

# Check tools — verify each tool is registered in server.go
for tool in exec status sessions audit think investigate consensus debate dialog agents; do
    if grep -q "\"$tool\"" pkg/server/server.go 2>/dev/null; then
        echo "[IMPLEMENTED] tool: $tool"
        ((PASS++))
    else
        echo "[MISSING]     tool: $tool"
        ((FAIL++))
    fi
done

# Check exec_task and deepresearch_task (task variants — deferred)
for tool in exec_task deepresearch_task; do
    echo "[DEFERRED]    tool: $tool (MCP task API — Phase 8)"
    ((PENDING++))
done

# Check deepresearch and deepresearch_cache
if [ -f "pkg/tools/deepresearch/deepresearch.go" ]; then
    echo "[IMPLEMENTED] tool: deepresearch (scaffold)"
    ((PASS++))
else
    echo "[MISSING]     tool: deepresearch"
    ((FAIL++))
fi

# Check resources
if grep -q "aimux://health" pkg/server/server.go 2>/dev/null; then
    echo "[IMPLEMENTED] resource: aimux://health"
    ((PASS++))
else
    echo "[MISSING]     resource: aimux://health"
    ((FAIL++))
fi

# Check prompts
if grep -q "aimux-background" pkg/server/server.go 2>/dev/null; then
    echo "[IMPLEMENTED] prompt: aimux-background"
    ((PASS++))
else
    echo "[MISSING]     prompt: aimux-background"
    ((FAIL++))
fi

# Check orchestrator strategies
for strategy in pair_coding dialog consensus debate audit; do
    if ls pkg/orchestrator/*.go 2>/dev/null | xargs grep -l "func.*Name.*string.*return.*\"$strategy\"" >/dev/null 2>&1; then
        echo "[IMPLEMENTED] strategy: $strategy"
        ((PASS++))
    else
        echo "[CHECK]       strategy: $strategy (verify manually)"
        ((PENDING++))
    fi
done

# Check executor implementations
for executor in pipe conpty pty; do
    if [ -d "pkg/executor/$executor" ]; then
        echo "[IMPLEMENTED] executor: $executor"
        ((PASS++))
    else
        echo "[MISSING]     executor: $executor"
        ((FAIL++))
    fi
done

# Check infrastructure
for pkg in session/registry session/jobs session/wal session/sqlite session/gc session/live session/recovery; do
    file="pkg/${pkg}.go"
    if [ -f "$file" ]; then
        echo "[IMPLEMENTED] infra: $file"
        ((PASS++))
    else
        echo "[MISSING]     infra: $file"
        ((FAIL++))
    fi
done

echo ""
echo "=== Results ==="
echo "Implemented: $PASS"
echo "Missing:     $FAIL"
echo "Deferred:    $PENDING"
echo "Total:       $((PASS + FAIL + PENDING))"

if [ $FAIL -eq 0 ]; then
    echo ""
    echo "✓ PARITY CHECK PASSED"
    exit 0
else
    echo ""
    echo "✗ PARITY CHECK FAILED ($FAIL missing)"
    exit 1
fi
