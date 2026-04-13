package commands

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setupTemplateEnv redirects EZSTACK_HOME to a t.TempDir and returns the
// template root path that applyTemplate will read from for the given template
// name. The returned path is a fresh directory ready to be populated.
func setupTemplateEnv(t *testing.T, name string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("EZSTACK_HOME", home)
	tmplRoot := filepath.Join(home, "templates", name)
	if err := os.MkdirAll(tmplRoot, 0755); err != nil {
		t.Fatal(err)
	}
	return tmplRoot
}

func TestApplyTemplate_CopiesFileAndExecBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec-bit semantics are POSIX-only")
	}
	src := setupTemplateEnv(t, "basic")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := applyTemplate("basic", dest); err != nil {
		t.Fatalf("applyTemplate: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "README.md"))
	if err != nil || string(got) != "hello" {
		t.Errorf("README content = %q err=%v", string(got), err)
	}

	fi, err := os.Stat(filepath.Join(dest, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0111 == 0 {
		t.Errorf("run.sh should retain exec bit, got mode %v", fi.Mode().Perm())
	}
}

func TestApplyTemplate_NestedDirs(t *testing.T) {
	src := setupTemplateEnv(t, "nested")
	if err := os.MkdirAll(filepath.Join(src, "a", "b", "c"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a", "b", "c", "deep.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := applyTemplate("nested", dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a", "b", "c", "deep.txt")); err != nil {
		t.Errorf("nested file missing: %v", err)
	}
}

func TestApplyTemplate_SkipsDotGit(t *testing.T) {
	src := setupTemplateEnv(t, "gittest")
	if err := os.MkdirAll(filepath.Join(src, ".git", "objects"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("k"), 0644); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := applyTemplate("gittest", dest); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should be skipped, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "keep.txt")); err != nil {
		t.Errorf("non-.git file missing: %v", err)
	}
}

func TestApplyTemplate_RejectsSymlinkRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are POSIX-only in this test")
	}
	home := t.TempDir()
	t.Setenv("EZSTACK_HOME", home)
	// Create a real dir somewhere and a symlink at templates/<name>.
	realDir := filepath.Join(home, "real")
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	tmplParent := filepath.Join(home, "templates")
	if err := os.MkdirAll(tmplParent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(tmplParent, "linked")); err != nil {
		t.Fatal(err)
	}

	err := applyTemplate("linked", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink rejection, got %v", err)
	}
}

func TestApplyTemplate_SymlinkInsideTemplateCopiedAsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are POSIX-only in this test")
	}
	src := setupTemplateEnv(t, "linkbody")
	if err := os.WriteFile(filepath.Join(src, "target.txt"), []byte("body"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(src, "alias.txt")); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := applyTemplate("linkbody", dest); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(filepath.Join(dest, "alias.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("alias.txt should be a symlink in dest, got mode %v", fi.Mode())
	}
	got, err := os.Readlink(filepath.Join(dest, "alias.txt"))
	if err != nil || got != "target.txt" {
		t.Errorf("readlink = %q err=%v, want target.txt", got, err)
	}
}

func TestApplyTemplate_ReplacesPreExistingDestSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are POSIX-only in this test")
	}
	src := setupTemplateEnv(t, "replace")
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("fresh"), 0644); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	// Pre-place a dangling symlink at dest/file.txt pointing to an outside path.
	outside := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(outside, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "file.txt")); err != nil {
		t.Fatal(err)
	}

	if err := applyTemplate("replace", dest); err != nil {
		t.Fatal(err)
	}

	// The symlink should have been removed and replaced with a regular file
	// whose content is "fresh". The outside victim must remain untouched.
	fi, err := os.Lstat(filepath.Join(dest, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Error("dest/file.txt should no longer be a symlink")
	}
	got, _ := os.ReadFile(filepath.Join(dest, "file.txt"))
	if string(got) != "fresh" {
		t.Errorf("dest file content = %q, want fresh", string(got))
	}
	victim, _ := os.ReadFile(outside)
	if string(victim) != "original" {
		t.Errorf("symlink target was clobbered: %q", string(victim))
	}
}
