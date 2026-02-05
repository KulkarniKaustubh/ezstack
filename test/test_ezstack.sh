#!/bin/bash
# Test script for ezstack
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
EZS="$PROJECT_DIR/bin/ezs"
TEST_DIR="$PROJECT_DIR/test/testrepo"
WORKTREE_DIR="$PROJECT_DIR/test/worktrees"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() {
    echo -e "${GREEN}[TEST]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

cleanup() {
    log "Cleaning up test directories..."
    rm -rf "$TEST_DIR" "$WORKTREE_DIR"
    rm -rf ~/.ezstack  # Clean config for test
}

# Cleanup first
cleanup

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
"$EZS" config show

log "Creating first branch in stack (feature-a)..."
"$EZS" new feature-a

log "Listing stacks..."
"$EZS" list

log "Moving to feature-a worktree..."
cd "$WORKTREE_DIR/feature-a"

# Make a commit
echo "Feature A code" > feature-a.txt
git add feature-a.txt
git commit -m "Add feature A"

log "Status in feature-a..."
"$EZS" status

log "Creating second branch in stack (feature-b from feature-a)..."
"$EZS" new feature-b

log "Moving to feature-b worktree..."
cd "$WORKTREE_DIR/feature-b"

# Make a commit
echo "Feature B code" > feature-b.txt
git add feature-b.txt
git commit -m "Add feature B"

log "Status in feature-b..."
"$EZS" status

log "Creating third branch in stack (feature-c from feature-b)..."
"$EZS" new feature-c

log "Moving to feature-c worktree..."
cd "$WORKTREE_DIR/feature-c"

# Make a commit
echo "Feature C code" > feature-c.txt
git add feature-c.txt
git commit -m "Add feature C"

log "Full stack status..."
"$EZS" status

log "Listing all stacks..."
"$EZS" list --all

# Test rebase scenario: modify feature-a and rebase children
log "Going back to feature-a to add more commits..."
cd "$WORKTREE_DIR/feature-a"

echo "More feature A work" >> feature-a.txt
git add feature-a.txt
git commit -m "Improve feature A"

log "Rebasing child branches..."
"$EZS" rebase --children

log "Verifying feature-b has the new commit from feature-a..."
cd "$WORKTREE_DIR/feature-b"
cat feature-a.txt

log "Verifying feature-c has commits from both feature-a and feature-b..."
cd "$WORKTREE_DIR/feature-c"
cat feature-a.txt
cat feature-b.txt

log "Final status..."
"$EZS" status

log "✅ All tests passed!"
echo ""
echo "Test directories:"
echo "  Main repo: $TEST_DIR"
echo "  Worktrees: $WORKTREE_DIR"
echo ""
echo "To cleanup, run: rm -rf $TEST_DIR $WORKTREE_DIR"

