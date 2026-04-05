#!/bin/bash
# Test suite for pre-commit-stub-check.sh
# Creates temp git repos with stub patterns and verifies hook behavior.

set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HOOK="$SCRIPT_DIR/pre-commit-stub-check.sh"
PASS=0
FAIL=0

run_test() {
    local name="$1"
    local expect="$2"  # "pass" or "fail"
    local file_content="$3"

    # Create temp git repo
    local tmpdir
    tmpdir=$(mktemp -d)
    cd "$tmpdir"
    git init -q
    git config user.email "test@test.com"
    git config user.name "Test"

    # Create and stage Go file
    mkdir -p pkg
    echo "$file_content" > pkg/main.go
    git add pkg/main.go

    # Copy hook
    cp "$HOOK" .git/hooks/pre-commit
    chmod +x .git/hooks/pre-commit

    # Run hook
    local exit_code=0
    .git/hooks/pre-commit > /dev/null 2>&1 || exit_code=$?

    cd /
    rm -rf "$tmpdir"

    if [ "$expect" = "pass" ] && [ "$exit_code" -eq 0 ]; then
        echo "PASS: $name"
        PASS=$((PASS + 1))
    elif [ "$expect" = "fail" ] && [ "$exit_code" -ne 0 ]; then
        echo "PASS: $name (correctly blocked)"
        PASS=$((PASS + 1))
    else
        echo "FAIL: $name (expected=$expect, exit=$exit_code)"
        FAIL=$((FAIL + 1))
    fi
}

echo "=== Pre-commit hook tests ==="

# Test 1: STUB-DISCARD — should block
run_test "STUB-DISCARD: _ = params" "fail" \
'package main
func hello() {
    params := buildParams()
    _ = params
}'

# Test 2: STUB-HARDCODED — should block
run_test "STUB-HARDCODED: delegating string" "fail" \
'package main
func handle() string {
    return "delegating to exec"
}'

# Test 3: Clean code — should pass
run_test "Clean code passes" "pass" \
'package main
import "fmt"
func Hello(name string) {
    fmt.Println("Hello", name)
}'

# Test 4: _ = err (legitimate) — should pass
run_test "Legitimate _ = err passes" "pass" \
'package main
func hello() {
    _ = err
}'

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
