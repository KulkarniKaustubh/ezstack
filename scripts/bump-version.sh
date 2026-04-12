#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION_FILE="$REPO_ROOT/VERSION"
# Portable semver pattern for sed (BRE — works on both GNU and BSD sed)
SV='[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*'

if [ $# -ne 1 ]; then
    echo "Usage: $0 <new-version>"
    echo "Example: $0 3.2.7"
    exit 1
fi

NEW_VERSION="$1"

if ! [[ "$NEW_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: version must be in semver format (e.g. 3.2.7)"
    exit 1
fi

if [ ! -f "$VERSION_FILE" ]; then
    echo "Error: VERSION file not found at $VERSION_FILE"
    exit 1
fi

OLD_VERSION="$(cat "$VERSION_FILE" | tr -d '[:space:]')"

echo "Bumping version: $OLD_VERSION → $NEW_VERSION"
echo ""

# Track files changed
changed=()

# 1. VERSION file
printf '%s' "$NEW_VERSION" > "$VERSION_FILE"
changed+=("VERSION")

# 2. Go CLI constant — match any semver for idempotent runs
FILE="$REPO_ROOT/cmd/ezs/main.go"
if [ -f "$FILE" ]; then
    sed -i.bak "s/const version = \"$SV\"/const version = \"$NEW_VERSION\"/" "$FILE"
    changed+=("cmd/ezs/main.go")
fi

# 3. VS Code extension package.json — match any semver in the "version" field
FILE="$REPO_ROOT/vscode-extension/package.json"
if [ -f "$FILE" ]; then
    sed -i.bak "s/\"version\": \"$SV\"/\"version\": \"$NEW_VERSION\"/" "$FILE"
    changed+=("vscode-extension/package.json")
fi

# 4. VS Code extension package-lock.json — update via node for precision
FILE="$REPO_ROOT/vscode-extension/package-lock.json"
if [ -f "$FILE" ] && command -v node >/dev/null 2>&1; then
    node -e "
      const fs = require('fs');
      const lock = JSON.parse(fs.readFileSync('$FILE', 'utf8'));
      lock.version = '$NEW_VERSION';
      if (lock.packages && lock.packages['']) {
        lock.packages[''].version = '$NEW_VERSION';
      }
      fs.writeFileSync('$FILE', JSON.stringify(lock, null, 2) + '\n');
    "
    changed+=("vscode-extension/package-lock.json")
elif [ -f "$FILE" ] && [ "$OLD_VERSION" != "$NEW_VERSION" ]; then
    sed -i.bak "s/\"version\": \"$OLD_VERSION\"/\"version\": \"$NEW_VERSION\"/g" "$FILE"
    changed+=("vscode-extension/package-lock.json")
fi

# 5. VS Code extension README — match any semver in .vsix filename
FILE="$REPO_ROOT/vscode-extension/README.md"
if [ -f "$FILE" ]; then
    sed -i.bak "s/ezstack-$SV\.vsix/ezstack-$NEW_VERSION.vsix/g" "$FILE"
    changed+=("vscode-extension/README.md")
fi

# 6-8. Docs HTML files — use OLD_VERSION to avoid clobbering unrelated version strings
#      Skipped when OLD_VERSION == NEW_VERSION (already correct)
if [ "$OLD_VERSION" != "$NEW_VERSION" ]; then
    for doc in docs/index.html docs/vscode.html docs/agent.html; do
        FILE="$REPO_ROOT/$doc"
        if [ -f "$FILE" ]; then
            sed -i.bak "s/$OLD_VERSION/$NEW_VERSION/g" "$FILE"
            changed+=("$doc")
        fi
    done
fi

# Clean up .bak files left by sed -i.bak
find "$REPO_ROOT" -name "*.bak" -not -path "*/node_modules/*" -delete 2>/dev/null || true

echo "Updated ${#changed[@]} files:"
for f in "${changed[@]}"; do
    echo "  ✓ $f"
done
