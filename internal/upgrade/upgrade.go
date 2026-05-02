// Package upgrade implements self-update for the ezs and ezs-mcp binaries.
//
// It downloads the matching release tarball from GitHub, verifies the
// SHA-256 against checksums.txt, and atomically swaps the running
// binary (and its sibling) on disk. `go install` users are upgraded by
// re-running `go install` against the latest release tag so the
// toolchain remains the source of truth for their install. Homebrew
// users are routed back to `brew upgrade ezstack` so brew's receipt of
// the install stays in sync with the binary on disk.
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
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Repo is the GitHub source of release artifacts.
const (
	repoOwner = "KulkarniKaustubh"
	repoName  = "ezstack"
)

// apiBaseURL is the GitHub API root used for release lookups. It is a
// var (not a const) so tests can point it at a local httptest.Server.
// Production code must not reassign it.
var apiBaseURL = "https://api.github.com"

// httpClient is shared so connection pooling kicks in for the back-to-back
// API + asset requests. No client-level Timeout — the per-request context
// (set in Run with a 5-minute budget) is the single source of truth for
// cancellation, since a slow tarball over a flaky connection legitimately
// takes longer than any single request reasonably should.
var httpClient = &http.Client{}

// currentExecutableFn locates the running binary. It exists as an
// override hook so tests can drive Run() against a fake binary path
// without trying to clobber the test runner's own binary.
var currentExecutableFn = currentExecutable

// atomicReplaceFn does the new-binary-onto-old-binary swap. Override
// hook so a rollback test can inject a controlled failure on a specific
// destination.
var atomicReplaceFn = atomicReplace

// goInstallFn invokes `go install <pkg>` for the go-install upgrade
// path. Test hook so the dispatch can be exercised without spawning a
// real `go` process or hitting the proxy.
var goInstallFn = runGoInstall

// goBinExistsFn reports whether the `go` toolchain is on PATH. Test
// hook so the pre-flight check (which fires BEFORE Confirm so the
// user doesn't agree to an upgrade that immediately errors out)
// doesn't depend on the runtime PATH containing the system `go`.
var goBinExistsFn = func() error {
	_, err := exec.LookPath("go")
	return err
}

// maxArchiveBytes caps the raw tarball download to defend against a
// malicious or misconfigured release returning an unbounded body. 500 MiB
// is far above any realistic ezstack archive (~5 MiB today).
const maxArchiveBytes = 500 << 20

// InstallMethod is how the running ezs binary was installed. Only Binary
// is upgraded in place; the others print a routing message instead so the
// upgrade stays consistent with how the user originally installed.
type InstallMethod int

const (
	InstallBinary InstallMethod = iota
	InstallHomebrew
	InstallGoInstall
)

func (m InstallMethod) String() string {
	switch m {
	case InstallHomebrew:
		return "homebrew"
	case InstallGoInstall:
		return "go install"
	default:
		return "binary"
	}
}

// Release is the trimmed GitHub release payload we care about.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset is one downloadable artifact attached to a release.
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Size        int64  `json:"size"`
}

// Options controls a single upgrade run.
type Options struct {
	// CurrentVersion is what the running binary reports (no leading "v").
	CurrentVersion string
	// TargetTag pins to a specific release tag like "v4.6.0". Empty
	// means "latest published release".
	TargetTag string
	// Force replaces the binary even when CurrentVersion already matches.
	Force bool
	// CheckOnly prints the comparison and exits without downloading.
	CheckOnly bool
	// IncludeMCP also swaps a sibling ezs-mcp binary if one exists in
	// the same directory.
	IncludeMCP bool
	// Confirm is called between the version check and the download.
	// Returning false aborts cleanly. nil auto-confirms.
	Confirm func(prompt string) bool
	// Logf receives progress messages, one per call, no trailing newline.
	// nil discards.
	Logf func(format string, args ...any)
}

// Result describes what an upgrade run actually did.
type Result struct {
	From         string
	To           string
	Method       InstallMethod
	Updated      []string // absolute paths of binaries that were replaced
	AlreadyAtTip bool     // true when no upgrade was performed
	Cancelled    bool     // user said no at the confirm prompt
}

// ErrNoMatchingAsset is returned when no release asset matches the
// runtime os/arch. Surfaced as its own type so the CLI layer can print a
// platform-specific hint.
var ErrNoMatchingAsset = errors.New("no release asset matches this platform")

// NetworkError wraps any HTTP-level failure (GitHub API call or asset
// download) so the CLI layer can route it to a network-specific exit
// code. Use errors.As to extract.
type NetworkError struct {
	// Op is a short label describing the failing operation, e.g.
	// "github api" or "download <url>".
	Op  string
	Err error
}

func (e *NetworkError) Error() string { return fmt.Sprintf("%s: %v", e.Op, e.Err) }
func (e *NetworkError) Unwrap() error { return e.Err }

// LatestRelease fetches the latest published (non-prerelease) release.
func LatestRelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBaseURL, repoOwner, repoName)
	return fetchRelease(ctx, url)
}

// ReleaseByTag fetches a specific tag. The tag must include the leading "v".
func ReleaseByTag(ctx context.Context, tag string) (*Release, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", apiBaseURL, repoOwner, repoName, tag)
	return fetchRelease(ctx, url)
}

func fetchRelease(ctx context.Context, url string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ezstack-upgrade")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, &NetworkError{Op: "github api", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, &NetworkError{Op: "github api", Err: fmt.Errorf("release not found at %s", url)}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &NetworkError{Op: "github api", Err: fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))}
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, &NetworkError{Op: "github api", Err: fmt.Errorf("decode release: %w", err)}
	}
	if rel.TagName == "" {
		return nil, &NetworkError{Op: "github api", Err: fmt.Errorf("release has no tag_name")}
	}
	return &rel, nil
}

// AssetName returns the goreleaser archive name for the current runtime.
// It only knows about platforms we actually publish (linux/darwin x amd64/arm64).
func AssetName() (string, error) {
	if !supportedPlatform(runtime.GOOS, runtime.GOARCH) {
		return "", fmt.Errorf("unsupported platform %s/%s — only linux/darwin amd64/arm64 are published", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("ezstack_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH), nil
}

func supportedPlatform(goos, goarch string) bool {
	if goos != "linux" && goos != "darwin" {
		return false
	}
	return goarch == "amd64" || goarch == "arm64"
}

// CompareVersions compares two semver-ish strings (with optional leading v).
// Returns -1 if a<b, 0 if equal, +1 if a>b. Pre-release suffixes follow
// semver: a version with a pre-release is less than the same version
// without one (so 4.6.3-rc.1 < 4.6.3), and pre-release identifiers are
// compared lexicographically as a fallback. Invalid input never panics,
// it just sorts deterministically.
func CompareVersions(a, b string) int {
	coreA, preA := splitSemver(a)
	coreB, preB := splitSemver(b)
	for i := 0; i < 3; i++ {
		if coreA[i] != coreB[i] {
			if coreA[i] < coreB[i] {
				return -1
			}
			return 1
		}
	}
	// Cores match. Per semver §11: a version without a pre-release
	// outranks the same version with one.
	switch {
	case preA == "" && preB == "":
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	case preA < preB:
		return -1
	case preA > preB:
		return 1
	default:
		return 0
	}
}

func splitSemver(v string) ([3]int, string) {
	var core [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Per semver §10, build metadata (everything after `+`) is NOT
	// ordering-relevant — strip it before extracting the pre-release
	// suffix so that `1.2.3+build.5` compares equal to `1.2.3`.
	if i := strings.Index(v, "+"); i >= 0 {
		v = v[:i]
	}
	var pre string
	if i := strings.Index(v, "-"); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 4)
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		core[i] = n
	}
	return core, pre
}

// DetectInstall classifies how the binary at execPath was installed.
// Symlinks must be resolved by the caller before passing the path here.
func DetectInstall(execPath string) InstallMethod {
	p := filepath.ToSlash(execPath)
	low := strings.ToLower(p)

	// Homebrew installs land in a Cellar directory under either
	// /opt/homebrew (Apple Silicon) or /usr/local (Intel + Linuxbrew).
	if strings.Contains(low, "/cellar/ezstack/") || strings.Contains(low, "/homebrew/cellar/") {
		return InstallHomebrew
	}

	// `go install` writes to $GOBIN, then $GOPATH/bin, then ~/go/bin.
	gobin := os.Getenv("GOBIN")
	if gobin != "" && strings.HasPrefix(p, filepath.ToSlash(gobin)+"/") {
		return InstallGoInstall
	}
	gopath := os.Getenv("GOPATH")
	if gopath != "" {
		for _, gp := range strings.Split(gopath, string(os.PathListSeparator)) {
			if gp == "" {
				continue
			}
			if strings.HasPrefix(p, filepath.ToSlash(gp)+"/bin/") {
				return InstallGoInstall
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if strings.HasPrefix(p, filepath.ToSlash(home)+"/go/bin/") {
			return InstallGoInstall
		}
	}

	return InstallBinary
}

// Run performs an upgrade end-to-end. It is safe to call from either
// `ezs upgrade` or `ezs-mcp --upgrade`; the install-method routing is
// the same.
func Run(ctx context.Context, opts Options) (*Result, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	execPath, err := currentExecutableFn()
	if err != nil {
		return nil, fmt.Errorf("locate current binary: %w", err)
	}
	method := DetectInstall(execPath)

	res := &Result{Method: method, From: opts.CurrentVersion}

	switch method {
	case InstallGoInstall:
		return runGoInstallUpgrade(ctx, opts, execPath, res, logf)
	case InstallHomebrew:
		return res, &ManagedInstallError{Method: method, ExecPath: execPath}
	}

	rel, err := resolveRelease(ctx, opts.TargetTag)
	if err != nil {
		return nil, err
	}
	res.To = strings.TrimPrefix(rel.TagName, "v")

	cmp := CompareVersions(opts.CurrentVersion, rel.TagName)
	pinned := opts.TargetTag != ""
	// CheckOnly takes precedence over Force so that `--force --check` at the
	// tip reports "already at latest" instead of a phantom "upgrade available".
	if opts.CheckOnly {
		switch {
		case cmp >= 0 && pinned:
			res.AlreadyAtTip = true
			logf("ezstack %s is at or above pinned tag %s", res.From, rel.TagName)
		case cmp >= 0:
			res.AlreadyAtTip = true
			logf("ezstack is already at the latest version (%s)", res.From)
		default:
			logf("upgrade available: %s → %s", res.From, res.To)
		}
		return res, nil
	}
	if cmp >= 0 && !opts.Force {
		res.AlreadyAtTip = true
		if pinned {
			logf("ezstack %s is at or above pinned tag %s — use --force to reinstall", res.From, rel.TagName)
		} else {
			logf("ezstack is already at the latest version (%s)", res.From)
		}
		return res, nil
	}

	switch {
	case cmp > 0:
		logf("downgrading from %s to %s", res.From, res.To)
	case cmp == 0:
		// Force-reinstall at the same version — don't claim "upgrading".
		logf("reinstalling %s", res.From)
	default:
		logf("upgrading from %s to %s", res.From, res.To)
	}

	if opts.Confirm != nil {
		ok := opts.Confirm(fmt.Sprintf("Replace binaries at %s?", filepath.Dir(execPath)))
		if !ok {
			res.Cancelled = true
			return res, nil
		}
	}

	assetName, err := AssetName()
	if err != nil {
		return nil, err
	}
	asset := findAsset(rel, assetName)
	if asset == nil {
		return nil, fmt.Errorf("%w: looking for %q in %s", ErrNoMatchingAsset, assetName, rel.TagName)
	}
	checksumsAsset := findAsset(rel, "checksums.txt")
	if checksumsAsset == nil {
		return nil, fmt.Errorf("release %s is missing checksums.txt", rel.TagName)
	}

	binDir := filepath.Dir(execPath)

	// Build the swap plan up front. ezs always swaps next to itself.
	// ezs-mcp is more flexible: if it sits in binDir we swap it there,
	// otherwise we resolve it via PATH so an installation that landed
	// in a separate directory (e.g. ~/go/bin/ezs-mcp planted by
	// `ezs agent`'s `go install` while ezs lives in ~/.local/bin/) is
	// still upgraded in lock-step. Homebrew-managed MCP siblings on PATH
	// are skipped — those must be updated through `brew upgrade`.
	type swap struct {
		name   string
		src    string // staged tmp path; populated after extract
		dst    string // live target path
		backup string // adjacent rollback backup; same dir as dst
	}
	makeBackup := func(dst string) string {
		return filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".ezstack-upgrade-backup")
	}
	plans := []swap{{name: "ezs", dst: filepath.Join(binDir, "ezs")}}
	plans[0].backup = makeBackup(plans[0].dst)
	if opts.IncludeMCP {
		mcpDst, skipReason, ok := resolveMCPTarget(binDir)
		switch {
		case ok:
			plans = append(plans, swap{
				name:   "ezs-mcp",
				dst:    mcpDst,
				backup: makeBackup(mcpDst),
			})
		case skipReason != "":
			logf("%s", skipReason)
		}
	}

	// Acquire non-blocking exclusive locks on every distinct destination
	// directory BEFORE staging anything. Two concurrent `ezs upgrade`
	// processes would otherwise race on the fixed-name backup files
	// (.ezs.ezstack-upgrade-backup): the loser's stale-backup cleanup
	// in Phase 1 would clobber the winner's live hard-link backup,
	// breaking rollback. Sorted so two runs that land on the same set of
	// dirs can't AB/BA-deadlock.
	seenLockDir := map[string]bool{}
	var lockDirs []string
	for _, p := range plans {
		d := filepath.Dir(p.dst)
		if seenLockDir[d] {
			continue
		}
		seenLockDir[d] = true
		lockDirs = append(lockDirs, d)
	}
	sort.Strings(lockDirs)
	var locks []*upgradeLock
	defer func() {
		for _, l := range locks {
			l.release()
		}
	}()
	for _, d := range lockDirs {
		l, err := acquireUpgradeLock(filepath.Join(d, ".ezstack-upgrade.lock"))
		if err != nil {
			if errors.Is(err, errLockHeld) {
				return nil, fmt.Errorf("another ezstack upgrade is already in progress in %s — wait for it to finish or remove .ezstack-upgrade.lock if it is stale", d)
			}
			return nil, fmt.Errorf("acquire upgrade lock in %s: %w", d, err)
		}
		locks = append(locks, l)
	}

	// Stage inside binDir so the final replace is a same-filesystem
	// rename (truly atomic on Unix), and so a missing-write-permission
	// error surfaces *before* we burn bandwidth on the download.
	tmp, err := os.MkdirTemp(binDir, ".ezstack-upgrade-*")
	if err != nil {
		return nil, fmt.Errorf("cannot stage upgrade in %s (write access required): %w", binDir, err)
	}
	defer os.RemoveAll(tmp)

	logf("downloading %s (%s)", assetName, humanSize(asset.Size))
	tarPath := filepath.Join(tmp, assetName)
	sumsPath := filepath.Join(tmp, "checksums.txt")

	// Tarball is the bottleneck; checksums.txt is < 1 KB. Fetch them in
	// parallel so the round-trip on the sums file overlaps with the body
	// of the archive download instead of stacking serially.
	var (
		wg             sync.WaitGroup
		tarErr, sumErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		tarErr = download(ctx, asset.DownloadURL, tarPath, maxArchiveBytes)
	}()
	go func() {
		defer wg.Done()
		sumErr = download(ctx, checksumsAsset.DownloadURL, sumsPath, 1<<20)
	}()
	wg.Wait()
	if tarErr != nil {
		return nil, fmt.Errorf("download %s: %w", assetName, tarErr)
	}
	if sumErr != nil {
		return nil, fmt.Errorf("download checksums.txt: %w", sumErr)
	}
	if err := verifyChecksum(tarPath, sumsPath, assetName); err != nil {
		return nil, fmt.Errorf("checksum mismatch for %s: %w", assetName, err)
	}
	logf("verified sha-256 against checksums.txt")

	wantBins := make([]string, 0, len(plans))
	for _, p := range plans {
		wantBins = append(wantBins, p.name)
	}
	staged, err := extractBinaries(tarPath, tmp, wantBins)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	// Pre-flight: every wanted binary must be present in the tarball.
	// Validate before touching the destination directory so we don't end
	// up rolling back a partial swap because of a missing-entry error.
	// Multi-binary swaps need to be all-or-nothing: replacing `ezs` and
	// then failing to replace `ezs-mcp` would leave the user with a
	// version-skewed pair (the MCP protocol surface is sensitive to that).
	// Strategy: hard-link each existing target to a backup file in the
	// same directory as the live binary, do all renames, restore from
	// backups on any failure.
	for i := range plans {
		src, ok := staged[plans[i].name]
		if !ok {
			return nil, fmt.Errorf("tarball is missing %s", plans[i].name)
		}
		plans[i].src = src
	}

	// Phase 1: snapshot existing targets via hard link (or copy fallback
	// for filesystems without hard-link support). Track what we created so
	// we can clean up on failure.
	type backup struct {
		path    string
		existed bool
	}
	var backups []backup
	cleanupBackups := func() {
		for _, b := range backups {
			os.Remove(b.path)
		}
	}
	for _, p := range plans {
		if _, err := os.Stat(p.dst); err != nil {
			// Target doesn't exist (e.g. a fresh ezs-mcp install) —
			// nothing to back up. A failed swap of a non-existent
			// target rolls back by simply unlinking the new file.
			backups = append(backups, backup{path: p.backup, existed: false})
			continue
		}
		// Remove any stale backup left behind by a previous interrupted
		// upgrade so the link/copy below has a clean target.
		os.Remove(p.backup)
		if err := snapshotForRollback(p.dst, p.backup); err != nil {
			cleanupBackups()
			return nil, fmt.Errorf("snapshot %s: %w", p.dst, err)
		}
		backups = append(backups, backup{path: p.backup, existed: true})
	}

	// Phase 2: rename new binaries into place. On any failure, restore
	// from backups and report the original error.
	var swapped []swap
	for i, p := range plans {
		if err := atomicReplaceFn(p.src, p.dst); err != nil {
			// Roll back: restore each completed swap from its backup
			// (or unlink the new file if there was no original).
			for j, done := range swapped {
				if backups[j].existed {
					if rerr := os.Rename(done.backup, done.dst); rerr != nil {
						logf("WARNING: rollback failed for %s: %v", done.dst, rerr)
					}
				} else {
					os.Remove(done.dst)
				}
			}
			// Also clean up the as-yet-unused backup for the failing
			// swap (and any later plans that never ran).
			for k := i; k < len(backups); k++ {
				os.Remove(backups[k].path)
			}
			res.Updated = nil
			return nil, fmt.Errorf("replace %s: %w", p.dst, err)
		}
		swapped = append(swapped, p)
		res.Updated = append(res.Updated, p.dst)
		logf("replaced %s", p.dst)
	}

	// Phase 3: success — drop backups.
	cleanupBackups()
	return res, nil
}

// snapshotForRollback creates a same-inode reference to src at backup so
// that, after src is overwritten via os.Rename, the previous content is
// still recoverable by renaming backup back over src. Falls back to a
// byte copy when the filesystem rejects hard links.
func snapshotForRollback(src, backup string) error {
	if err := os.Link(src, backup); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(backup, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(backup)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(backup)
		return err
	}
	return nil
}

// ManagedInstallError signals that the binary is owned by a package
// manager and the user must upgrade through that channel. The CLI
// catches this and prints a tailored hint. Only Homebrew installs end
// up here today — `go install` installs are upgraded in-process by
// re-running `go install`.
type ManagedInstallError struct {
	Method   InstallMethod
	ExecPath string
}

func (e *ManagedInstallError) Error() string {
	switch e.Method {
	case InstallHomebrew:
		return fmt.Sprintf("ezstack was installed via Homebrew (%s); run `brew upgrade ezstack` instead", e.ExecPath)
	default:
		return "ezstack is managed by an external installer; in-place upgrade is disabled"
	}
}

// runGoInstallUpgrade re-installs ezs (and optionally ezs-mcp) by
// invoking `go install` for the resolved release tag. It mirrors the
// flag semantics of the binary path (--check, --force, --version,
// --no-mcp, Confirm, Logf), but delegates the actual swap to the Go
// toolchain so the user's install stays consistent with how it was
// originally laid down.
//
// ezs-mcp is only re-installed when it already exists somewhere the
// classifier accepts (sibling to ezs OR resolvable via PATH and not
// Homebrew-managed). We don't want to silently plant a new binary on
// a machine that didn't have one, and we don't want to add a duplicate
// go-install copy when brew already owns the binary on PATH.
func runGoInstallUpgrade(ctx context.Context, opts Options, execPath string, res *Result, logf func(format string, args ...any)) (*Result, error) {
	rel, err := resolveRelease(ctx, opts.TargetTag)
	if err != nil {
		return nil, err
	}
	res.To = strings.TrimPrefix(rel.TagName, "v")

	cmp := CompareVersions(opts.CurrentVersion, rel.TagName)
	pinned := opts.TargetTag != ""
	if opts.CheckOnly {
		switch {
		case cmp >= 0 && pinned:
			res.AlreadyAtTip = true
			logf("ezstack %s is at or above pinned tag %s", res.From, rel.TagName)
		case cmp >= 0:
			res.AlreadyAtTip = true
			logf("ezstack is already at the latest version (%s)", res.From)
		default:
			logf("upgrade available via `go install`: %s → %s", res.From, res.To)
		}
		return res, nil
	}
	if cmp >= 0 && !opts.Force {
		res.AlreadyAtTip = true
		if pinned {
			logf("ezstack %s is at or above pinned tag %s — use --force to reinstall", res.From, rel.TagName)
		} else {
			logf("ezstack is already at the latest version (%s)", res.From)
		}
		return res, nil
	}

	// Pre-flight: fail fast if `go` isn't on PATH so the user gets a
	// clean install-Go-or-use-the-tarball message instead of a confusing
	// post-confirm error after they've agreed to proceed.
	if err := goBinExistsFn(); err != nil {
		return nil, fmt.Errorf("ezs was installed via `go install` (%s) but `go` is not on PATH; install Go from https://go.dev/dl/ or download the binary tarball from the GitHub release", execPath)
	}

	// Warn if `go install` would land the new binary in a directory
	// other than where the running ezs lives. This happens when
	// GOBIN/GOPATH have drifted since the original install (e.g. ezs
	// is at $GOPATH/bin from a year ago, but $GOBIN now points
	// elsewhere). The new binary would sit on disk while $PATH still
	// resolves to the stale one — the exact failure the user is
	// upgrading to fix. Don't error: the user may have set up
	// shadowing intentionally, and they can resolve it after the run.
	ezsDir := filepath.Dir(execPath)
	predictedDir := predictedGoInstallDir()
	if predictedDir != "" && predictedDir != ezsDir {
		logf("note: `go install` will write to %s but the running ezs lives in %s — make sure %s appears first on $PATH or restart the shell after the upgrade",
			predictedDir, ezsDir, predictedDir)
	}

	switch {
	case cmp > 0:
		logf("downgrading from %s to %s via `go install`", res.From, res.To)
	case cmp == 0:
		logf("reinstalling %s via `go install`", res.From)
	default:
		logf("upgrading from %s to %s via `go install`", res.From, res.To)
	}

	if opts.Confirm != nil {
		ok := opts.Confirm(fmt.Sprintf("Re-run `go install` to upgrade ezs at %s?", ezsDir))
		if !ok {
			res.Cancelled = true
			return res, nil
		}
	}

	pkgs := []string{"ezs"}
	if opts.IncludeMCP {
		// Reuse the binary-path classifier so a brew-managed ezs-mcp
		// on PATH gets the same skip-with-hint treatment, and so a
		// genuinely missing ezs-mcp doesn't trigger a brand-new install.
		_, skipReason, mcpOK := resolveMCPTarget(ezsDir)
		switch {
		case mcpOK:
			pkgs = append(pkgs, "ezs-mcp")
		case skipReason != "":
			logf("%s", skipReason)
		default:
			logf("no ezs-mcp found alongside ezs or on PATH — skipping (run `go install %s/cmd/ezs-mcp@%s` if you want it)",
				goModulePath(rel.TagName), rel.TagName)
		}
	}

	modulePath := goModulePath(rel.TagName)
	for _, name := range pkgs {
		target := fmt.Sprintf("%s/cmd/%s@%s", modulePath, name, rel.TagName)
		logf("running: go install %s", target)
		if err := goInstallFn(ctx, target, logf); err != nil {
			// Surface partial progress so the user can tell which
			// binary needs a manual retry. ezs upgraded but ezs-mcp
			// failed leaves the pair version-skewed; the log line
			// above and the error returned together make that clear.
			return nil, fmt.Errorf("go install %s: %w", target, err)
		}
		landingDir := predictedDir
		if landingDir == "" {
			landingDir = ezsDir
		}
		res.Updated = append(res.Updated, filepath.Join(landingDir, name))
	}
	return res, nil
}

// predictedGoInstallDir mirrors what `go install` would pick for its
// output directory: $GOBIN, then the first $GOPATH/bin entry, then
// ~/go/bin. Returns "" when the user has neither GOPATH set nor a
// resolvable home directory — `go install` itself will then surface
// the underlying error and the caller falls back to logging without
// the dir.
func predictedGoInstallDir() string {
	if g := strings.TrimSpace(os.Getenv("GOBIN")); g != "" {
		return g
	}
	if gp := os.Getenv("GOPATH"); gp != "" {
		// `go install pkg@version` uses the FIRST entry in a
		// colon-separated GOPATH for its output bin dir.
		first := strings.SplitN(gp, string(os.PathListSeparator), 2)[0]
		if first = strings.TrimSpace(first); first != "" {
			return filepath.Join(first, "bin")
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "go", "bin")
	}
	return ""
}

// goModulePath computes the import path that `go install` should
// receive for the given release tag. Major versions ≥ 2 use the
// /vN suffix that semantic-import-versioning requires; v0/v1 omit it.
// Falls back to the v4 path that ships today when the tag is malformed
// so a corrupted release record doesn't silently re-install the wrong
// major version.
func goModulePath(tag string) string {
	core, _ := splitSemver(tag)
	major := core[0]
	if major <= 0 {
		major = 4
	}
	if major == 1 {
		return fmt.Sprintf("github.com/%s/%s", repoOwner, repoName)
	}
	return fmt.Sprintf("github.com/%s/%s/v%d", repoOwner, repoName, major)
}

// runGoInstall executes `go install <pkg>` and pipes its stdout/stderr
// through logf line-by-line so the user sees module-download progress
// in the same stream as the surrounding upgrade messages. Returns the
// exit error wrapped with the captured output to make an arcane go
// toolchain failure (e.g. "module requires Go 1.26") legible.
func runGoInstall(ctx context.Context, pkg string, logf func(format string, args ...any)) error {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("`go` not on PATH — install Go from https://go.dev/dl/ or download the binary tarball from GitHub Releases")
	}
	cmd := exec.CommandContext(ctx, goBin, "install", pkg)
	cmd.Env = os.Environ()
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	msg := strings.TrimSpace(out.String())
	if runErr != nil {
		if msg != "" {
			return fmt.Errorf("%w\n%s", runErr, msg)
		}
		return runErr
	}
	if msg != "" {
		for _, line := range strings.Split(msg, "\n") {
			if line == "" {
				continue
			}
			logf("go: %s", line)
		}
	}
	return nil
}

// resolveRelease picks between LatestRelease and ReleaseByTag based on
// whether the user pinned a target.
func resolveRelease(ctx context.Context, tag string) (*Release, error) {
	if tag == "" {
		return LatestRelease(ctx)
	}
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return ReleaseByTag(ctx, tag)
}

// currentExecutable returns the absolute path of the running binary with
// symlinks resolved, so a binary invoked through e.g. /usr/local/bin/ezs
// → /opt/homebrew/Cellar/... is correctly classified.
func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// Fall back to the raw path — losing symlink resolution only
		// downgrades install-method detection, not correctness.
		return exe, nil
	}
	return resolved, nil
}

func findAsset(rel *Release, name string) *Asset {
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			return &rel.Assets[i]
		}
	}
	return nil
}

// download streams url into dst, replacing whatever is already there. The
// body is capped at maxBytes; pass 0 for "no application-level cap" (the
// caller's context still applies). Returning an error rolls back dst.
//
// Errors are classified for the CLI exit-code mapping:
//   - HTTP transport errors, non-200 responses, mid-stream copy errors,
//     and over-cap responses are wrapped in *NetworkError (exit 8).
//   - Local filesystem errors (Create, Sync) bubble through unwrapped
//     so they map to general I/O exit 1.
//
// io.Copy errors are biased toward NetworkError because the source is
// the response body — a disk-full failure during the copy is rare
// compared to a connection drop.
func download(ctx context.Context, url, dst string, maxBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ezstack-upgrade")
	resp, err := httpClient.Do(req)
	if err != nil {
		return &NetworkError{Op: "download " + url, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &NetworkError{Op: "download " + url, Err: fmt.Errorf("http %s", resp.Status)}
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	var src io.Reader = resp.Body
	if maxBytes > 0 {
		// +1 so a body that exactly hits the cap still copies cleanly,
		// while one byte over surfaces as "exceeds maximum size".
		src = io.LimitReader(resp.Body, maxBytes+1)
	}
	n, err := io.Copy(f, src)
	if err != nil {
		os.Remove(dst)
		return &NetworkError{Op: "download " + url, Err: err}
	}
	if maxBytes > 0 && n > maxBytes {
		os.Remove(dst)
		return &NetworkError{Op: "download " + url, Err: fmt.Errorf("response exceeds maximum size of %s", humanSize(maxBytes))}
	}
	return f.Sync()
}

// verifyChecksum hashes archivePath and matches it against the entry for
// archiveName inside sumsPath (goreleaser's "<sha256>  <name>" format).
func verifyChecksum(archivePath, sumsPath, archiveName string) error {
	want, err := readExpectedSum(sumsPath, archiveName)
	if err != nil {
		return err
	}
	got, err := sha256File(archivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("expected %s, got %s", want, got)
	}
	return nil
}

func readExpectedSum(sumsPath, archiveName string) (string, error) {
	data, err := os.ReadFile(sumsPath)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[len(fields)-1] == archiveName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no entry for %s in checksums.txt", archiveName)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinaries pulls each name in want out of the gzipped tar at
// archivePath, writes them to dstDir, and returns name → file path.
// Files are written 0755. Anything not in want is skipped.
func extractBinaries(archivePath, dstDir string, want []string) (map[string]string, error) {
	wantSet := make(map[string]bool, len(want))
	for _, n := range want {
		wantSet[n] = true
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	out := make(map[string]string, len(want))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if !wantSet[base] {
			continue
		}
		if _, dup := out[base]; dup {
			return nil, fmt.Errorf("tarball contains duplicate entry %q", base)
		}
		dstPath := filepath.Join(dstDir, base)
		dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return nil, err
		}
		// Cap the per-file copy to defend against a tar bomb. 200 MiB is
		// well above any single ezstack binary today. Read max+1 so we
		// can distinguish "file fits" from "source still has more bytes
		// after the cap" — io.CopyN by itself returns nil on a clean
		// truncation, which would let an oversized payload through.
		const maxBinaryBytes = 200 << 20
		n, err := io.CopyN(dst, tr, maxBinaryBytes+1)
		if err != nil && !errors.Is(err, io.EOF) {
			dst.Close()
			return nil, err
		}
		if n > maxBinaryBytes {
			dst.Close()
			return nil, fmt.Errorf("entry %q exceeds %s cap", base, humanSize(maxBinaryBytes))
		}
		// Sync before close so a power loss between the eventual rename
		// and the kernel flushing data blocks can't leave a zero-length
		// binary in place. Matches what `download` does for the tarball.
		if err := dst.Sync(); err != nil {
			dst.Close()
			return nil, err
		}
		if err := dst.Close(); err != nil {
			return nil, err
		}
		out[base] = dstPath
	}
	return out, nil
}

// atomicReplace moves src on top of dst. On Unix this works even when
// dst is the currently-running binary: the old inode is unlinked but the
// kernel keeps backing the running process until it exits. To survive a
// cross-filesystem rename (e.g. /tmp on tmpfs vs /usr/local/bin on the
// root fs), we fall back to a copy + rename inside the destination dir.
func atomicReplace(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Cross-device or permission rename failure: copy into the
	// destination directory under a hidden temp name, then rename.
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".ezstack-upgrade-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { os.Remove(tmpPath) }

	in, err := os.Open(src)
	if err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	defer in.Close()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		cleanup()
		return err
	}
	return nil
}

func siblingExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// resolveMCPTarget locates the ezs-mcp binary that should be swapped
// alongside the running ezs. It prefers a sibling in binDir (the
// happy path for Homebrew, manual tarball installs, and any other
// "drop both binaries in the same directory" install layout). If no
// sibling exists it falls back to PATH so a `go install`-only
// ezs-mcp at ~/go/bin/ — common when `ezs agent` had to bootstrap it
// on a machine where ezs itself was installed differently — is still
// upgraded in lock-step.
//
// A Homebrew-managed ezs-mcp on PATH is intentionally skipped: the
// user is expected to run `brew upgrade ezstack` to keep brew's
// receipt of the install in sync. InstallGoInstall paths are NOT
// skipped here — the user opted into in-place self-update by running
// `ezs upgrade` against an unmanaged primary, and the cost of leaving
// MCP version-skewed (silent JSON-RPC mismatches with Claude Code)
// outweighs the cost of overwriting a binary that `go install` will
// happily rewrite on the next run anyway.
//
// Returns:
//
//	(path, "", true)   — found and eligible for swap
//	("",   reason, false) — found but skipped; reason is human-readable
//	("",   "",     false) — no candidate found anywhere
func resolveMCPTarget(binDir string) (dst, skipReason string, ok bool) {
	if siblingExists(binDir, "ezs-mcp") {
		return filepath.Join(binDir, "ezs-mcp"), "", true
	}
	p, err := exec.LookPath("ezs-mcp")
	if err != nil {
		return "", "", false
	}
	// Resolve symlinks ONLY for the install-method classification — a
	// brew-managed binary often lives behind /usr/local/bin/ezs-mcp →
	// /opt/homebrew/Cellar/.../ezs-mcp, and we need the resolved path
	// to recognize the Cellar marker. The swap target itself stays as
	// the LookPath result so we don't follow a user's intentional alias
	// and rewrite the canonical file underneath them — same posture as
	// the sibling branch above, which never resolves symlinks either.
	classifyPath := p
	if r, err := filepath.EvalSymlinks(p); err == nil {
		classifyPath = r
	}
	if DetectInstall(classifyPath) == InstallHomebrew {
		return "", fmt.Sprintf("skipping Homebrew-managed ezs-mcp at %s — run `brew upgrade ezstack` to update it", classifyPath), false
	}
	return p, "", true
}

func humanSize(n int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
	)
	switch {
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
