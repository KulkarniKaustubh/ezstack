#!/bin/bash
# Integration tests for ezstack reparent command
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
EZS="$PROJECT_DIR/bin/ezs-go"
TEST_DIR="$PROJECT_DIR/test/testrepo_reparent"
WORKTREE_DIR="$PROJECT_DIR/test/worktrees_reparent"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() {
    echo -e "${GREEN}[TEST]${NC} $1"
}

error() {
    echo -e "${RED}[FAIL]${NC} $1"
    exit 1
}

assert_parent() {
    local branch="$1"
    local expected_parent="$2"
    # Check the parent by looking at ezs status output or config
    local actual_parent=$(grep -A5 "\"name\": \"$branch\"" ~/.ezstack/stacks.json | grep '"parent"' | head -1 | sed 's/.*: "\([^"]*\)".*/\1/')
    if [ "$actual_parent" != "$expected_parent" ]; then
        error "Branch $branch has parent '$actual_parent', expected '$expected_parent'"
    fi
    log "✓ Branch $branch has parent $expected_parent"
}

cleanup() {
    log "Cleaning up test directories..."
    rm -rf "$TEST_DIR" "$WORKTREE_DIR"
    rm -rf ~/.ezstack  # Clean config for test
}

# Cleanup first
cleanup

log "Building ezstack..."
cd "$PROJECT_DIR"
go build -o bin/ezs-go ./cmd/ezs

log "Creating test repository at $TEST_DIR"
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

git init
git config user.email "test@test.com"
git config user.name "Test User"

# Create initial commit on main
echo "# Test Repo" > README.md
git add README.md
git commit -m "Initial commit"

log "Configuring ezstack..."
"$EZS" config set worktree_base_dir "$WORKTREE_DIR"

# ============================================================================
# Test 1: Reparent within same stack (A -> B -> C becomes A -> C, B stays child of A)
# ============================================================================
log ""
log "=== Test 1: Reparent within same stack ==="

log "Creating stack: main -> feature-a -> feature-b -> feature-c"
echo "y" | "$EZS" new feature-a

cd "$WORKTREE_DIR/feature-a"
echo "Feature A" > a.txt
git add a.txt
git commit -m "Add feature A"

echo "y" | "$EZS" new feature-b
cd "$WORKTREE_DIR/feature-b"
echo "Feature B" > b.txt
git add b.txt
git commit -m "Add feature B"

echo "y" | "$EZS" new feature-c
cd "$WORKTREE_DIR/feature-c"
echo "Feature C" > c.txt
git add c.txt
git commit -m "Add feature C"

log "Before reparent - showing stack:"
"$EZS" ls

log "Reparenting feature-c to feature-a (skipping feature-b)..."
"$EZS" reparent feature-c feature-a --no-rebase <<< "y"

log "After reparent - showing stack:"
"$EZS" ls

assert_parent "feature-c" "feature-a"
assert_parent "feature-b" "feature-a"  # Should still be child of a

log "✓ Test 1 passed: Reparent within same stack works"

# ============================================================================
# Test 2: Reparent to main (splitting a stack)
# ============================================================================
log ""
log "=== Test 2: Reparent to main (split stack) ==="

log "Reparenting feature-b to main..."
"$EZS" reparent feature-b main --no-rebase <<< "y"

log "After reparent - showing stacks:"
"$EZS" ls

assert_parent "feature-b" "main"

log "Verifying ezs status works..."
"$EZS" status

log "✓ Test 2 passed: Reparent to main works"

# ============================================================================
# Test 3: Add standalone branch to existing stack
# ============================================================================
log ""
log "=== Test 3: Add standalone branch to existing stack ==="

log "Creating a standalone git worktree (not through ezs)..."
cd "$TEST_DIR"
git worktree add "$WORKTREE_DIR/standalone" -b standalone
cd "$WORKTREE_DIR/standalone"
echo "Standalone work" > standalone.txt
git add standalone.txt
git commit -m "Add standalone work"

log "Reparenting standalone to feature-a..."
cd "$WORKTREE_DIR/feature-a"
"$EZS" reparent standalone feature-a --no-rebase <<< "y"

log "After reparent - showing stacks:"
"$EZS" ls

assert_parent "standalone" "feature-a"

log "✓ Test 3 passed: Add standalone branch to stack works"

# ============================================================================
# Test 4: Verify sync works after reparent
# ============================================================================
log ""
log "=== Test 4: Verify sync works after reparent ==="

log "Adding more commits to feature-a..."
cd "$WORKTREE_DIR/feature-a"
echo "More A work" >> a.txt
git add a.txt
git commit -m "Update feature A"

log "Running sync on children..."
"$EZS" sync --children

log "Verifying feature-c has the new content from feature-a..."
cd "$WORKTREE_DIR/feature-c"
if grep -q "More A work" a.txt 2>/dev/null; then
    log "✓ feature-c has the rebased content"
else
    error "feature-c is missing the rebased content from feature-a"
fi

log "✓ Test 4 passed: Sync works after reparent"

# ============================================================================
# Test 5: Cycle detection
# ============================================================================
log ""
log "=== Test 5: Verify cycle detection ==="

log "Attempting to reparent feature-a to feature-c (should fail)..."
cd "$WORKTREE_DIR/feature-a"
if "$EZS" reparent feature-a feature-c --no-rebase <<< "y" 2>&1 | grep -q "circular"; then
    log "✓ Cycle detection correctly prevented circular dependency"
else
    error "Cycle detection failed - should have prevented reparenting feature-a to its descendant"
fi

log "✓ Test 5 passed: Cycle detection works"

# ============================================================================
# Summary
# ============================================================================
log ""
log "============================================"
log "✅ All reparent integration tests passed!"
log "============================================"
echo ""
echo "Test directories:"
echo "  Main repo: $TEST_DIR"
echo "  Worktrees: $WORKTREE_DIR"
echo ""
echo "To cleanup, run: rm -rf $TEST_DIR $WORKTREE_DIR ~/.ezstack"

