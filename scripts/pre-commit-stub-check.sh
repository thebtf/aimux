#!/bin/bash
# Anti-stub pre-commit hook — blocks commits containing stub patterns.
# Constitution P17: No Stubs — Every Code Path Must Produce Real Behavior.
#
# Install: ln -sf ../../scripts/pre-commit-stub-check.sh .git/hooks/pre-commit
# Or: cp scripts/pre-commit-stub-check.sh .git/hooks/pre-commit

set -e

ERRORS=0

# Get staged Go files (excluding test files for some rules)
STAGED_GO=$(git diff --cached --name-only --diff-filter=ACM | grep '\.go$' || true)
STAGED_GO_IMPL=$(echo "$STAGED_GO" | grep -v '_test\.go$' || true)

if [ -z "$STAGED_GO" ]; then
    exit 0  # No Go files staged
fi

echo "Anti-stub check: scanning $(echo "$STAGED_GO" | wc -w) staged Go files..."

# STUB-DISCARD: _ = expr (excluding known-safe patterns)
for file in $STAGED_GO_IMPL; do
    # Get only the staged content (not working tree)
    matches=$(git diff --cached -- "$file" | grep '^+' | grep -v '^+++' | \
        grep '_ = ' | \
        grep -v '_ = err' | \
        grep -v '_ = range' | \
        grep -v '_ = .*\.\(.*\)' | \
        grep -v '//nolint' || true)

    if [ -n "$matches" ]; then
        echo "STUB-DISCARD in $file:"
        echo "$matches"
        ERRORS=$((ERRORS + 1))
    fi
done

# STUB-HARDCODED: Known stub keywords in string literals
STUB_KEYWORDS='(delegating to|wiring pending|not yet implemented|placeholder|scaffold|API integration pending|not yet fully implemented)'
for file in $STAGED_GO_IMPL; do
    matches=$(git diff --cached -- "$file" | grep '^+' | grep -v '^+++' | \
        grep -iE "\"[^\"]*${STUB_KEYWORDS}[^\"]*\"" || true)

    if [ -n "$matches" ]; then
        echo "STUB-HARDCODED in $file:"
        echo "$matches"
        ERRORS=$((ERRORS + 1))
    fi
done

# STUB-TODO: TODO/FIXME/SCAFFOLD/PLACEHOLDER in new lines (not test files)
for file in $STAGED_GO_IMPL; do
    matches=$(git diff --cached -- "$file" | grep '^+' | grep -v '^+++' | \
        grep -E '(TODO|FIXME|SCAFFOLD|PLACEHOLDER|HACK)[\s:]' | \
        grep -v 'stubs-quality.*TODO' || true)  # exclude audit prompt text

    if [ -n "$matches" ]; then
        echo "STUB-TODO in $file:"
        echo "$matches"
        ERRORS=$((ERRORS + 1))
    fi
done

if [ $ERRORS -gt 0 ]; then
    echo ""
    echo "BLOCKED: $ERRORS stub pattern(s) detected."
    echo "Fix the patterns above or add //nolint:stub-* comment with justification."
    echo "See: config/audit-rules.d/stub-detection.yaml for rule definitions."
    exit 1
fi

echo "Anti-stub check: PASSED (0 patterns found)"
exit 0
