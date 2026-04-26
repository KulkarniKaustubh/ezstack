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

// httpClient is shared so connection pooling kicks in for the back-to-back
// API + asset requests. No client-level Timeout — the per-request context
// (set in Run with a 5-minute budget) is the single source of truth for
// cancellation, since a slow tarball over a flaky connection legitimately
// takes longer than any single request reasonably should.
var httpClient = &http.Client{}

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
	Body    string  `json:"body"`
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

// LatestRelease fetches the latest published (non-prerelease) release.
func LatestRelease(ctx context.Context) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	return fetchRelease(ctx, url)
}

// ReleaseByTag fetches a specific tag. The tag must include the leading "v".
func ReleaseByTag(ctx context.Context, tag string) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", repoOwner, repoName, tag)
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
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("release not found at %s", url)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github api %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("release has no tag_name")
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
// Returns -1 if a<b, 0 if equal, +1 if a>b. Non-numeric segments compare
// lexicographically; invalid input never panics, it just sorts deterministically.
func CompareVersions(a, b string) int {
	pa := splitSemver(a)
	pb := splitSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitSemver(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Drop any pre-release/build metadata so "4.6.0-rc.1" still sorts.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.SplitN(v, ".", 4)
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
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

	execPath, err := currentExecutable()
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
	if cmp >= 0 && !opts.Force {
		res.AlreadyAtTip = true
		logf("ezstack is already at the latest version (%s)", res.From)
		return res, nil
	}

	if opts.CheckOnly {
		logf("upgrade available: %s → %s", res.From, res.To)
		return res, nil
	}

	if cmp > 0 {
		logf("downgrading from %s to %s", res.From, res.To)
	} else {
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

	tmp, err := os.MkdirTemp("", "ezstack-upgrade-*")
	if err != nil {
		return nil, err
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

	binDir := filepath.Dir(execPath)
	wantBins := []string{"ezs"}
	if opts.IncludeMCP && siblingExists(binDir, "ezs-mcp") {
		wantBins = append(wantBins, "ezs-mcp")
	}

	staged, err := extractBinaries(tarPath, tmp, wantBins)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	for _, name := range wantBins {
		src, ok := staged[name]
		if !ok {
			return nil, fmt.Errorf("tarball is missing %s", name)
		}
		dst := filepath.Join(binDir, name)
		if err := atomicReplace(src, dst); err != nil {
			return nil, fmt.Errorf("replace %s: %w", dst, err)
		}
		res.Updated = append(res.Updated, dst)
		logf("replaced %s", dst)
	}
	return res, nil
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
func download(ctx context.Context, url, dst string, maxBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ezstack-upgrade")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %s", resp.Status)
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
		return err
	}
	if maxBytes > 0 && n > maxBytes {
		os.Remove(dst)
		return fmt.Errorf("response exceeds maximum size of %s", humanSize(maxBytes))
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
		dstPath := filepath.Join(dstDir, base)
		dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return nil, err
		}
		// Cap the per-file copy to defend against a tar bomb. 200 MiB is
		// well above any single ezstack binary today.
		if _, err := io.CopyN(dst, tr, 200<<20); err != nil && !errors.Is(err, io.EOF) {
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
	tmp, err := os.CreateTemp(dir, ".ezs-upgrade-*")
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
