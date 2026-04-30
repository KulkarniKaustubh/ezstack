package commands

import (
	"errors"
	"fmt"
	"testing"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/github"
)

// fakePRFetcher implements prFetcher for unit tests. Each map entry takes
// precedence over its sibling error map so a single fake can express the
// "GetPR fails but GetPRByBranch succeeds" path that the real refresh
// helper needs to handle.
type fakePRFetcher struct {
	byNumber    map[int]*github.PR
	byBranch    map[string]*github.PR
	errsByNum   map[int]error
	errsByBranc map[string]error
}

func (f *fakePRFetcher) GetPR(n int) (*github.PR, error) {
	if pr, ok := f.byNumber[n]; ok {
		return pr, nil
	}
	if err, ok := f.errsByNum[n]; ok {
		return nil, err
	}
	return nil, nil
}

func (f *fakePRFetcher) GetPRByBranch(branch string) (*github.PR, error) {
	if pr, ok := f.byBranch[branch]; ok {
		return pr, nil
	}
	if err, ok := f.errsByBranc[branch]; ok {
		return nil, err
	}
	return nil, nil
}

func setupCacheTest(t *testing.T) (cacheDir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("EZSTACK_HOME", home)
	return "/fake/repo" // any consistent string works as the repo key
}

// reloadBranchCache fetches the persisted BranchCache for branchName from
// disk so tests can assert what actually got written.
func reloadBranchCache(t *testing.T, cacheDir, branchName string) *config.BranchCache {
	t.Helper()
	cc, err := config.LoadCacheConfig(cacheDir)
	if err != nil {
		t.Fatalf("LoadCacheConfig: %v", err)
	}
	return cc.GetBranchCache(branchName)
}

func TestPRStateFromGitHub_StateMapping(t *testing.T) {
	tests := []struct {
		name string
		pr   *github.PR
		want string
	}{
		{"nil PR", nil, ""},
		{"merged trumps state", &github.PR{State: "OPEN", Merged: true}, "MERGED"},
		{"closed when not merged", &github.PR{State: "CLOSED"}, "CLOSED"},
		{"draft when open", &github.PR{State: "OPEN", IsDraft: true}, "DRAFT"},
		{"plain open", &github.PR{State: "OPEN"}, "OPEN"},
		// The github.PR.State field is sometimes empty when callers haven't
		// populated it (e.g. CreatePR mocks). The fallback should still be
		// "OPEN" rather than empty, matching what fetchBranchStatuses does.
		{"empty state defaults to open", &github.PR{}, "OPEN"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := prStateFromGitHub(tc.pr); got != tc.want {
				t.Errorf("prStateFromGitHub(%+v) = %q, want %q", tc.pr, got, tc.want)
			}
		})
	}
}

func TestDisplayPRState(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "state unknown"},
		{"OPEN", "OPEN"},
		{"MERGED", "MERGED"},
		{"DRAFT", "DRAFT"},
	}
	for _, tc := range tests {
		if got := displayPRState(tc.in); got != tc.want {
			t.Errorf("displayPRState(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSavePRToCache_PersistsAllFields(t *testing.T) {
	cacheDir := setupCacheTest(t)
	pr := &github.PR{
		Number:   42,
		URL:      "https://github.com/o/r/pull/42",
		State:    "OPEN",
		MergedAt: "",
		Merged:   false,
		IsDraft:  false,
	}
	savePRToCache(cacheDir, "feature", pr)

	bc := reloadBranchCache(t, cacheDir, "feature")
	if bc == nil {
		t.Fatal("BranchCache for 'feature' not persisted")
	}
	if bc.PRUrl != pr.URL {
		t.Errorf("PRUrl = %q, want %q", bc.PRUrl, pr.URL)
	}
	if bc.PRState != "OPEN" {
		t.Errorf("PRState = %q, want OPEN", bc.PRState)
	}
	if bc.IsMerged {
		t.Errorf("IsMerged = true, want false")
	}
}

func TestSavePRToCache_DraftStateRecorded(t *testing.T) {
	cacheDir := setupCacheTest(t)
	pr := &github.PR{Number: 7, URL: "u", State: "OPEN", IsDraft: true}
	savePRToCache(cacheDir, "wip", pr)
	bc := reloadBranchCache(t, cacheDir, "wip")
	if bc == nil || bc.PRState != "DRAFT" {
		t.Fatalf("PRState = %v, want DRAFT", bc)
	}
}

func TestSavePRToCache_PreservesNonPRFields(t *testing.T) {
	// Regression for the pre-fix bug where savePRToCache only wrote pr_url
	// and silently kept stale pr_state/is_merged. Now the helper takes the
	// full PR struct and overwrites all PR-association fields together.
	// What it MUST NOT touch: worktree_path, is_remote, remote.
	cacheDir := setupCacheTest(t)

	cc, err := config.LoadCacheConfig(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	cc.SetBranchCache("feature", &config.BranchCache{
		WorktreePath: "/wt/feature",
		IsRemote:     true,
		Remote:       "fork",
		PRUrl:        "stale-url",
		PRState:      "MERGED",
		IsMerged:     true,
	})
	if err := cc.Save(cacheDir); err != nil {
		t.Fatal(err)
	}

	pr := &github.PR{Number: 99, URL: "https://github.com/o/r/pull/99", State: "OPEN"}
	savePRToCache(cacheDir, "feature", pr)

	bc := reloadBranchCache(t, cacheDir, "feature")
	if bc == nil {
		t.Fatal("cache lost")
	}
	if bc.PRUrl != pr.URL {
		t.Errorf("PRUrl = %q, want %q", bc.PRUrl, pr.URL)
	}
	if bc.PRState != "OPEN" {
		t.Errorf("PRState = %q, want OPEN (must move with pr_url)", bc.PRState)
	}
	if bc.IsMerged {
		t.Errorf("IsMerged should have been overwritten to false")
	}
	if bc.WorktreePath != "/wt/feature" {
		t.Errorf("WorktreePath clobbered: got %q", bc.WorktreePath)
	}
	if !bc.IsRemote {
		t.Errorf("IsRemote clobbered: got %v", bc.IsRemote)
	}
	if bc.Remote != "fork" {
		t.Errorf("Remote clobbered: got %q", bc.Remote)
	}
}

func TestSavePRToCache_NilPRIsNoop(t *testing.T) {
	cacheDir := setupCacheTest(t)
	// Should not panic, should not create a cache entry.
	savePRToCache(cacheDir, "feature", nil)
	if bc := reloadBranchCache(t, cacheDir, "feature"); bc != nil {
		t.Errorf("nil-pr save still wrote BranchCache: %+v", bc)
	}
}

func TestClearPRFromCache_ClearsPRFieldsPreservesOthers(t *testing.T) {
	cacheDir := setupCacheTest(t)
	cc, err := config.LoadCacheConfig(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	cc.SetBranchCache("feature", &config.BranchCache{
		WorktreePath: "/wt/feature",
		IsRemote:     true,
		Remote:       "fork",
		PRUrl:        "https://github.com/o/r/pull/42",
		PRState:      "OPEN",
		IsMerged:     false,
	})
	if err := cc.Save(cacheDir); err != nil {
		t.Fatal(err)
	}

	if err := clearPRFromCache(cacheDir, "feature"); err != nil {
		t.Fatalf("clearPRFromCache: %v", err)
	}

	bc := reloadBranchCache(t, cacheDir, "feature")
	if bc == nil {
		t.Fatal("entry vanished — should have kept worktree/remote")
	}
	if bc.PRUrl != "" || bc.PRState != "" || bc.IsMerged {
		t.Errorf("PR fields not cleared: %+v", bc)
	}
	if bc.WorktreePath != "/wt/feature" || !bc.IsRemote || bc.Remote != "fork" {
		t.Errorf("non-PR fields touched: %+v", bc)
	}
}

func TestClearPRFromCache_NoOpForMissingBranch(t *testing.T) {
	cacheDir := setupCacheTest(t)
	if err := clearPRFromCache(cacheDir, "ghost"); err != nil {
		t.Errorf("expected no-op, got error: %v", err)
	}
	if bc := reloadBranchCache(t, cacheDir, "ghost"); bc != nil {
		t.Errorf("clear created a phantom entry: %+v", bc)
	}
}

func TestRefreshPRStateFromGitHub_UpdatesStaleMergedCache(t *testing.T) {
	cacheDir := setupCacheTest(t)
	branch := &config.Branch{
		Name:     "feature",
		PRNumber: 42,
		PRUrl:    "https://github.com/o/r/pull/42",
		PRState:  "OPEN",
		IsMerged: false,
	}
	gh := &fakePRFetcher{
		byNumber: map[int]*github.PR{
			42: {Number: 42, URL: "https://github.com/o/r/pull/42", State: "MERGED", MergedAt: "2026-01-01", Merged: true},
		},
	}
	pr, err := refreshPRStateFromGitHub(gh, cacheDir, branch)
	if err != nil {
		t.Fatalf("refresh err: %v", err)
	}
	if pr == nil {
		t.Fatal("expected pr")
	}
	if branch.PRState != "MERGED" || !branch.IsMerged {
		t.Errorf("in-memory branch not updated: %+v", branch)
	}
	bc := reloadBranchCache(t, cacheDir, "feature")
	if bc == nil || bc.PRState != "MERGED" || !bc.IsMerged {
		t.Errorf("cache not updated: %+v", bc)
	}
}

func TestRefreshPRStateFromGitHub_NoLongerExistsClearsCache(t *testing.T) {
	cacheDir := setupCacheTest(t)
	// Pre-seed the cache so we can verify it gets cleared.
	cc, err := config.LoadCacheConfig(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	cc.SetBranchCache("feature", &config.BranchCache{
		WorktreePath: "/wt/feature",
		PRUrl:        "https://github.com/o/r/pull/42",
		PRState:      "OPEN",
	})
	if err := cc.Save(cacheDir); err != nil {
		t.Fatal(err)
	}

	branch := &config.Branch{Name: "feature", PRNumber: 42, PRState: "OPEN"}
	// fake returns (nil, nil) for both lookups — the "PR genuinely deleted" case.
	gh := &fakePRFetcher{}

	pr, err := refreshPRStateFromGitHub(gh, cacheDir, branch)
	if err != nil {
		t.Fatalf("refresh err: %v", err)
	}
	if pr != nil {
		t.Errorf("expected nil pr, got %+v", pr)
	}
	if branch.PRNumber != 0 || branch.PRUrl != "" || branch.PRState != "" || branch.IsMerged {
		t.Errorf("in-memory branch not cleared: %+v", branch)
	}
	bc := reloadBranchCache(t, cacheDir, "feature")
	if bc == nil {
		t.Fatal("BranchCache vanished — should still exist with non-PR fields")
	}
	if bc.PRUrl != "" || bc.PRState != "" || bc.IsMerged {
		t.Errorf("PR fields not cleared in cache: %+v", bc)
	}
	if bc.WorktreePath != "/wt/feature" {
		t.Errorf("WorktreePath should have been preserved: got %q", bc.WorktreePath)
	}
}

func TestRefreshPRStateFromGitHub_TransientErrorDoesNotMutate(t *testing.T) {
	cacheDir := setupCacheTest(t)
	cc, err := config.LoadCacheConfig(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	cc.SetBranchCache("feature", &config.BranchCache{
		PRUrl:    "https://github.com/o/r/pull/42",
		PRState:  "OPEN",
		IsMerged: false,
	})
	if err := cc.Save(cacheDir); err != nil {
		t.Fatal(err)
	}

	branch := &config.Branch{Name: "feature", PRNumber: 42, PRState: "OPEN"}
	netErr := errors.New("network unreachable")
	gh := &fakePRFetcher{
		errsByNum:   map[int]error{42: netErr},
		errsByBranc: map[string]error{"feature": netErr},
	}

	pr, err := refreshPRStateFromGitHub(gh, cacheDir, branch)
	if err == nil {
		t.Fatal("expected error to surface")
	}
	if pr != nil {
		t.Errorf("expected nil pr on error, got %+v", pr)
	}
	if branch.PRState != "OPEN" {
		t.Errorf("in-memory branch mutated despite error: %+v", branch)
	}
	bc := reloadBranchCache(t, cacheDir, "feature")
	if bc == nil || bc.PRState != "OPEN" {
		t.Errorf("cache mutated despite error: %+v", bc)
	}
}

func TestRefreshPRStateFromGitHub_GetPRFallsBackToGetPRByBranch(t *testing.T) {
	// When the cached PR number is stale (PR deleted via API) but the
	// branch still has a current PR, GetPR errors on the old number and we
	// must fall back to a head-branch lookup. Otherwise users with a
	// stale-number cache lose the ability to ever auto-recover.
	cacheDir := setupCacheTest(t)
	branch := &config.Branch{Name: "feature", PRNumber: 11, PRState: "OPEN"}
	gh := &fakePRFetcher{
		errsByNum: map[int]error{11: errors.New("not found")},
		byBranch: map[string]*github.PR{
			"feature": {Number: 22, URL: "https://github.com/o/r/pull/22", State: "OPEN"},
		},
	}
	pr, err := refreshPRStateFromGitHub(gh, cacheDir, branch)
	if err != nil {
		t.Fatalf("refresh err: %v", err)
	}
	if pr == nil || pr.Number != 22 {
		t.Fatalf("expected PR #22 from fallback, got %+v", pr)
	}
	if branch.PRNumber != 22 || branch.PRState != "OPEN" {
		t.Errorf("branch not updated to live PR: %+v", branch)
	}
}

func TestRefreshPRStateFromGitHub_BranchLookupErrPRNotFoundClearsCache(t *testing.T) {
	// The real github.Client surfaces "no PR for this branch" as an
	// ErrPRNotFound-wrapped error rather than (nil, nil). The refresh
	// helper must recognize that sentinel and treat it as "PR is gone"
	// rather than as a transient failure — otherwise users see misleading
	// "refresh failed" messages instead of the cache being cleared.
	cacheDir := setupCacheTest(t)
	cc, err := config.LoadCacheConfig(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	cc.SetBranchCache("feature", &config.BranchCache{
		WorktreePath: "/wt/feature",
		PRUrl:        "https://github.com/o/r/pull/42",
		PRState:      "OPEN",
	})
	if err := cc.Save(cacheDir); err != nil {
		t.Fatal(err)
	}

	branch := &config.Branch{Name: "feature", PRNumber: 42, PRState: "OPEN"}
	gh := &fakePRFetcher{
		errsByNum: map[int]error{42: errors.New("not found")},
		errsByBranc: map[string]error{
			"feature": fmt.Errorf("%w for branch %q", github.ErrPRNotFound, "feature"),
		},
	}

	pr, refreshErr := refreshPRStateFromGitHub(gh, cacheDir, branch)
	if refreshErr != nil {
		t.Fatalf("expected ErrPRNotFound to be swallowed, got: %v", refreshErr)
	}
	if pr != nil {
		t.Errorf("expected nil pr, got %+v", pr)
	}
	if branch.PRNumber != 0 || branch.PRState != "" {
		t.Errorf("branch not cleared: %+v", branch)
	}
	bc := reloadBranchCache(t, cacheDir, "feature")
	if bc == nil || bc.PRUrl != "" || bc.PRState != "" || bc.WorktreePath != "/wt/feature" {
		t.Errorf("cache not reconciled correctly: %+v", bc)
	}
}

func TestRefreshPRStateFromGitHub_NoCachedNumberErrPRNotFoundIsClean(t *testing.T) {
	// Same as above but without a stale cached number — the no-PR signal
	// from GetPRByBranch (the only call made) must still resolve to (nil,
	// nil) rather than bubble up as a refresh failure.
	cacheDir := setupCacheTest(t)
	branch := &config.Branch{Name: "fresh"}
	gh := &fakePRFetcher{
		errsByBranc: map[string]error{
			"fresh": fmt.Errorf("%w for branch %q", github.ErrPRNotFound, "fresh"),
		},
	}
	pr, err := refreshPRStateFromGitHub(gh, cacheDir, branch)
	if err != nil {
		t.Fatalf("expected nil err, got: %v", err)
	}
	if pr != nil {
		t.Errorf("expected nil pr, got %+v", pr)
	}
}

func TestFetchLivePR_PreservesTransientErrorsAcrossFallback(t *testing.T) {
	// When the fallback lookup also fails with a transient error (not
	// ErrPRNotFound), the original GetPR error must surface — clearing
	// the cache on a flaky network would silently destroy good data.
	branch := &config.Branch{Name: "feature", PRNumber: 42, PRState: "OPEN"}
	getPRErr := errors.New("network unreachable")
	gh := &fakePRFetcher{
		errsByNum:   map[int]error{42: getPRErr},
		errsByBranc: map[string]error{"feature": errors.New("also down")},
	}
	pr, err := fetchLivePR(gh, branch)
	if err == nil {
		t.Fatal("expected error to surface")
	}
	if pr != nil {
		t.Errorf("expected nil pr on error, got %+v", pr)
	}
}

func TestRefreshPRStateFromGitHub_NoCachedNumberUsesBranchLookup(t *testing.T) {
	cacheDir := setupCacheTest(t)
	branch := &config.Branch{Name: "fresh"} // no PRNumber
	gh := &fakePRFetcher{
		byBranch: map[string]*github.PR{
			"fresh": {Number: 5, URL: "https://github.com/o/r/pull/5", State: "OPEN"},
		},
		// errsByNum populated to ensure GetPR isn't called when number is 0
		errsByNum: map[int]error{0: errors.New("must not be called")},
	}
	pr, err := refreshPRStateFromGitHub(gh, cacheDir, branch)
	if err != nil {
		t.Fatalf("refresh err: %v", err)
	}
	if pr == nil || pr.Number != 5 {
		t.Fatalf("expected PR #5, got %+v", pr)
	}
}

// fakePRFetcher_RecordedCalls verifies our fake's contract — protects future
// test reuse from assumptions that don't match the real client.
func TestFakePRFetcher_ContractMatch(t *testing.T) {
	gh := &fakePRFetcher{
		byNumber:    map[int]*github.PR{1: {Number: 1}},
		byBranch:    map[string]*github.PR{"x": {Number: 9}},
		errsByNum:   map[int]error{2: errors.New("nope")},
		errsByBranc: map[string]error{"y": errors.New("nope")},
	}
	if pr, err := gh.GetPR(1); pr == nil || err != nil {
		t.Errorf("GetPR(1) = (%+v, %v), want (PR, nil)", pr, err)
	}
	if pr, err := gh.GetPR(2); pr != nil || err == nil {
		t.Errorf("GetPR(2) = (%+v, %v), want (nil, err)", pr, err)
	}
	if pr, err := gh.GetPR(3); pr != nil || err != nil {
		t.Errorf("GetPR(3) = (%+v, %v), want (nil, nil)", pr, err)
	}
	if pr, err := gh.GetPRByBranch("x"); pr == nil || err != nil {
		t.Errorf("GetPRByBranch(x) = (%+v, %v)", pr, err)
	}
	// Compile-time check that *github.Client satisfies the same interface.
	var _ prFetcher = (*fakePRFetcher)(nil)
	var _ prFetcher = (*github.Client)(nil)
	_ = fmt.Sprintf
}
