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

# 2. Centralized Go version constant — cmd/ezs and cmd/ezs-mcp both import this.
FILE="$REPO_ROOT/internal/version/version.go"
if [ -f "$FILE" ]; then
    sed -i.bak "s/const Version = \"$SV\"/const Version = \"$NEW_VERSION\"/" "$FILE"
    changed+=("internal/version/version.go")
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

# 6. Tauri desktop frontend package.json
FILE="$REPO_ROOT/tauri-ui/package.json"
if [ -f "$FILE" ]; then
    sed -i.bak "s/\"version\": \"$SV\"/\"version\": \"$NEW_VERSION\"/" "$FILE"
    changed+=("tauri-ui/package.json")
fi

# 7. Tauri desktop frontend package-lock.json
FILE="$REPO_ROOT/tauri-ui/package-lock.json"
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
    changed+=("tauri-ui/package-lock.json")
fi

# 7b. Tauri desktop frontend version constant — displayed in the title bar and
#     settings dialog. Until this file is bumped, the desktop UI shows a stale
#     version string even though the Cargo binary itself reports the right one.
FILE="$REPO_ROOT/tauri-ui/src/version.ts"
if [ -f "$FILE" ]; then
    sed -i.bak "s/APP_VERSION = \"[^\"]*\"/APP_VERSION = \"$NEW_VERSION\"/" "$FILE"
    changed+=("tauri-ui/src/version.ts")
fi

# 8. Tauri desktop tauri.conf.json
FILE="$REPO_ROOT/tauri-ui/src-tauri/tauri.conf.json"
if [ -f "$FILE" ]; then
    sed -i.bak "s/\"version\": \"$SV\"/\"version\": \"$NEW_VERSION\"/" "$FILE"
    changed+=("tauri-ui/src-tauri/tauri.conf.json")
fi

# 9. Tauri desktop Cargo.toml — match the package's [package] version line.
#    Anchored to the line that immediately follows `name = "ezstack-desktop"` so
#    we don't clobber dependency versions further down the file.
FILE="$REPO_ROOT/tauri-ui/src-tauri/Cargo.toml"
if [ -f "$FILE" ]; then
    sed -i.bak "/^name = \"ezstack-desktop\"$/{n;s/^version = \"$SV\"$/version = \"$NEW_VERSION\"/;}" "$FILE"
    changed+=("tauri-ui/src-tauri/Cargo.toml")
fi

# 10. Tauri desktop Cargo.lock — same anchored approach.
FILE="$REPO_ROOT/tauri-ui/src-tauri/Cargo.lock"
if [ -f "$FILE" ]; then
    sed -i.bak "/^name = \"ezstack-desktop\"$/{n;s/^version = \"$SV\"$/version = \"$NEW_VERSION\"/;}" "$FILE"
    changed+=("tauri-ui/src-tauri/Cargo.lock")
fi

# 11. DOCUMENTATION.md — the source of truth for the rendered docs HTML. Match
#     the `ezstack version X.Y.Z` example output and any `ezstack-X.Y.Z.vsix`
#     install snippets. `make docs` regenerates docs/documentation.html from
#     this file later in the release pipeline, so docs/documentation.html is
#     intentionally re-bumped in step 12 even though docsgen will overwrite it.
FILE="$REPO_ROOT/DOCUMENTATION.md"
if [ -f "$FILE" ]; then
    sed -i.bak \
        -e "s/ezstack version $SV/ezstack version $NEW_VERSION/g" \
        -e "s/\(ezstack[-_]\)$SV\.vsix/\1$NEW_VERSION.vsix/g" \
        "$FILE"
    changed+=("DOCUMENTATION.md")
fi

# 12. Docs HTML files — rewrite any `vX.Y.Z` or `ezstack[-_]X.Y.Z` token to the new
#     version. Using a regex (instead of matching OLD_VERSION literally) means stale
#     refs from prior skipped bumps get caught automatically on the next run.
for doc in docs/index.html docs/vscode.html docs/agent.html docs/nvim.html docs/desktop.html docs/documentation.html; do
    FILE="$REPO_ROOT/$doc"
    if [ -f "$FILE" ]; then
        sed -i.bak \
            -e "s/v$SV/v$NEW_VERSION/g" \
            -e "s/\(ezstack[-_]\)$SV/\1$NEW_VERSION/g" \
            "$FILE"
        changed+=("$doc")
    fi
done

# Clean up .bak files left by sed -i.bak
find "$REPO_ROOT" -name "*.bak" -not -path "*/node_modules/*" -delete 2>/dev/null || true

echo "Updated ${#changed[@]} files:"
for f in "${changed[@]}"; do
    echo "  ✓ $f"
done
