package ghstack

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// fakeGit is an in-memory Git for projection tests.
type fakeGit struct {
	heads      map[string]string // branch -> sha ("" / missing = no branch)
	ancestors  map[string]bool   // "commit>branch" -> commit is in branch's history
	mergeBases map[string]string // "parent>branch" -> sha
}

func (f fakeGit) Head(b string) string                  { return f.heads[b] }
func (f fakeGit) IsAncestor(commit, branch string) bool { return f.ancestors[commit+">"+branch] }
func (f fakeGit) MergeBase(parent, branch string) string {
	return f.mergeBases[parent+">"+branch]
}

// tree builds a config.Stack from a tree, flagging merged branches (merged
// branches get a PR unless listed in noPR).
func tree(hash, root string, t config.BranchTree, merged []string, noPR ...string) *config.Stack {
	isMerged := map[string]bool{}
	for _, m := range merged {
		isMerged[m] = true
	}
	skipPR := map[string]bool{}
	for _, n := range noPR {
		skipPR[n] = true
	}
	s := &config.Stack{Hash: hash, Root: root, Tree: t}
	var walk func(parent string, t config.BranchTree)
	walk = func(parent string, t config.BranchTree) {
		for _, k := range sortedKeys(t) {
			b := &config.Branch{Name: k, Parent: parent, IsMerged: isMerged[k]}
			if isMerged[k] && !skipPR[k] {
				b.PRNumber = 100 + len(s.Branches)
			}
			s.Branches = append(s.Branches, b)
			walk(k, t[k])
		}
	}
	walk(root, t)
	return s
}

func chainSummary(chains []Chain) []string {
	var out []string
	for _, c := range chains {
		var parts []string
		for i, b := range c.Branches {
			p := b
			if c.Merged[b] {
				p += "*"
			}
			if i < c.Shared {
				p = "(" + p + ")"
			}
			parts = append(parts, p)
		}
		out = append(out, c.StackHash+":"+c.Trunk+"|"+strings.Join(parts, ","))
	}
	return out
}

func TestChains(t *testing.T) {
	tests := []struct {
		name   string
		stacks []*config.Stack
		old    *Catalog
		want   []string
	}{
		{
			name:   "linear",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"a": {"b": {"c": {}}}}, nil)},
			want:   []string{"s1:main|a,b,c"},
		},
		{
			name:   "two top-level branches are separate chains",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"x": {}, "y": {"z": {}}}, nil)},
			want:   []string{"s1:main|x", "s1:main|y,z"},
		},
		{
			name:   "fork repeats the path to the fork point",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"a": {"b1": {"c": {}}, "b2": {}}}, nil)},
			want:   []string{"s1:main|a,b1,c", "s1:main|(a),b2"},
		},
		{
			name: "nested forks",
			stacks: []*config.Stack{tree("s1", "main",
				config.BranchTree{"a": {"b": {"c1": {}, "c2": {}}, "d": {}}}, nil)},
			want: []string{"s1:main|a,b,c1", "s1:main|(a),(b),c2", "s1:main|(a),d"},
		},
		{
			name:   "existing catalog order decides which child continues",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"a": {"b1": {}, "b2": {}}}, nil)},
			old: &Catalog{Stacks: []Stack{{Trunk: BranchRef{Branch: "main"},
				Branches: []BranchRef{{Branch: "a"}, {Branch: "b2"}}}}},
			want: []string{"s1:main|a,b2", "s1:main|(a),b1"},
		},
		{
			name:   "merged layers stay flagged in place",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"a": {"b": {}}}, []string{"a"})},
			want:   []string{"s1:main|a*,b"},
		},
		{
			name:   "merged fork point is shared and flagged",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"a": {"b1": {}, "b2": {}}}, []string{"a"})},
			want:   []string{"s1:main|a*,b1", "s1:main|(a*),b2"},
		},
		{
			name: "merged branch without a PR is omitted",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"a": {"b": {}}},
				[]string{"a"}, "a")},
			want: []string{"s1:main|b"},
		},
		{
			name:   "fully merged chain is dropped",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"a": {"b": {}}, "c": {}}, []string{"a", "b"})},
			want:   []string{"s1:main|c"},
		},
		{
			// The first child being merged must not end the chain on the
			// shared fork point (no unique layer to anchor gh-stack on).
			name:   "merged first child: the active sibling continues the chain",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"a": {"b1": {}, "b2": {}}}, []string{"b1"})},
			want:   []string{"s1:main|a,b2"},
		},
		{
			name: "merged first child with an active subtree still continues",
			stacks: []*config.Stack{tree("s1", "main",
				config.BranchTree{"a": {"b1": {"c": {}}, "b2": {}}}, []string{"b1"})},
			want: []string{"s1:main|a,b1*,c", "s1:main|(a),b2"},
		},
		{
			name:   "catalog preference ignored for a fully merged child",
			stacks: []*config.Stack{tree("s1", "main", config.BranchTree{"a": {"b1": {}, "b2": {}}}, []string{"b2"})},
			old: &Catalog{Stacks: []Stack{{Trunk: BranchRef{Branch: "main"},
				Branches: []BranchRef{{Branch: "a"}, {Branch: "b2"}}}}},
			want: []string{"s1:main|a,b1"},
		},
		{
			name: "stack rooted on another stack's branch comes after it, rooted at its trunk",
			stacks: []*config.Stack{
				tree("aa", "z", config.BranchTree{"q": {}}, nil),
				tree("zz", "main", config.BranchTree{"z": {}}, nil),
			},
			want: []string{"zz:main|z", "aa:main|(z),q"},
		},
		{
			name: "stacks ordered by hash; root carried as trunk",
			stacks: []*config.Stack{
				tree("zz", "main", config.BranchTree{"z": {}}, nil),
				tree("aa", "develop", config.BranchTree{"q": {}}, nil),
			},
			want: []string{"aa:develop|q", "zz:main|z"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chainSummary(Chains(tt.stacks, tt.old))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Chains = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChainAccessors(t *testing.T) {
	c := Chain{Trunk: "main", Branches: []string{"m", "a", "b", "c"},
		Merged: map[string]bool{"m": true}, Shared: 2}
	if got := c.Active(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("Active = %v", got)
	}
	if got := c.Own(); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("Own = %v", got)
	}
	if got := c.Base(); got != "a" {
		t.Errorf("Base = %q, want a", got)
	}
	if got := c.Top(); got != "c" {
		t.Errorf("Top = %q, want c", got)
	}
	if !c.Has("m") || c.Has("zz") {
		t.Error("Has")
	}

	merged := Chain{Trunk: "main", Branches: []string{"m", "x"}, Merged: map[string]bool{"m": true}, Shared: 1}
	if merged.Base() != "main" {
		t.Errorf("Base skips merged shared layers: got %q", merged.Base())
	}
	empty := Chain{Trunk: "main", Merged: map[string]bool{}}
	if empty.Top() != "" || empty.Base() != "main" {
		t.Error("empty chain accessors")
	}
}

func TestChainLookups(t *testing.T) {
	chains := Chains([]*config.Stack{tree("s1", "main",
		config.BranchTree{"a": {"b1": {}, "b2": {"c": {}}}}, nil)}, nil)
	// main|a,b1  and  main|(a),b2,c
	if c, ok := ChainFor(chains, "a"); !ok || c.Top() != "b1" {
		t.Errorf("ChainFor(a) = %v, %v; want the chain owning a", c, ok)
	}
	if c, ok := ChainFor(chains, "c"); !ok || c.Top() != "c" {
		t.Errorf("ChainFor(c) = %v, %v", c, ok)
	}
	if _, ok := ChainFor(chains, "nope"); ok {
		t.Error("ChainFor(nope) found a chain")
	}
	if got := ChainsSharing(chains, "a"); len(got) != 1 || got[0].Top() != "c" {
		t.Errorf("ChainsSharing(a) = %v", got)
	}
	if got := ChainsSharing(chains, "b2"); len(got) != 0 {
		t.Errorf("ChainsSharing(b2) = %v, want none", got)
	}
	if Unique(chains, "a") || !Unique(chains, "b1") || !Unique(chains, "c") || Unique(chains, "nope") {
		t.Error("Unique")
	}
}

func TestProject(t *testing.T) {
	g := fakeGit{
		heads: map[string]string{"main": "M", "a": "A", "b": "B", "c": "C", "user1": "U"},
		ancestors: map[string]bool{
			"oldA>b": true, // b still contains a's old tip
		},
		mergeBases: map[string]string{"main>a": "M0", "a>b": "A", "a>c": "A", "main>c": "M0"},
	}
	unmanaged := Stack{Trunk: BranchRef{Branch: "main", Head: "M"}, Branches: []BranchRef{{Branch: "user1", Base: "M"}}}
	old := &Catalog{Repository: "github.com:o/r", Stacks: []Stack{
		unmanaged,
		{ID: "77", Number: 7, Trunk: BranchRef{Branch: "main", Head: "Mold"}, Branches: []BranchRef{
			{Branch: "a", Base: "stale", PullRequest: &PullRequestRef{Number: 1, ID: "PR_1"}},
			{Branch: "b", Base: "oldA"},
		}},
	}}
	s := tree("s1", "main", config.BranchTree{"a": {"b": {}, "c": {}}}, nil)
	s.Branches[0].PRNumber = 1 // a: same PR as catalog
	s.Branches[1].PRNumber = 2 // b: new PR
	s.Branches[1].PRUrl = "https://x/pull/2"
	stacks := []*config.Stack{s}
	managed := map[string]bool{"a": true, "b": true, "c": true}

	chains := Chains(stacks, old)
	next, changed := Project(old, chains, stacks, managed, g)
	if !changed {
		t.Fatal("expected a change")
	}
	if next.Repository != "github.com:o/r" {
		t.Errorf("Repository not preserved: %q", next.Repository)
	}
	if len(next.Stacks) != 3 || !reflect.DeepEqual(next.Stacks[0], unmanaged) {
		t.Fatalf("unmanaged stack not kept first: %+v", next.Stacks)
	}
	ab := next.Stacks[1]
	if ab.ID != "77" || ab.Number != 7 || ab.Trunk.Head != "Mold" {
		t.Errorf("stack identity / trunk head not preserved: %+v", ab)
	}
	if got := ab.Branches[0]; got.Base != "M0" || got.PullRequest == nil || got.PullRequest.ID != "PR_1" {
		t.Errorf("a: base must be recomputed (old base not an ancestor), PR kept: %+v", got)
	}
	if got := ab.Branches[1]; got.Base != "oldA" || got.PullRequest == nil || got.PullRequest.Number != 2 || got.PullRequest.URL != "https://x/pull/2" {
		t.Errorf("b: base must be kept (still an ancestor), PR from ezstack: %+v %+v", got, got.PullRequest)
	}
	ac := next.Stacks[2]
	if ac.ID != "" || ac.Trunk.Head != "M" {
		t.Errorf("new chain must not steal the old id and takes the live trunk head: %+v", ac)
	}
	if names := ac.BranchNames(); !reflect.DeepEqual(names, []string{"a", "c"}) || ac.Branches[1].Base != "A" {
		t.Errorf("fork chain = %v base %q", names, ac.Branches[1].Base)
	}

	// Re-projecting the result is a no-op.
	if _, changed := Project(next, Chains(stacks, next), stacks, managed, g); changed {
		t.Error("projection is not idempotent")
	}
}

func TestProject_MergedAndForgotten(t *testing.T) {
	g := fakeGit{heads: map[string]string{"main": "M", "b": "B", "keep": "K"},
		ancestors: map[string]bool{"Atip>b": true}}
	old := &Catalog{Stacks: []Stack{
		{Trunk: BranchRef{Branch: "main"}, Branches: []BranchRef{{Branch: "a", Base: "M"}, {Branch: "b", Base: "Atip"}}},
		{Trunk: BranchRef{Branch: "main"}, Branches: []BranchRef{{Branch: "gone"}}},  // forgotten
		{Trunk: BranchRef{Branch: "main"}, Branches: []BranchRef{{Branch: "ghost"}}}, // user's stack, branch gone: still theirs
		{Trunk: BranchRef{Branch: "main"}, Branches: []BranchRef{{Branch: "keep"}}},  // user's own stack
	}}
	s := tree("s1", "main", config.BranchTree{"a": {"b": {}}}, []string{"a"})
	stacks := []*config.Stack{s}
	managed := map[string]bool{"a": true, "b": true, "gone": true}
	next, _ := Project(old, Chains(stacks, old), stacks, managed, g)

	var names []string
	for _, st := range next.Stacks {
		names = append(names, strings.Join(st.BranchNames(), ","))
	}
	if !reflect.DeepEqual(names, []string{"ghost", "keep", "a,b"}) {
		t.Fatalf("stacks = %v, want [ghost keep a,b]", names)
	}
	ab := next.Stacks[2]
	if pr := ab.Branches[0].PullRequest; pr == nil || !pr.Merged {
		t.Errorf("merged layer not flagged: %+v", ab.Branches[0])
	}
	if ab.Branches[1].Base != "Atip" {
		t.Errorf("child of merged parent must keep its --onto anchor, got %q", ab.Branches[1].Base)
	}
}

// TestChains_MergedFirstChildAnchors: every chain keeps a layer no other
// chain has, so gh-stack can be pointed at it.
func TestChains_MergedFirstChildAnchors(t *testing.T) {
	stacks := []*config.Stack{tree("s1", "main",
		config.BranchTree{"a": {"b1": {}, "b2": {}, "b3": {}}}, []string{"b1"})}
	chains := Chains(stacks, nil)
	if len(chains) != 2 {
		t.Fatalf("chains = %v", chainSummary(chains))
	}
	for _, c := range chains {
		if !Unique(chains, c.Top()) {
			t.Errorf("chain %v has no unique top", c.Branches)
		}
	}
}

func TestDependencyOrder(t *testing.T) {
	base := tree("bb", "main", config.BranchTree{"base": {"mid": {}}}, nil)
	onMid := tree("aa", "mid", config.BranchTree{"x": {}}, nil)
	onX := tree("a0", "x", config.BranchTree{"y": {}}, nil)
	other := tree("cc", "develop", config.BranchTree{"o": {}}, nil)
	var got []string
	for _, s := range dependencyOrder([]*config.Stack{other, onX, onMid, base}) {
		got = append(got, s.Hash)
	}
	if want := []string{"bb", "aa", "a0", "cc"}; !reflect.DeepEqual(got, want) {
		t.Errorf("dependencyOrder = %v, want %v", got, want)
	}
	// A (corrupt) cycle must not hang or drop stacks.
	c1 := tree("c1", "q2", config.BranchTree{"q1": {}}, nil)
	c2 := tree("c2", "q1", config.BranchTree{"q2": {}}, nil)
	if n := len(dependencyOrder([]*config.Stack{c1, c2})); n != 2 {
		t.Errorf("cycle: %d stacks, want 2", n)
	}
}

func TestRecordedBase(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if RecordedBase(repo, "a") != "" {
		t.Error("no catalog → empty base")
	}
	dir, err := CommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, catalogFile), []byte(`{"schemaVersion":1,"stacks":[
		{"trunk":{"branch":"main"},"branches":[{"branch":"a","base":"AAA"},{"branch":"b","base":"BBB"}]}]}`), 0o644)
	if got := RecordedBase(repo, "b"); got != "BBB" {
		t.Errorf("RecordedBase(b) = %q", got)
	}
	if got := RecordedBase(repo, "zzz"); got != "" {
		t.Errorf("untracked branch = %q", got)
	}
	if RecordedBase(t.TempDir(), "a") != "" {
		t.Error("non-repo → empty base")
	}
}

// TestHintRewriter_ConcurrentStreams mirrors run(): stdout and stderr are
// copied by separate goroutines into rewriters sharing a destination lock.
// Run under -race; every line must arrive whole.
func TestHintRewriter_ConcurrentStreams(t *testing.T) {
	var out bytes.Buffer
	var mu sync.Mutex
	streams := []*hintRewriter{{w: &out, mu: &mu}, {w: &out, mu: &mu}}
	var wg sync.WaitGroup
	for i, h := range streams {
		wg.Add(1)
		go func(i int, h *hintRewriter) {
			defer wg.Done()
			line := strings.Repeat(string(rune('a'+i)), 40) + "\n"
			for n := 0; n < 200; n++ {
				// Split each line across writes to exercise the line buffer.
				h.Write([]byte(line[:13]))
				h.Write([]byte(line[13:]))
			}
			h.Flush()
		}(i, h)
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 400 {
		t.Fatalf("got %d lines, want 400", len(lines))
	}
	for _, l := range lines {
		if l != strings.Repeat("a", 40) && l != strings.Repeat("b", 40) {
			t.Fatalf("interleaved line %q", l)
		}
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"gh stack version 0.2.1\n", [3]int{0, 2, 1}, true},
		{"gh stack version v1.10.3 (2026-10-01)", [3]int{1, 10, 3}, true},
		{"gh stack version dev", [3]int{}, false},
		{"", [3]int{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseVersion(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
	if !versionLess([3]int{0, 1, 9}, MinVersion) || versionLess([3]int{0, 2, 0}, MinVersion) ||
		versionLess([3]int{1, 0, 0}, MinVersion) {
		t.Error("versionLess vs MinVersion")
	}
}

func TestWrapRunErr_ExitCodes(t *testing.T) {
	sentinels := map[int]error{2: ErrNotInStack, 3: ErrConflict, 4: ErrGitHubAPI, 5: ErrInvalidArgs,
		6: ErrDisambiguation, 7: ErrRebaseActive, 8: ErrLocked, 9: ErrStacksUnavailable, 10: ErrModifyRecovery}
	for code, want := range sentinels {
		runErr := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
		err := wrapRunErr([]string{"rebase"}, runErr, "noise\n\x1b[31m✗ real reason\x1b[0m\n\n")
		if !errors.Is(err, want) {
			t.Errorf("exit %d: errors.Is(%v, %v) = false", code, err, want)
		}
		var ge *Error
		if !errors.As(err, &ge) || ge.ExitCode != code || ge.Stderr != "✗ real reason" {
			t.Errorf("exit %d: %+v", code, ge)
		}
		if !strings.Contains(err.Error(), "real reason") {
			t.Errorf("message lacks stderr: %v", err)
		}
	}
	err := wrapRunErr([]string{"push"}, exec.Command("sh", "-c", "exit 1").Run(), "")
	var ge *Error
	if !errors.As(err, &ge) || errors.Unwrap(err) != nil || !strings.Contains(err.Error(), "gh stack push failed (exit 1)") {
		t.Errorf("generic exit: %v", err)
	}
	if wrapRunErr(nil, nil, "x") != nil {
		t.Error("nil error must stay nil")
	}
	if err := wrapRunErr([]string{"x"}, errors.New("boom"), ""); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("non-exit error: %v", err)
	}
}

func TestHintRewriter(t *testing.T) {
	var out bytes.Buffer
	h := &hintRewriter{w: &out}
	chunks := []string{
		"✓ Rebased a onto main\nTo push up your ch", "anges, run `gh stack push`\n",
		"\x1b[2mTo create PRs for this stack, run `gh stack submit`\x1b[0m\n",
		"Resolve conflicts on a, then run `gh stack rebase --continue`\n",
		"Or abort this operation with `gh stack rebase --abort`\n",
		"partial tail",
	}
	for _, c := range chunks {
		if n, err := h.Write([]byte(c)); err != nil || n != len(c) {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	h.Flush()
	want := "✓ Rebased a onto main\n" +
		"Resolve conflicts on a, then run `ezs sync --continue`\n" +
		"Or abort this operation with `ezs sync --abort`\n" +
		"partial tail"
	if out.String() != want {
		t.Errorf("rewritten output:\n%q\nwant:\n%q", out.String(), want)
	}
}

func TestLastLine(t *testing.T) {
	if got := lastLine("one\n\x1b[31mtwo\x1b[0m\n  \n"); got != "two" {
		t.Errorf("lastLine = %q", got)
	}
	if got := lastLine(""); got != "" {
		t.Errorf("lastLine(empty) = %q", got)
	}
	tb := &tailBuffer{max: 4}
	tb.Write([]byte("abc"))
	tb.Write([]byte("def"))
	if tb.String() != "cdef" {
		t.Errorf("tailBuffer = %q", tb.String())
	}
}

func TestCatalog_LoadAndUpdate(t *testing.T) {
	dir := t.TempDir()
	cat, err := LoadCatalog(dir)
	if err != nil || len(cat.Stacks) != 0 || cat.SchemaVersion != catalogSchemaVersion {
		t.Fatalf("missing catalog: %+v, %v", cat, err)
	}

	// No change → no file written.
	if err := updateCatalog(dir, func(*Catalog) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, catalogFile)); !os.IsNotExist(err) {
		t.Fatal("catalog written without a change")
	}

	if err := updateCatalog(dir, func(c *Catalog) (bool, error) {
		c.Stacks = append(c.Stacks, Stack{Trunk: BranchRef{Branch: "main"}, Branches: []BranchRef{{Branch: "a"}}})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	cat, err = LoadCatalog(dir)
	if err != nil || len(cat.Stacks) != 1 || cat.Stacks[0].Branches[0].Branch != "a" {
		t.Fatalf("round trip: %+v, %v", cat, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".gh-stack-*")); len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}

	fnErr := errors.New("fn failed")
	if err := updateCatalog(dir, func(*Catalog) (bool, error) { return true, fnErr }); !errors.Is(err, fnErr) {
		t.Errorf("fn error not propagated: %v", err)
	}

	// A paused gh-stack rebase owns the catalog.
	os.WriteFile(filepath.Join(dir, rebaseStateFile), []byte("{}"), 0o644)
	if !RebaseInProgress(dir) {
		t.Fatal("RebaseInProgress = false")
	}
	if err := updateCatalog(dir, func(*Catalog) (bool, error) { return true, nil }); !errors.Is(err, ErrRebasePaused) {
		t.Errorf("update during paused rebase: %v", err)
	}
	os.Remove(filepath.Join(dir, rebaseStateFile))

	// Pre-0.2 per-worktree catalogs must be migrated by gh-stack first.
	legacy := filepath.Join(dir, "worktrees", "wt1")
	os.MkdirAll(legacy, 0o755)
	os.WriteFile(filepath.Join(legacy, catalogFile), []byte("{}"), 0o644)
	if err := updateCatalog(dir, func(*Catalog) (bool, error) { return true, nil }); !errors.Is(err, errLegacyCatalog) {
		t.Errorf("update with legacy catalog: %v", err)
	}
}

func TestLoadCatalog_Errors(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, catalogFile), []byte(`{"schemaVersion": 2, "stacks": []}`), 0o644)
	if _, err := LoadCatalog(dir); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("newer schema: %v", err)
	}
	os.WriteFile(filepath.Join(dir, catalogFile), []byte(`{not json`), 0o644)
	if _, err := LoadCatalog(dir); err == nil {
		t.Error("corrupt catalog accepted")
	}
	os.WriteFile(filepath.Join(dir, catalogFile), []byte(`{"schemaVersion": 1}`), 0o644)
	if cat, err := LoadCatalog(dir); err != nil || cat.Stacks == nil {
		t.Errorf("null stacks must load as empty: %+v %v", cat, err)
	}
}

func TestAcquireLock_TimesOutWhileHeld(t *testing.T) {
	old := lockTimeout
	lockTimeout = 200 * time.Millisecond
	defer func() { lockTimeout = old }()

	path := filepath.Join(t.TempDir(), operationLockFile)
	held, err := acquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(path); !errors.Is(err, ErrLocked) {
		t.Errorf("second acquire = %v, want ErrLocked", err)
	}
	held.release()
	again, err := acquireLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	again.release()
	again.release() // idempotent
}
