// Package upgrade implements self-update for the ezs and ezs-mcp binaries.
//
// It downloads the matching release tarball from GitHub, verifies the
// SHA-256 against checksums.txt, and atomically swaps the running
// binary (and its sibling) on disk. Homebrew and `go install` users
// are detected and routed to their respective package manager instead
// of an in-place swap.
package upgrade

import (
	"archive/tar"
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
	"path/filepath"
	"runtime"
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

	if method != InstallBinary {
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
	wantBins := []string{"ezs"}
	if opts.IncludeMCP && siblingExists(binDir, "ezs-mcp") {
		wantBins = append(wantBins, "ezs-mcp")
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

	staged, err := extractBinaries(tarPath, tmp, wantBins)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	// Pre-flight: every wanted binary must be present in the tarball.
	// Validate before touching the destination directory so we don't end
	// up rolling back a partial swap because of a missing-entry error.
	for _, name := range wantBins {
		if _, ok := staged[name]; !ok {
			return nil, fmt.Errorf("tarball is missing %s", name)
		}
	}

	// Multi-binary swaps need to be all-or-nothing: replacing `ezs` and
	// then failing to replace `ezs-mcp` would leave the user with a
	// version-skewed pair (the MCP protocol surface is sensitive to that).
	// Strategy: hard-link each existing target to a backup name in the same
	// directory, do all renames, restore from backups on any failure.
	// Hard links are guaranteed same-fs since binDir == backup dir.
	type swap struct{ src, dst, backup string }
	plans := make([]swap, 0, len(wantBins))
	for _, name := range wantBins {
		dst := filepath.Join(binDir, name)
		plans = append(plans, swap{
			src:    staged[name],
			dst:    dst,
			backup: filepath.Join(binDir, "."+name+".ezstack-upgrade-backup"),
		})
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
// catches this and prints a tailored hint.
type ManagedInstallError struct {
	Method   InstallMethod
	ExecPath string
}

func (e *ManagedInstallError) Error() string {
	switch e.Method {
	case InstallHomebrew:
		return fmt.Sprintf("ezstack was installed via Homebrew (%s); run `brew upgrade ezstack` instead", e.ExecPath)
	case InstallGoInstall:
		return fmt.Sprintf("ezstack was installed via `go install` (%s); run `go install github.com/%s/%s/v4/cmd/ezs@latest` (and `cmd/ezs-mcp@latest`) instead", e.ExecPath, repoOwner, repoName)
	default:
		return "ezstack is managed by an external installer; in-place upgrade is disabled"
	}
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
