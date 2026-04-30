package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"v1.2.3", "v1.3.0", -1},
		{"v2.0.0", "v1.99.99", 1},
		{"4.6.3", "v4.6.3-rc.1", 1},      // GA outranks rc
		{"v4.6.3-rc.1", "4.6.3", -1},     // and vice versa
		{"4.6.3-rc.1", "4.6.3-rc.2", -1}, // pre-release identifiers ordered
		{"4.6.3-rc.2", "4.6.3-rc.1", 1},
		{"4.6.3-rc.1", "4.6.3-rc.1", 0},
		{"4.6.3", "v4.6.4", -1},
		{"", "0.0.1", -1},
		{"0.0.0", "", 0},
		// Build metadata (semver §10) is NOT ordering-relevant.
		{"1.2.3+build.5", "1.2.3", 0},
		{"1.2.3", "1.2.3+build.5", 0},
		{"1.2.3+a", "1.2.3+b", 0},
		{"1.2.3-rc.1+build.5", "1.2.3-rc.1", 0},
		{"1.2.3-rc.1+x", "1.2.3", -1}, // pre-release still less than GA
	}
	for _, c := range cases {
		got := CompareVersions(c.a, c.b)
		if got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAssetNameMatchesRuntime(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("test platform %s/%s is not a published target", runtime.GOOS, runtime.GOARCH)
	}
	got, err := AssetName()
	if err != nil {
		t.Fatalf("AssetName: %v", err)
	}
	want := "ezstack_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	if got != want {
		t.Errorf("AssetName = %q, want %q", got, want)
	}
}

func TestSupportedPlatform(t *testing.T) {
	cases := []struct {
		os, arch string
		ok       bool
	}{
		{"linux", "amd64", true},
		{"linux", "arm64", true},
		{"darwin", "amd64", true},
		{"darwin", "arm64", true},
		{"windows", "amd64", false},
		{"linux", "386", false},
	}
	for _, c := range cases {
		if got := supportedPlatform(c.os, c.arch); got != c.ok {
			t.Errorf("supportedPlatform(%q, %q) = %v, want %v", c.os, c.arch, got, c.ok)
		}
	}
}

func TestDetectInstall(t *testing.T) {
	t.Setenv("GOPATH", "/home/u/go")
	t.Setenv("GOBIN", "")

	cases := []struct {
		path string
		want InstallMethod
	}{
		{"/opt/homebrew/Cellar/ezstack/4.6.3/bin/ezs", InstallHomebrew},
		{"/usr/local/Cellar/ezstack/4.6.3/bin/ezs", InstallHomebrew},
		{"/home/linuxbrew/.linuxbrew/Cellar/ezstack/4.6.3/bin/ezs", InstallHomebrew},
		{"/home/u/go/bin/ezs", InstallGoInstall},
		{"/usr/local/bin/ezs", InstallBinary},
		{"/Users/k/.local/bin/ezs", InstallBinary},
	}
	for _, c := range cases {
		if got := DetectInstall(c.path); got != c.want {
			t.Errorf("DetectInstall(%q) = %v, want %v", c.path, got, c.want)
		}
	}

	t.Run("respects GOBIN", func(t *testing.T) {
		t.Setenv("GOBIN", "/custom/gobin")
		if got := DetectInstall("/custom/gobin/ezs"); got != InstallGoInstall {
			t.Errorf("expected InstallGoInstall for GOBIN path, got %v", got)
		}
	})
}

func TestReadExpectedSum(t *testing.T) {
	dir := t.TempDir()
	sums := filepath.Join(dir, "checksums.txt")
	body := "deadbeef  ezstack_other.tar.gz\nabc123  ezstack_linux_amd64.tar.gz\n"
	if err := os.WriteFile(sums, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readExpectedSum(sums, "ezstack_linux_amd64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc123" {
		t.Errorf("got %q, want abc123", got)
	}
	if _, err := readExpectedSum(sums, "missing.tar.gz"); err == nil {
		t.Error("expected error for missing entry")
	}
}

func TestVerifyAndExtract(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "ezstack_test.tar.gz")
	contents := map[string]string{
		"ezs":       "fake-ezs-binary\n",
		"ezs-mcp":   "fake-mcp-binary\n",
		"README.md": "ignored",
	}
	writeTarGz(t, archive, contents)

	sum := sha256OfFile(t, archive)
	sums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(sums, []byte(sum+"  ezstack_test.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyChecksum(archive, sums, "ezstack_test.tar.gz"); err != nil {
		t.Fatalf("verifyChecksum: %v", err)
	}

	staged, err := extractBinaries(archive, dir, []string{"ezs", "ezs-mcp"})
	if err != nil {
		t.Fatalf("extractBinaries: %v", err)
	}
	for _, name := range []string{"ezs", "ezs-mcp"} {
		path, ok := staged[name]
		if !ok {
			t.Fatalf("missing %s in staged output", name)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(got) != contents[name] {
			t.Errorf("%s: got %q, want %q", name, got, contents[name])
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s should be executable, mode=%v", name, info.Mode())
		}
	}
	if _, ok := staged["README.md"]; ok {
		t.Error("README.md should have been skipped")
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "fake.tar.gz")
	if err := os.WriteFile(archive, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	sums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(sums, []byte("0000000000000000000000000000000000000000000000000000000000000000  fake.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := verifyChecksum(archive, sums, "fake.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "expected") {
		t.Errorf("expected mismatch error, got %v", err)
	}
}

// TestExtractBinariesDuplicate ensures we error rather than silently
// overwrite when a tarball contains the same wanted basename twice
// (e.g. both `ezs` and `bin/ezs`).
func TestExtractBinariesDuplicate(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "dup.tar.gz")
	writeTarGzOrdered(t, archive, []tarEntry{
		{name: "ezs", body: "first"},
		{name: "bin/ezs", body: "second"},
	})
	if _, err := extractBinaries(archive, dir, []string{"ezs"}); err == nil {
		t.Fatal("expected duplicate-entry error")
	} else if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("expected duplicate-entry error, got %v", err)
	}
}

// TestExtractBinariesAtCapBoundary verifies that a body up to the
// per-file cap is preserved exactly (no off-by-one truncation). The
// real cap is 200 MiB, too large for a unit test, so we exercise the
// boundary condition with a small body that should round-trip cleanly.
func TestExtractBinariesAtCapBoundary(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "boundary.tar.gz")
	body := strings.Repeat("a", 1024)
	writeTarGzOrdered(t, archive, []tarEntry{{name: "ezs", body: body}})
	staged, err := extractBinaries(archive, dir, []string{"ezs"})
	if err != nil {
		t.Fatalf("extractBinaries: %v", err)
	}
	got, err := os.ReadFile(staged["ezs"])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("body length got=%d want=%d", len(got), len(body))
	}
}

// TestSnapshotForRollbackHardLink verifies that the backup remains
// readable after the source is overwritten via os.Rename — the property
// the rollback path relies on.
func TestSnapshotForRollbackHardLink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "live")
	backup := filepath.Join(dir, ".live.bak")
	if err := os.WriteFile(src, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := snapshotForRollback(src, backup); err != nil {
		t.Fatalf("snapshotForRollback: %v", err)
	}

	// Overwrite src via rename; backup must still hold the original bytes.
	newFile := filepath.Join(dir, "new")
	if err := os.WriteFile(newFile, []byte("replaced"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(newFile, src); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("backup got %q, want %q", got, "original")
	}

	// Rollback: rename the backup back over src.
	if err := os.Rename(backup, src); err != nil {
		t.Fatalf("rollback rename: %v", err)
	}
	got, err = os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Errorf("post-rollback src got %q, want %q", got, "original")
	}
}

func TestAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "new")
	dst := filepath.Join(dir, "target")
	if err := os.WriteFile(src, []byte("new-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old-content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicReplace(src, dst); err != nil {
		t.Fatalf("atomicReplace: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-content" {
		t.Errorf("got %q, want new-content", got)
	}
}

// fakeRelease starts an httptest.Server pair (asset file server +
// release JSON API) and points apiBaseURL at the API server for the
// duration of the test. The returned binDir holds pre-existing "old"
// ezs and ezs-mcp files that Run() will swap. currentExecutableFn is
// also redirected so Run() classifies the install as Binary against
// our fake binDir, not the test runner's own binary.
//
// Cleanup is registered with t.Cleanup so package-level overrides are
// restored even if the test fails partway.
func fakeRelease(t *testing.T, tag string, contents map[string]string) (binDir string, assetName string) {
	t.Helper()

	// Defensive: make sure DetectInstall doesn't mistake the temp dir
	// for a Go-install path because of a stray developer GOBIN/GOPATH.
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/var/empty/non-existent-gopath")

	releaseDir := t.TempDir()
	assetName = fmt.Sprintf("ezstack_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archive := filepath.Join(releaseDir, assetName)
	writeTarGz(t, archive, contents)

	sum := sha256OfFile(t, archive)
	if err := os.WriteFile(filepath.Join(releaseDir, "checksums.txt"),
		[]byte(sum+"  "+assetName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	assetSrv := httptest.NewServer(http.FileServer(http.Dir(releaseDir)))
	t.Cleanup(assetSrv.Close)

	rel := Release{
		TagName: tag,
		Assets: []Asset{
			{Name: assetName, DownloadURL: assetSrv.URL + "/" + assetName},
			{Name: "checksums.txt", DownloadURL: assetSrv.URL + "/checksums.txt"},
		},
	}
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&rel)
	}))
	t.Cleanup(apiSrv.Close)

	oldBase := apiBaseURL
	apiBaseURL = apiSrv.URL
	t.Cleanup(func() { apiBaseURL = oldBase })

	binDir = t.TempDir()
	for name := range contents {
		// Only seed the binDir with ezs/ezs-mcp; readme-style entries
		// in the tarball stay only in the archive.
		if name != "ezs" && name != "ezs-mcp" {
			continue
		}
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("old-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	fakeExe := filepath.Join(binDir, "ezs")
	oldFn := currentExecutableFn
	currentExecutableFn = func() (string, error) { return fakeExe, nil }
	t.Cleanup(func() { currentExecutableFn = oldFn })

	return binDir, assetName
}

// TestRunFullPipeline drives Run() end-to-end against a local httptest
// release server. It verifies that the install dispatch, parallel
// download, checksum verify, extract, and atomic-replace pipeline all
// stitch together — the path that production users actually exercise.
func TestRunFullPipeline(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s — no published asset", runtime.GOOS, runtime.GOARCH)
	}

	binDir, _ := fakeRelease(t, "v9.9.9", map[string]string{
		"ezs":     "new-ezs-v9.9.9\n",
		"ezs-mcp": "new-mcp-v9.9.9\n",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var logs []string
	res, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("Run: %v\nlogs:\n%s", err, strings.Join(logs, "\n"))
	}
	if res.AlreadyAtTip {
		t.Fatal("Run reported AlreadyAtTip but should have upgraded")
	}
	if res.Method != InstallBinary {
		t.Errorf("Method = %v, want InstallBinary", res.Method)
	}
	if res.From != "1.0.0" || res.To != "9.9.9" {
		t.Errorf("From/To = %q/%q, want 1.0.0/9.9.9", res.From, res.To)
	}
	if len(res.Updated) != 2 {
		t.Fatalf("Updated = %v, want 2 entries", res.Updated)
	}

	for name, want := range map[string]string{
		"ezs":     "new-ezs-v9.9.9\n",
		"ezs-mcp": "new-mcp-v9.9.9\n",
	} {
		got, err := os.ReadFile(filepath.Join(binDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	// No leftover backup files in binDir after a successful upgrade.
	for _, name := range []string{".ezs.ezstack-upgrade-backup", ".ezs-mcp.ezstack-upgrade-backup"} {
		if _, err := os.Stat(filepath.Join(binDir, name)); err == nil {
			t.Errorf("leftover backup %s after success", name)
		}
	}
}

// TestRunCheckOnlyAtTip verifies that --check on a binary already at
// the published tag returns AlreadyAtTip and does not touch disk.
func TestRunCheckOnlyAtTip(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binDir, _ := fakeRelease(t, "v1.2.3", map[string]string{
		"ezs":     "new-ezs\n",
		"ezs-mcp": "new-mcp\n",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := Run(ctx, Options{
		CurrentVersion: "1.2.3",
		CheckOnly:      true,
		IncludeMCP:     true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.AlreadyAtTip {
		t.Error("expected AlreadyAtTip for matching version")
	}
	// Disk should be untouched: original "old-ezs" content still present.
	got, _ := os.ReadFile(filepath.Join(binDir, "ezs"))
	if string(got) != "old-ezs" {
		t.Errorf("ezs was modified during --check: %q", got)
	}
}

// TestRunPinnedAboveTipMessage verifies that --version + --check when
// the running binary is newer than the pinned tag produces the
// pinned-tag-aware message rather than the misleading "already at the
// latest version" text.
func TestRunPinnedAboveTipMessage(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	fakeRelease(t, "v1.0.0", map[string]string{
		"ezs":     "old-ezs\n",
		"ezs-mcp": "old-mcp\n",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var logs []string
	res, err := Run(ctx, Options{
		CurrentVersion: "9.9.9",
		TargetTag:      "v1.0.0",
		CheckOnly:      true,
		IncludeMCP:     true,
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.AlreadyAtTip {
		t.Error("expected AlreadyAtTip when current > pinned target")
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "pinned tag") {
		t.Errorf("expected pinned-tag wording in log, got: %s", joined)
	}
	if strings.Contains(joined, "already at the latest version") {
		t.Errorf("should not claim 'already at the latest version' for a pinned tag, got: %s", joined)
	}
}

// TestRunRollbackOnSecondReplaceFail injects a controlled failure on
// the ezs-mcp swap. The first swap (ezs) completes, then ezs-mcp fails,
// so Run() must roll back ezs to its original content and surface the
// error instead of leaving a version-skewed pair.
func TestRunRollbackOnSecondReplaceFail(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binDir, _ := fakeRelease(t, "v9.9.9", map[string]string{
		"ezs":     "new-ezs\n",
		"ezs-mcp": "new-mcp\n",
	})

	// Inject a failure mode: succeed for ezs, fail for ezs-mcp.
	oldReplace := atomicReplaceFn
	atomicReplaceFn = func(src, dst string) error {
		if filepath.Base(dst) == "ezs-mcp" {
			return errors.New("synthetic failure")
		}
		return oldReplace(src, dst)
	}
	t.Cleanup(func() { atomicReplaceFn = oldReplace })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
	})
	if err == nil {
		t.Fatal("expected error from injected failure")
	}
	if !strings.Contains(err.Error(), "synthetic failure") {
		t.Errorf("expected wrapped synthetic failure, got %v", err)
	}

	// Both targets must hold their original bytes after rollback.
	for _, name := range []string{"ezs", "ezs-mcp"} {
		got, err := os.ReadFile(filepath.Join(binDir, name))
		if err != nil {
			t.Fatalf("read %s after rollback: %v", name, err)
		}
		if string(got) != "old-"+name {
			t.Errorf("%s = %q after rollback, want %q", name, got, "old-"+name)
		}
	}

	// Backup files must be cleaned up after rollback.
	for _, name := range []string{".ezs.ezstack-upgrade-backup", ".ezs-mcp.ezstack-upgrade-backup"} {
		if _, err := os.Stat(filepath.Join(binDir, name)); err == nil {
			t.Errorf("leftover backup %s after rollback", name)
		}
	}
}

// TestRunRefusesConcurrentUpgrade verifies that a second invocation
// fails fast with an "already in progress" message when the first one
// is still holding the lock — prevents the rollback-corruption race
// where two processes' fixed-name backup files clobber each other.
func TestRunRefusesConcurrentUpgrade(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binDir, _ := fakeRelease(t, "v9.9.9", map[string]string{
		"ezs":     "new-ezs\n",
		"ezs-mcp": "new-mcp\n",
	})

	// Hold the lock manually to simulate a sibling upgrade in flight.
	held, err := acquireUpgradeLock(filepath.Join(binDir, ".ezstack-upgrade.lock"))
	if err != nil {
		t.Fatalf("pre-acquire lock: %v", err)
	}
	defer held.release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
	})
	if err == nil {
		t.Fatal("expected lock-conflict error")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected 'already in progress' message, got %v", err)
	}
	// Disk untouched: original "old-ezs" content still present.
	got, _ := os.ReadFile(filepath.Join(binDir, "ezs"))
	if string(got) != "old-ezs" {
		t.Errorf("ezs was modified despite lock conflict: %q", got)
	}
}

// TestRunNetworkErrorClassified verifies that when the API server
// returns a 500, Run() surfaces a *NetworkError so the CLI can route
// it to ExitNetworkError.
func TestRunNetworkErrorClassified(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/var/empty/non-existent-gopath")

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer apiSrv.Close()
	oldBase := apiBaseURL
	apiBaseURL = apiSrv.URL
	t.Cleanup(func() { apiBaseURL = oldBase })

	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "ezs"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeExe := filepath.Join(binDir, "ezs")
	oldFn := currentExecutableFn
	currentExecutableFn = func() (string, error) { return fakeExe, nil }
	t.Cleanup(func() { currentExecutableFn = oldFn })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := Run(ctx, Options{CurrentVersion: "1.0.0"})
	if err == nil {
		t.Fatal("expected error from API 500")
	}
	var net *NetworkError
	if !errors.As(err, &net) {
		t.Errorf("expected *NetworkError in chain, got %T: %v", err, err)
	}
}

// TestResolveMCPTargetSibling verifies the happy path: when ezs-mcp
// lives alongside ezs in binDir, that path is returned and PATH is
// not consulted at all.
func TestResolveMCPTargetSibling(t *testing.T) {
	binDir := t.TempDir()
	mcpPath := filepath.Join(binDir, "ezs-mcp")
	if err := os.WriteFile(mcpPath, []byte("anything"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Even if PATH is empty, the sibling lookup must still succeed.
	t.Setenv("PATH", "")
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/var/empty/non-existent-gopath")

	dst, reason, ok := resolveMCPTarget(binDir)
	if !ok {
		t.Fatalf("expected sibling to be found, got reason=%q", reason)
	}
	if dst != mcpPath {
		t.Errorf("dst = %q, want %q", dst, mcpPath)
	}
	if reason != "" {
		t.Errorf("expected empty reason for sibling, got %q", reason)
	}
}

// TestResolveMCPTargetPATHFallback verifies the new behavior: when
// ezs-mcp does NOT live next to ezs, exec.LookPath is consulted and
// the path it returns is used as the swap destination — even when
// that path lives under a directory that DetectInstall would
// normally classify as InstallGoInstall (~/go/bin or $GOBIN).
//
// This is the bug the user hit: ezs at ~/.local/bin/ but ezs-mcp at
// ~/go/bin/ (planted by `ezs agent`'s `go install`) was silently
// skipped, leaving the pair version-skewed.
func TestResolveMCPTargetPATHFallback(t *testing.T) {
	binDir := t.TempDir()
	mcpDir := t.TempDir()
	mcpPath := filepath.Join(mcpDir, "ezs-mcp")
	if err := os.WriteFile(mcpPath, []byte("anything"), 0o755); err != nil {
		t.Fatal(err)
	}

	// PATH must contain mcpDir; clear GOBIN/GOPATH so DetectInstall
	// doesn't accidentally classify the temp mcpDir as go-install.
	t.Setenv("PATH", mcpDir)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/var/empty/non-existent-gopath")

	dst, reason, ok := resolveMCPTarget(binDir)
	if !ok {
		t.Fatalf("expected PATH-resolved ezs-mcp to be eligible, got reason=%q", reason)
	}
	if dst != mcpPath {
		t.Errorf("dst = %q, want %q", dst, mcpPath)
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

// TestResolveMCPTargetSkipsHomebrew verifies that a Homebrew-managed
// ezs-mcp on PATH is reported as skipped, not swapped — brew's
// receipt of the install would otherwise drift from the binary on
// disk, breaking subsequent `brew upgrade` runs.
func TestResolveMCPTargetSkipsHomebrew(t *testing.T) {
	binDir := t.TempDir()
	// Build a path that DetectInstall recognizes as Homebrew. The
	// classifier lower-cases the path before checking, so the actual
	// dir name capitalization doesn't matter for matching.
	brewBin := filepath.Join(t.TempDir(), "Cellar", "ezstack", "1.0.0", "bin")
	if err := os.MkdirAll(brewBin, 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(brewBin, "ezs-mcp")
	if err := os.WriteFile(mcpPath, []byte("anything"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", brewBin)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/var/empty/non-existent-gopath")

	dst, reason, ok := resolveMCPTarget(binDir)
	if ok {
		t.Errorf("expected Homebrew-managed ezs-mcp to be skipped, got dst=%q", dst)
	}
	if reason == "" {
		t.Error("expected non-empty skip reason for Homebrew-managed mcp")
	}
	if !strings.Contains(reason, "brew upgrade") {
		t.Errorf("skip reason should mention `brew upgrade`, got %q", reason)
	}
}

// TestResolveMCPTargetMissing verifies that when neither the sibling
// nor PATH yields an ezs-mcp, the resolver returns false with no
// reason — the caller treats that as "user has no MCP installed,
// silently leave well enough alone".
func TestResolveMCPTargetMissing(t *testing.T) {
	binDir := t.TempDir()
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/var/empty/non-existent-gopath")

	dst, reason, ok := resolveMCPTarget(binDir)
	if ok {
		t.Errorf("expected no candidate, got dst=%q", dst)
	}
	if reason != "" {
		t.Errorf("expected empty reason when nothing is found, got %q", reason)
	}
}

// TestRunUpgradesMCPInSeparateDir is the end-to-end test for the
// LookPath fallback. ezs lives in primary binDir; ezs-mcp lives in a
// completely separate dir on PATH. After Run(), both binaries must
// hold the new bytes and per-dir backups must be cleaned up.
func TestRunUpgradesMCPInSeparateDir(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s — no published asset", runtime.GOOS, runtime.GOARCH)
	}

	binDir, _ := fakeRelease(t, "v9.9.9", map[string]string{
		"ezs":     "new-ezs-v9.9.9\n",
		"ezs-mcp": "new-mcp-v9.9.9\n",
	})
	// fakeRelease seeded ezs-mcp next to ezs; remove it so siblingExists
	// returns false and the LookPath fallback kicks in.
	if err := os.Remove(filepath.Join(binDir, "ezs-mcp")); err != nil {
		t.Fatalf("remove sibling ezs-mcp: %v", err)
	}

	mcpDir := t.TempDir()
	mcpPath := filepath.Join(mcpDir, "ezs-mcp")
	if err := os.WriteFile(mcpPath, []byte("old-ezs-mcp-elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", mcpDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var logs []string
	res, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("Run: %v\nlogs:\n%s", err, strings.Join(logs, "\n"))
	}
	if len(res.Updated) != 2 {
		t.Fatalf("Updated = %v, want 2 entries", res.Updated)
	}

	gotEzs, _ := os.ReadFile(filepath.Join(binDir, "ezs"))
	if string(gotEzs) != "new-ezs-v9.9.9\n" {
		t.Errorf("ezs at %s = %q, want new bytes", binDir, gotEzs)
	}
	gotMCP, _ := os.ReadFile(mcpPath)
	if string(gotMCP) != "new-mcp-v9.9.9\n" {
		t.Errorf("ezs-mcp at %s = %q, want new bytes", mcpPath, gotMCP)
	}

	// No leftover backup in either directory after success.
	for _, p := range []string{
		filepath.Join(binDir, ".ezs.ezstack-upgrade-backup"),
		filepath.Join(mcpDir, ".ezs-mcp.ezstack-upgrade-backup"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("leftover backup %s after successful upgrade", p)
		}
	}
}

// TestRunSkipsHomebrewMCPOnPATH ensures the end-to-end pipeline routes
// a Homebrew-managed ezs-mcp on PATH to a "skipping" log instead of
// silently overwriting brew's binary. ezs (which lives in an
// InstallBinary path) still upgrades.
func TestRunSkipsHomebrewMCPOnPATH(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binDir, _ := fakeRelease(t, "v9.9.9", map[string]string{
		"ezs":     "new-ezs-v9.9.9\n",
		"ezs-mcp": "new-mcp-v9.9.9\n",
	})
	if err := os.Remove(filepath.Join(binDir, "ezs-mcp")); err != nil {
		t.Fatalf("remove sibling ezs-mcp: %v", err)
	}

	brewBin := filepath.Join(t.TempDir(), "Cellar", "ezstack", "1.0.0", "bin")
	if err := os.MkdirAll(brewBin, 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(brewBin, "ezs-mcp")
	if err := os.WriteFile(mcpPath, []byte("brew-old-mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", brewBin)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var logs []string
	res, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("Run: %v\nlogs:\n%s", err, strings.Join(logs, "\n"))
	}
	if len(res.Updated) != 1 {
		t.Errorf("Updated = %v, want 1 entry (ezs only)", res.Updated)
	}

	gotMCP, _ := os.ReadFile(mcpPath)
	if string(gotMCP) != "brew-old-mcp" {
		t.Errorf("Homebrew-managed ezs-mcp was overwritten: %q", gotMCP)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "Homebrew") || !strings.Contains(joined, "brew upgrade") {
		t.Errorf("expected log about skipping Homebrew-managed mcp, got:\n%s", joined)
	}
}

// TestRunLockOnSecondaryDirSurfacesConflict pre-acquires the lock in
// the SECONDARY (mcp) directory and verifies Run() refuses fast with
// the existing "already in progress" message. The primary directory
// is untouched even though its lock was momentarily acquired —
// rolling back the lock acquisition is essential to keep the user's
// install in a consistent state when a sibling upgrade is in flight.
func TestRunLockOnSecondaryDirSurfacesConflict(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binDir, _ := fakeRelease(t, "v9.9.9", map[string]string{
		"ezs":     "new-ezs\n",
		"ezs-mcp": "new-mcp\n",
	})
	if err := os.Remove(filepath.Join(binDir, "ezs-mcp")); err != nil {
		t.Fatalf("remove sibling ezs-mcp: %v", err)
	}

	mcpDir := t.TempDir()
	mcpPath := filepath.Join(mcpDir, "ezs-mcp")
	if err := os.WriteFile(mcpPath, []byte("old-mcp-elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", mcpDir)

	// Hold the secondary lock to simulate a sibling upgrade still
	// running in mcpDir while we attempt one for binDir.
	held, err := acquireUpgradeLock(filepath.Join(mcpDir, ".ezstack-upgrade.lock"))
	if err != nil {
		t.Fatalf("pre-acquire secondary lock: %v", err)
	}
	defer held.release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
	})
	if err == nil {
		t.Fatal("expected lock-conflict error")
	}
	if !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("expected 'already in progress' message, got %v", err)
	}
	if !strings.Contains(err.Error(), mcpDir) {
		t.Errorf("expected error to name secondary dir %s, got %v", mcpDir, err)
	}

	// Both binaries untouched.
	gotEzs, _ := os.ReadFile(filepath.Join(binDir, "ezs"))
	if string(gotEzs) != "old-ezs" {
		t.Errorf("ezs touched despite secondary-lock conflict: %q", gotEzs)
	}
	gotMCP, _ := os.ReadFile(mcpPath)
	if string(gotMCP) != "old-mcp-elsewhere" {
		t.Errorf("ezs-mcp touched despite secondary-lock conflict: %q", gotMCP)
	}
}

// TestRunRollbackAcrossDirs verifies all-or-nothing semantics still
// hold when ezs and ezs-mcp live in different directories: a failed
// ezs-mcp swap must restore ezs from its backup, even though the
// backup file lives in a different dir than the failing target.
func TestRunRollbackAcrossDirs(t *testing.T) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		t.Skipf("skip on %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binDir, _ := fakeRelease(t, "v9.9.9", map[string]string{
		"ezs":     "new-ezs\n",
		"ezs-mcp": "new-mcp\n",
	})
	if err := os.Remove(filepath.Join(binDir, "ezs-mcp")); err != nil {
		t.Fatalf("remove sibling ezs-mcp: %v", err)
	}

	mcpDir := t.TempDir()
	mcpPath := filepath.Join(mcpDir, "ezs-mcp")
	if err := os.WriteFile(mcpPath, []byte("old-mcp-elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", mcpDir)

	// Inject a synthetic failure on the ezs-mcp swap; ezs must roll back.
	oldReplace := atomicReplaceFn
	atomicReplaceFn = func(src, dst string) error {
		if filepath.Base(dst) == "ezs-mcp" {
			return errors.New("synthetic failure")
		}
		return oldReplace(src, dst)
	}
	t.Cleanup(func() { atomicReplaceFn = oldReplace })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
	})
	if err == nil {
		t.Fatal("expected error from injected failure")
	}
	if !strings.Contains(err.Error(), "synthetic failure") {
		t.Errorf("expected wrapped synthetic failure, got %v", err)
	}

	gotEzs, _ := os.ReadFile(filepath.Join(binDir, "ezs"))
	if string(gotEzs) != "old-ezs" {
		t.Errorf("ezs not rolled back: got %q, want old-ezs", gotEzs)
	}
	gotMCP, _ := os.ReadFile(mcpPath)
	if string(gotMCP) != "old-mcp-elsewhere" {
		t.Errorf("ezs-mcp unexpectedly modified: got %q", gotMCP)
	}

	// Backup files cleaned up in BOTH dirs.
	for _, p := range []string{
		filepath.Join(binDir, ".ezs.ezstack-upgrade-backup"),
		filepath.Join(mcpDir, ".ezs-mcp.ezstack-upgrade-backup"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("leftover backup %s after rollback", p)
		}
	}
}

// --- helpers ---

type tarEntry struct {
	name string
	body string
}

// writeTarGzOrdered writes a gzipped tarball preserving entry order,
// which the map-based writeTarGz can't guarantee. Useful for tests
// that need a specific tar layout (e.g. duplicate basenames).
func writeTarGzOrdered(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     e.name,
			Mode:     0o755,
			Size:     int64(len(e.body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha256OfFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// fakeReleaseAPI starts an httptest API server returning a release with
// the given tag (no asset bodies — go-install tests don't download a
// tarball) and points apiBaseURL at it for the test's lifetime. Cleanup
// restores the original apiBaseURL via t.Cleanup.
func fakeReleaseAPI(t *testing.T, tag string) {
	t.Helper()
	rel := Release{
		TagName: tag,
		// Asset URLs intentionally absent — the go-install path
		// resolves the tag and never touches asset bodies.
	}
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&rel)
	}))
	t.Cleanup(apiSrv.Close)
	oldBase := apiBaseURL
	apiBaseURL = apiSrv.URL
	t.Cleanup(func() { apiBaseURL = oldBase })
}

// fakeGoInstalledEzs lays down a fake ezs binary inside a synthetic
// $GOPATH/bin so DetectInstall classifies the running executable as
// InstallGoInstall, and points currentExecutableFn at it. Returns the
// bin directory so callers can drop an ezs-mcp sibling next to it for
// the IncludeMCP path.
func fakeGoInstalledEzs(t *testing.T) string {
	t.Helper()
	gopath := t.TempDir()
	binDir := filepath.Join(gopath, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeEzs := filepath.Join(binDir, "ezs")
	if err := os.WriteFile(fakeEzs, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", gopath)
	// Empty PATH so ezs-mcp PATH-fallback is purely opt-in per test.
	t.Setenv("PATH", binDir)

	old := currentExecutableFn
	currentExecutableFn = func() (string, error) { return fakeEzs, nil }
	t.Cleanup(func() { currentExecutableFn = old })

	return binDir
}

// captureGoInstall replaces goInstallFn with a recorder that appends
// each invoked package spec into the returned slice. Also no-ops the
// `go` pre-flight check so tests don't have to keep the system `go`
// dir on PATH while also constraining PATH for ezs-mcp lookups.
// Cleanup restores both hooks.
func captureGoInstall(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	oldInstall := goInstallFn
	goInstallFn = func(_ context.Context, pkg string, _ func(string, ...any)) error {
		calls = append(calls, pkg)
		return nil
	}
	oldExists := goBinExistsFn
	goBinExistsFn = func() error { return nil }
	t.Cleanup(func() {
		goInstallFn = oldInstall
		goBinExistsFn = oldExists
	})
	return &calls
}

func TestGoModulePath(t *testing.T) {
	cases := []struct {
		tag  string
		want string
	}{
		{"v4.7.5", "github.com/KulkarniKaustubh/ezstack/v4"},
		{"4.7.5", "github.com/KulkarniKaustubh/ezstack/v4"},
		{"v5.0.0", "github.com/KulkarniKaustubh/ezstack/v5"},
		{"v2.0.0-rc.1", "github.com/KulkarniKaustubh/ezstack/v2"},
		// Major 1 omits the suffix per semantic-import-versioning.
		{"v1.9.9", "github.com/KulkarniKaustubh/ezstack"},
		// Junk tags fall back to the v4 shipping path so a corrupted
		// release record doesn't silently re-install the wrong major.
		{"", "github.com/KulkarniKaustubh/ezstack/v4"},
		{"banana", "github.com/KulkarniKaustubh/ezstack/v4"},
	}
	for _, c := range cases {
		if got := goModulePath(c.tag); got != c.want {
			t.Errorf("goModulePath(%q) = %q, want %q", c.tag, got, c.want)
		}
	}
}

// TestRunGoInstallPathUpgrades exercises the happy path: a synthetic
// go-installed ezs is detected, the release tag is resolved, and
// `go install` is invoked for both ezs and ezs-mcp (sibling layout).
func TestRunGoInstallPathUpgrades(t *testing.T) {
	binDir := fakeGoInstalledEzs(t)
	// Drop an ezs-mcp sibling so IncludeMCP triggers a second go install.
	if err := os.WriteFile(filepath.Join(binDir, "ezs-mcp"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeReleaseAPI(t, "v9.9.9")
	calls := captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var logs []string
	res, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("Run: %v\nlogs:\n%s", err, strings.Join(logs, "\n"))
	}
	if res.Method != InstallGoInstall {
		t.Errorf("Method = %v, want InstallGoInstall", res.Method)
	}
	if res.From != "1.0.0" || res.To != "9.9.9" {
		t.Errorf("From/To = %q/%q, want 1.0.0/9.9.9", res.From, res.To)
	}
	wantCalls := []string{
		"github.com/KulkarniKaustubh/ezstack/v9/cmd/ezs@v9.9.9",
		"github.com/KulkarniKaustubh/ezstack/v9/cmd/ezs-mcp@v9.9.9",
	}
	if !reflect.DeepEqual(*calls, wantCalls) {
		t.Errorf("go install calls = %v, want %v", *calls, wantCalls)
	}
	if len(res.Updated) != 2 {
		t.Errorf("Updated = %v, want 2 entries", res.Updated)
	}
}

// TestRunGoInstallPathSkipsMissingMCP verifies that ezs-mcp is omitted
// when it neither sits next to ezs nor resolves on PATH — we don't
// silently plant a new binary on a machine that didn't have one.
func TestRunGoInstallPathSkipsMissingMCP(t *testing.T) {
	fakeGoInstalledEzs(t) // no sibling ezs-mcp written
	// Empty PATH dir so exec.LookPath("ezs-mcp") fails.
	t.Setenv("PATH", t.TempDir())
	fakeReleaseAPI(t, "v9.9.9")
	calls := captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var logs []string
	if _, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0], "/cmd/ezs@v9.9.9") {
		t.Errorf("expected only ezs to be reinstalled, got %v", *calls)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "no ezs-mcp found") {
		t.Errorf("expected skip log for missing ezs-mcp, got:\n%s", strings.Join(logs, "\n"))
	}
}

// TestRunGoInstallPathUsesPATHFallbackForMCP verifies that an ezs-mcp
// reachable through PATH (but not next to ezs) still triggers a second
// `go install`, matching the binary-path behavior.
func TestRunGoInstallPathUsesPATHFallbackForMCP(t *testing.T) {
	fakeGoInstalledEzs(t)
	mcpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mcpDir, "ezs-mcp"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", mcpDir)
	fakeReleaseAPI(t, "v9.9.9")
	calls := captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*calls) != 2 {
		t.Errorf("expected 2 go install calls (ezs + ezs-mcp), got %v", *calls)
	}
}

// TestRunGoInstallCheckOnly ensures --check on a go-installed binary
// short-circuits before any `go install` invocation and still reports
// the latest version.
func TestRunGoInstallCheckOnly(t *testing.T) {
	fakeGoInstalledEzs(t)
	fakeReleaseAPI(t, "v9.9.9")
	calls := captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var logs []string
	res, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		CheckOnly:      true,
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.AlreadyAtTip {
		t.Error("expected upgrade-available result (AlreadyAtTip = false)")
	}
	if len(*calls) != 0 {
		t.Errorf("CheckOnly must not invoke go install; got %v", *calls)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "go install") {
		t.Errorf("expected check-only log to mention `go install`, got:\n%s", strings.Join(logs, "\n"))
	}
}

// TestRunGoInstallAtTip verifies the at-tip short-circuit: same version
// without --force returns AlreadyAtTip and skips go install entirely.
func TestRunGoInstallAtTip(t *testing.T) {
	fakeGoInstalledEzs(t)
	fakeReleaseAPI(t, "v1.0.0")
	calls := captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := Run(ctx, Options{CurrentVersion: "1.0.0"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.AlreadyAtTip {
		t.Error("expected AlreadyAtTip when current matches latest")
	}
	if len(*calls) != 0 {
		t.Errorf("at-tip path must not run go install, got %v", *calls)
	}
}

// TestRunGoInstallForceReinstall verifies --force re-runs `go install`
// even when the running binary already matches the published tag.
func TestRunGoInstallForceReinstall(t *testing.T) {
	fakeGoInstalledEzs(t)
	fakeReleaseAPI(t, "v1.0.0")
	calls := captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		Force:          true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.AlreadyAtTip {
		t.Error("--force must not return AlreadyAtTip")
	}
	if len(*calls) != 1 {
		t.Errorf("expected 1 go install call under --force, got %v", *calls)
	}
}

// TestRunGoInstallConfirmDeclined verifies that returning false from
// the Confirm prompt aborts cleanly without invoking go install.
func TestRunGoInstallConfirmDeclined(t *testing.T) {
	fakeGoInstalledEzs(t)
	fakeReleaseAPI(t, "v9.9.9")
	calls := captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		Confirm:        func(string) bool { return false },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Cancelled {
		t.Error("expected Cancelled when Confirm returns false")
	}
	if len(*calls) != 0 {
		t.Errorf("declined upgrade must not run go install, got %v", *calls)
	}
}

// TestRunGoInstallPropagatesError ensures a `go install` failure is
// surfaced wrapped with the package spec so the user can tell which
// binary needs a manual retry, and that res.Updated reflects the
// partial progress (ezs done, ezs-mcp not).
func TestRunGoInstallPropagatesError(t *testing.T) {
	binDir := fakeGoInstalledEzs(t)
	if err := os.WriteFile(filepath.Join(binDir, "ezs-mcp"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeReleaseAPI(t, "v9.9.9")

	oldInstall := goInstallFn
	goInstallFn = func(_ context.Context, pkg string, _ func(string, ...any)) error {
		if strings.Contains(pkg, "/cmd/ezs-mcp@") {
			return errors.New("synthetic toolchain failure")
		}
		return nil
	}
	oldExists := goBinExistsFn
	goBinExistsFn = func() error { return nil }
	t.Cleanup(func() {
		goInstallFn = oldInstall
		goBinExistsFn = oldExists
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
	})
	if err == nil {
		t.Fatal("expected error from synthetic failure")
	}
	if !strings.Contains(err.Error(), "synthetic toolchain failure") {
		t.Errorf("error should wrap synthetic failure, got %v", err)
	}
	if !strings.Contains(err.Error(), "/cmd/ezs-mcp@") {
		t.Errorf("error should name the failing package, got %v", err)
	}
}

// TestPredictedGoInstallDir locks down the precedence rules `go install`
// itself uses for resolving the output directory: $GOBIN > first
// $GOPATH/bin > ~/go/bin. A bug here would make the drift-warning fire
// on the wrong dir or skip a real drift case.
func TestPredictedGoInstallDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	t.Run("GOBIN wins", func(t *testing.T) {
		t.Setenv("GOBIN", "/explicit/gobin")
		t.Setenv("GOPATH", "/the/gopath")
		if got := predictedGoInstallDir(); got != "/explicit/gobin" {
			t.Errorf("got %q, want /explicit/gobin", got)
		}
	})
	t.Run("first GOPATH/bin when GOBIN empty", func(t *testing.T) {
		t.Setenv("GOBIN", "")
		t.Setenv("GOPATH", "/first"+string(os.PathListSeparator)+"/second")
		want := filepath.Join("/first", "bin")
		if got := predictedGoInstallDir(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("home/go/bin when both empty", func(t *testing.T) {
		t.Setenv("GOBIN", "")
		t.Setenv("GOPATH", "")
		want := filepath.Join(tmpHome, "go", "bin")
		if got := predictedGoInstallDir(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("whitespace-only GOBIN/GOPATH falls through", func(t *testing.T) {
		t.Setenv("GOBIN", "   ")
		t.Setenv("GOPATH", "   ")
		want := filepath.Join(tmpHome, "go", "bin")
		if got := predictedGoInstallDir(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestRunGoInstallWarnsOnDirDrift covers the GOBIN-drift case: ezs sits
// in $GOPATH/bin (matching DetectInstall's classifier) but the user has
// since set $GOBIN to a different dir, so `go install` would write
// somewhere else and PATH would still resolve to the stale binary.
// Run() must log a `note:` warning before invoking go install.
func TestRunGoInstallWarnsOnDirDrift(t *testing.T) {
	binDir := fakeGoInstalledEzs(t)
	// Set GOBIN to a different dir post-classification so the predicted
	// install dir diverges from the running ezs's dir.
	driftDir := t.TempDir()
	t.Setenv("GOBIN", driftDir)
	fakeReleaseAPI(t, "v9.9.9")
	captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var logs []string
	if _, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "note: `go install` will write to") {
		t.Errorf("expected drift warning in log, got:\n%s", joined)
	}
	if !strings.Contains(joined, driftDir) {
		t.Errorf("expected drift warning to name driftDir %q, got:\n%s", driftDir, joined)
	}
	if !strings.Contains(joined, binDir) {
		t.Errorf("expected drift warning to name running ezs dir %q, got:\n%s", binDir, joined)
	}
}

// TestRunGoInstallNoWarnWhenAligned ensures the drift warning does NOT
// fire on the happy path where ezs lives in the predicted install dir
// (the common case: GOPATH=$HOME/go, ezs at ~/go/bin/ezs, no GOBIN).
func TestRunGoInstallNoWarnWhenAligned(t *testing.T) {
	fakeGoInstalledEzs(t) // sets GOPATH=tmp, ezs at tmp/bin/ezs, GOBIN=""
	fakeReleaseAPI(t, "v9.9.9")
	captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var logs []string
	if _, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(strings.Join(logs, "\n"), "note: `go install` will write to") {
		t.Errorf("did not expect drift warning when dirs align, got:\n%s", strings.Join(logs, "\n"))
	}
}

// TestRunGoInstallSkipsBrewMCPOnPATH locks the Homebrew-on-PATH skip:
// even when go-installed ezs is being upgraded, a brew-managed
// ezs-mcp on PATH must NOT get a duplicate go-install copy planted —
// brew owns that binary.
func TestRunGoInstallSkipsBrewMCPOnPATH(t *testing.T) {
	fakeGoInstalledEzs(t)
	// Drop a brew-shaped ezs-mcp on PATH (no sibling next to ezs).
	brewBin := filepath.Join(t.TempDir(), "Cellar", "ezstack", "1.0.0", "bin")
	if err := os.MkdirAll(brewBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brewBin, "ezs-mcp"), []byte("brew-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", brewBin)
	fakeReleaseAPI(t, "v9.9.9")
	calls := captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var logs []string
	if _, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
		Logf:           func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0], "/cmd/ezs@v9.9.9") {
		t.Errorf("expected ezs-only go install (brew mcp must be skipped), got %v", *calls)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "Homebrew") || !strings.Contains(joined, "brew upgrade") {
		t.Errorf("expected brew-skip hint in logs, got:\n%s", joined)
	}
}

// TestRunGoInstallMissingGoFailsBeforeConfirm locks the pre-flight
// check ordering: when `go` is missing, Run() must fail BEFORE invoking
// Confirm so the user doesn't agree to an upgrade that immediately
// errors out.
func TestRunGoInstallMissingGoFailsBeforeConfirm(t *testing.T) {
	fakeGoInstalledEzs(t)
	fakeReleaseAPI(t, "v9.9.9")

	// Inject a missing-go pre-flight; do NOT use captureGoInstall (it
	// stubs the pre-flight back to success).
	oldExists := goBinExistsFn
	goBinExistsFn = func() error { return errors.New("exec: \"go\": executable file not found in $PATH") }
	t.Cleanup(func() { goBinExistsFn = oldExists })

	confirmCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		Confirm: func(string) bool {
			confirmCalled = true
			return true
		},
	})
	if err == nil {
		t.Fatal("expected pre-flight error, got nil")
	}
	if confirmCalled {
		t.Error("Confirm must not run before the pre-flight check")
	}
	if !strings.Contains(err.Error(), "go.dev/dl") {
		t.Errorf("error should hint at https://go.dev/dl/, got %v", err)
	}
}

// TestRunGoInstallUpdatedIncludesPath verifies that res.Updated holds
// real filesystem paths under the predicted go install dir, not bare
// names. The CLI surfaces these in a "replaced N binaries" message and
// Result consumers can use them for follow-up tasks.
func TestRunGoInstallUpdatedIncludesPath(t *testing.T) {
	binDir := fakeGoInstalledEzs(t)
	if err := os.WriteFile(filepath.Join(binDir, "ezs-mcp"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeReleaseAPI(t, "v9.9.9")
	captureGoInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := Run(ctx, Options{
		CurrentVersion: "1.0.0",
		IncludeMCP:     true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		filepath.Join(binDir, "ezs"),
		filepath.Join(binDir, "ezs-mcp"),
	}
	if !reflect.DeepEqual(res.Updated, want) {
		t.Errorf("res.Updated = %v, want %v", res.Updated, want)
	}
}

// TestRunGoInstallSubprocessRealRun is a smoke test against the real
// `go` toolchain: it replaces goBinExistsFn with the default and
// targets a known-good module to ensure the runGoInstall plumbing
// (env passthrough, output capture, error wrapping) doesn't drift.
// Skipped when the runner doesn't have network access or `go` itself.
func TestRunGoInstallSubprocessRealRun(t *testing.T) {
	if testing.Short() {
		t.Skip("real network/toolchain smoke test; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("`go` not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Use a tiny, stable module ("rsc.io/quote/v3") would hit the network;
	// instead invoke go with --help on the install command, which keeps
	// the smoke test offline-safe and still exercises the exec.Command
	// wiring path: env passthrough, output capture, and clean exit.
	var logs []string
	logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

	// runGoInstall returns nil on success; we use a synthetic invalid
	// pkg path to force a non-zero exit and verify the error message
	// is propagated with the captured stderr.
	err := runGoInstall(ctx, "this/module/path/does/not/exist@v0.0.0", logf)
	if err == nil {
		t.Fatal("expected runGoInstall to fail on a bogus module path")
	}
	// The error wrapping must include some toolchain-emitted text so
	// users can debug their own install failures from the message.
	if !strings.Contains(err.Error(), "this/module/path/does/not/exist") &&
		!strings.Contains(err.Error(), "exit status") {
		t.Errorf("expected wrapped go-install error, got: %v", err)
	}
}

// TestRunHomebrewStillRoutesToManagedError keeps the regression bar in
// place: switching the go-install path to in-place upgrade must NOT
// also auto-run anything for Homebrew installs — those still need to
// surface ManagedInstallError so the CLI prints a brew-specific hint.
func TestRunHomebrewStillRoutesToManagedError(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/var/empty/non-existent-gopath")

	brewBin := filepath.Join(t.TempDir(), "Cellar", "ezstack", "1.0.0", "bin")
	if err := os.MkdirAll(brewBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeEzs := filepath.Join(brewBin, "ezs")
	if err := os.WriteFile(fakeEzs, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := currentExecutableFn
	currentExecutableFn = func() (string, error) { return fakeEzs, nil }
	t.Cleanup(func() { currentExecutableFn = old })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Run(ctx, Options{CurrentVersion: "1.0.0"})
	if err == nil {
		t.Fatal("expected ManagedInstallError for Homebrew install")
	}
	var managed *ManagedInstallError
	if !errors.As(err, &managed) {
		t.Fatalf("expected *ManagedInstallError, got %T: %v", err, err)
	}
	if managed.Method != InstallHomebrew {
		t.Errorf("Method = %v, want InstallHomebrew", managed.Method)
	}
}
