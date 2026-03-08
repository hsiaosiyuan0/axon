#!/usr/bin/env bash
# check-release.sh — Pre-release checklist for Axon
# Run before tagging a release to verify everything is in order.

set -e

AXON=${AXON:-./axon}
PASS=0
FAIL=0

check() {
    local desc="$1"
    local cmd="$2"
    if eval "$cmd" > /dev/null 2>&1; then
        echo "  ✅ $desc"
        PASS=$((PASS + 1))
    else
        echo "  ❌ $desc"
        echo "     CMD: $cmd"
        FAIL=$((FAIL + 1))
    fi
}

echo "🦞 Axon Release Checklist"
echo "========================="
echo ""

echo "📦 Binary"
check "Binary exists" "test -f $AXON"
check "Binary is executable" "test -x $AXON"
check "Help works" "$AXON --help"
check "Version flag works" "$AXON --version"

echo ""
echo "🧪 Tests"
export PATH="/usr/local/Cellar/go/1.26.1/libexec/bin:$PATH"
check "go test passes" "CGO_ENABLED=1 go test -tags fts5 ./..."
check "go vet passes" "CGO_ENABLED=1 go vet -tags fts5 ./..."

echo ""
echo "📁 Files"
check "README.md exists" "test -f README.md"
check "CHANGELOG.md exists" "test -f CHANGELOG.md"
check "LICENSE exists" "test -f LICENSE"
check "Makefile exists" "test -f Makefile"
check ".github/workflows/ci.yml exists" "test -f .github/workflows/ci.yml"
check ".github/workflows/release.yml exists" "test -f .github/workflows/release.yml"
check "docs/README_zh.md exists" "test -f docs/README_zh.md"

echo ""
echo "🔧 Commands"
TMPDB=$(mktemp -d)/check.db
check "axon init" "$AXON --db $TMPDB init"
check "axon collection new" "$AXON --db $TMPDB collection new --name test --type notes"
check "axon collection list" "$AXON --db $TMPDB collection list"
check "axon status" "$AXON --db $TMPDB status"
check "axon list" "$AXON --db $TMPDB list"

TMPFILE=$(mktemp /tmp/axon_check.md)
echo "# Test\n\nThis is a test document for Axon release check." > "$TMPFILE"
check "axon add" "$AXON --db $TMPDB add $TMPFILE -c test"
check "axon query" "$AXON --db $TMPDB query 'test document'"
check "axon list after add" "$AXON --db $TMPDB list"
rm -f "$TMPFILE"

echo ""
echo "══════════════════════════════════"
echo "  Passed: $PASS  Failed: $FAIL"
echo "══════════════════════════════════"

if [ $FAIL -gt 0 ]; then
    echo "❌ Release check FAILED — fix the above issues before releasing."
    exit 1
else
    echo "✅ All checks passed — ready to release!"
    echo ""
    echo "  Next: git tag v0.1.0 && git push origin v0.1.0"
fi
