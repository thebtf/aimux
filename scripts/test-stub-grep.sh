#!/bin/bash
# Test suite for stub-grep.sh CI scanner

set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCANNER="$SCRIPT_DIR/stub-grep.sh"
PASS=0
FAIL=0

run_test() {
    local name="$1"
    local expect="$2"
    local dir="$3"

    local exit_code=0
    bash "$SCANNER" "$dir" > /dev/null 2>&1 || exit_code=$?

    if [ "$expect" = "pass" ] && [ "$exit_code" -eq 0 ]; then
        echo "PASS: $name"
        PASS=$((PASS + 1))
    elif [ "$expect" = "fail" ] && [ "$exit_code" -ne 0 ]; then
        echo "PASS: $name (correctly detected)"
        PASS=$((PASS + 1))
    else
        echo "FAIL: $name (expected=$expect, exit=$exit_code)"
        FAIL=$((FAIL + 1))
    fi
}

echo "=== stub-grep.sh tests ==="

# Create temp dirs with test files
TMPDIR=$(mktemp -d)
mkdir -p "$TMPDIR/pkg"

# Test 1: STUB-PASSTHROUGH
echo 'package main
func handle() {
	params := buildParams()
	_ = params
}' > "$TMPDIR/pkg/stub.go"
run_test "STUB-PASSTHROUGH detected" "fail" "$TMPDIR"
rm "$TMPDIR/pkg/stub.go"

# Test 2: Clean code
echo 'package main
import "fmt"
func Hello(name string) { fmt.Println(name) }' > "$TMPDIR/pkg/clean.go"
run_test "Clean code passes" "pass" "$TMPDIR"

rm -rf "$TMPDIR"

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
