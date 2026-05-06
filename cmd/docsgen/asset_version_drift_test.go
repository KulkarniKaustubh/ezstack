package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAssetVersionMatchesVERSION pins the contract that every install snippet
// referencing a versioned asset filename — `ezstack-X.Y.Z.vsix` (VS Code
// extension) and `ezstack_X.Y.Z_*` (Tauri desktop bundles) — matches VERSION.
//
// Companion to TestDocsFooterVersionMatchesVERSION. The footer test catches
// HTML pages whose chrome was forgotten in scripts/bump-version.sh's docs
// loop. This test catches install commands in *.md / *.html bodies whose
// regex was never wired into the bump script at all.
//
// Concrete failure mode this guards against: scripts/bump-version.sh step 5
// rewrites `ezstack-$SV.vsix` only inside `vscode-extension/README.md`. The
// repo-root README.md carried the same install snippet but was missing from
// the bump loop entirely, so the v4.7.6 release shipped main with
// `ezstack-4.0.0.vsix` in the install command (a 4-major-version drift).
//
// New asset-named files (e.g. a future tarball pattern) belong in the
// regex below — adding one without listing it here lets the new file
// drift silently across releases.
func TestAssetVersionMatchesVERSION(t *testing.T) {
	repoRoot := findRepoRoot(t)

	versionRaw, err := os.ReadFile(filepath.Join(repoRoot, "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	want := strings.TrimSpace(string(versionRaw))

	// Files whose body carries versioned asset filenames. Anything outside
	// this list is intentionally exempt (e.g. CHANGELOG entries that
	// reference historical version strings, or example snippets pinning a
	// specific older tag like `ezs upgrade --version v4.6.0`).
	files := []string{
		"README.md",
		"DOCUMENTATION.md",
		"vscode-extension/README.md",
		"docs/documentation.html",
		"docs/desktop.html",
		"docs/index.html",
		"docs/vscode.html",
	}

	// Match any `ezstack-X.Y.Z.vsix` (VS Code extension VSIX) or
	// `ezstack_X.Y.Z_<rest>` (Tauri desktop installers — _universal.dmg,
	// _x64_en-US.msi, _amd64.AppImage). Capture the version triple so we
	// can compare it against VERSION.
	tokenRe := regexp.MustCompile(`ezstack[-_](\d+\.\d+\.\d+)`)

	for _, rel := range files {
		path := filepath.Join(repoRoot, rel)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		matches := tokenRe.FindAllStringSubmatch(string(raw), -1)
		if matches == nil {
			// No versioned asset reference in this file — fine; the file
			// is in the list because it has historically carried one and
			// we want a noisy failure if a new drift appears.
			continue
		}
		for _, m := range matches {
			if m[1] != want {
				t.Errorf("%s: asset version %q drifted from VERSION %q (token %q)\n  → ensure scripts/bump-version.sh rewrites this file's install snippet",
					rel, m[1], want, m[0])
			}
		}
	}
}
