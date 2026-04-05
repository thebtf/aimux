#!/bin/bash
# CI stub detection scanner — runs in GitHub Actions.
# Exits 1 if CRITICAL stub patterns found, 0 otherwise.
# Constitution P17: No Stubs.

set -e
ERRORS=0
ROOT="${1:-.}"

echo "=== CI Stub Detection Scanner ==="

# STUB-DISCARD: _ = expr (excluding known-safe patterns)
DISCARD=$(grep -rn --include="*.go" --exclude="*_test.go" \
    '	_ = ' "$ROOT/pkg/" 2>/dev/null | \
    grep -v '_ = err' | \
    grep -v '_ = range' | \
    grep -v '_ = .*\.\(.*\)' | \
    grep -v '_ = killProcess\|_ = cmd\.\|_ = ptmx\.\|_ = s\.stdin\.\|_ = log\.' | \
    grep -v '//nolint' | \
    grep -v 'string\|checklist\|prompt\|STUB-\|example' || true)

if [ -n "$DISCARD" ]; then
    echo "STUB-DISCARD findings:"
    echo "$DISCARD"
    ERRORS=$((ERRORS + 1))
fi

# STUB-HARDCODED: Known stub keywords in string literals
HARDCODED=$(grep -rn --include="*.go" --exclude="*_test.go" \
    -iE '"[^"]*((delegating to|wiring pending|not yet implemented|placeholder|scaffold)[^"]*)"' \
    "$ROOT/pkg/" 2>/dev/null | \
    grep -v 'audit\|prompt\|checklist\|godox\|grep\|keywords\|scanner\|STUB-\|example\|review\|config\|string\|rule' || true)

if [ -n "$HARDCODED" ]; then
    echo "STUB-HARDCODED findings:"
    echo "$HARDCODED"
    ERRORS=$((ERRORS + 1))
fi

# STUB-PASSTHROUGH: _ = computed (most critical)
PASSTHROUGH=$(grep -rn --include="*.go" --exclude="*_test.go" \
    '_ = [a-zA-Z]*Params\|_ = [a-zA-Z]*Result\|_ = [a-zA-Z]*Config' \
    "$ROOT/pkg/" 2>/dev/null || true)

if [ -n "$PASSTHROUGH" ]; then
    echo "STUB-PASSTHROUGH findings (CRITICAL):"
    echo "$PASSTHROUGH"
    ERRORS=$((ERRORS + 1))
fi

echo ""
if [ $ERRORS -gt 0 ]; then
    echo "FAILED: $ERRORS stub pattern category(ies) found"
    exit 1
fi

echo "PASSED: 0 stub patterns found"
exit 0
