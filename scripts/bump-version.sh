#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION_FILE="$REPO_ROOT/VERSION"

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

if [ "$OLD_VERSION" = "$NEW_VERSION" ]; then
    echo "Version is already $NEW_VERSION"
    exit 0
fi

echo "Bumping version: $OLD_VERSION → $NEW_VERSION"
echo ""

# Track files changed
changed=()

# 1. VERSION file
printf '%s' "$NEW_VERSION" > "$VERSION_FILE"
changed+=("VERSION")

# 2. Go CLI constant
FILE="$REPO_ROOT/cmd/ezs/main.go"
if [ -f "$FILE" ]; then
    sed -i.bak "s/const version = \"$OLD_VERSION\"/const version = \"$NEW_VERSION\"/" "$FILE"
    changed+=("cmd/ezs/main.go")
fi

# 3. VS Code extension package.json
FILE="$REPO_ROOT/vscode-extension/package.json"
if [ -f "$FILE" ]; then
    sed -i.bak "s/\"version\": \"$OLD_VERSION\"/\"version\": \"$NEW_VERSION\"/" "$FILE"
    changed+=("vscode-extension/package.json")
fi

# 4. VS Code extension package-lock.json
FILE="$REPO_ROOT/vscode-extension/package-lock.json"
if [ -f "$FILE" ]; then
    sed -i.bak "s/\"version\": \"$OLD_VERSION\"/\"version\": \"$NEW_VERSION\"/g" "$FILE"
    changed+=("vscode-extension/package-lock.json")
fi

# 5. VS Code extension README
FILE="$REPO_ROOT/vscode-extension/README.md"
if [ -f "$FILE" ]; then
    sed -i.bak "s/ezstack-$OLD_VERSION\.vsix/ezstack-$NEW_VERSION.vsix/g" "$FILE"
    changed+=("vscode-extension/README.md")
fi

# 6-8. Docs HTML files (index.html, vscode.html, agent.html)
for doc in docs/index.html docs/vscode.html docs/agent.html; do
    FILE="$REPO_ROOT/$doc"
    if [ -f "$FILE" ]; then
        sed -i.bak "s/$OLD_VERSION/$NEW_VERSION/g" "$FILE"
        changed+=("$doc")
    fi
done

# Clean up .bak files left by sed -i.bak
find "$REPO_ROOT" -name "*.bak" -not -path "*/node_modules/*" -delete 2>/dev/null || true

echo "Updated ${#changed[@]} files:"
for f in "${changed[@]}"; do
    echo "  ✓ $f"
done
echo ""
echo "Next steps:"
echo "  1. git add -p && git commit -m 'bump to v$NEW_VERSION'"
echo "  2. git tag v$NEW_VERSION"
echo "  3. git push origin main --tags"
