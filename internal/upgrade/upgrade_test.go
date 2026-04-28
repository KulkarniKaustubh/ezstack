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
	"path/filepath"
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
