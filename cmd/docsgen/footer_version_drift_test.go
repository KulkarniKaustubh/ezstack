package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDocsFooterVersionMatchesVERSION pins the contract that every
// published docs/*.html page renders the same version in its footer-meta
// line as the canonical VERSION file. The previous failure mode: a page
// was added (docs/mcp.html) and never registered in scripts/bump-version.sh's
// docs loop, so its footer drifted across releases (got stuck at v4.7.5
// while every other page advanced to v4.7.6) — invisible until somebody
// happened to read that one page.
//
// This test catches that class structurally: any html file under docs/
// that carries a `class="footer-meta"` line with a `vX.Y.Z` token must
// exactly match VERSION. A new page added without updating bump-version.sh
// will fail here on the next release rather than drifting silently.
func TestDocsFooterVersionMatchesVERSION(t *testing.T) {
	repoRoot := findRepoRoot(t)

	versionRaw, err := os.ReadFile(filepath.Join(repoRoot, "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	want := "v" + strings.TrimSpace(string(versionRaw))

	docsDir := filepath.Join(repoRoot, "docs")
	entries, err := os.ReadDir(docsDir)
	if err != nil {
		t.Fatalf("read docs dir: %v", err)
	}

	// Match the rendered footer line: `<span class="footer-meta">…vX.Y.Z…</span>`.
	// The middle separator is &middot; on most pages and · on
	// docs/documentation.html (regenerated from DOCUMENTATION.md), so
	// don't anchor on it.
	footerRe := regexp.MustCompile(`class="footer-meta"[^<]*?(v\d+\.\d+\.\d+)`)

	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		path := filepath.Join(docsDir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		m := footerRe.FindStringSubmatch(string(raw))
		if m == nil {
			// Pages that don't carry a footer-meta version (e.g.
			// google site-verification stub) are exempt.
			continue
		}
		checked++
		if m[1] != want {
			t.Errorf("docs/%s footer = %q, want %q (this page is missing from scripts/bump-version.sh's docs loop)", e.Name(), m[1], want)
		}
	}
	if checked == 0 {
		t.Fatal("no docs/*.html with footer-meta version found — test pattern is wrong or pages were renamed")
	}
}
