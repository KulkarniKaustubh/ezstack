package ghstack

import (
	"bytes"
	"os/exec"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// Reconcile rewrites the gh-stack catalog of the repository at repoDir so it
// mirrors stacks (every ezstack stack of that repository), and returns the
// resulting chains in tree order.
//
// forget names branches ezstack just stopped tracking (deleted or untracked);
// catalog stacks containing them are treated as ezstack-managed and dropped
// or reshaped accordingly. Stacks the user built with gh-stack directly (no
// ezstack branch in them) are never touched.
//
// It refuses while a gh-stack rebase is paused (ErrRebasePaused): the paused
// cascade owns the catalog until it is continued or aborted.
func Reconcile(repoDir string, stacks []*config.Stack, forget ...string) ([]Chain, error) {
	commonDir, err := CommonDir(repoDir)
	if err != nil {
		return nil, err
	}
	managed := map[string]bool{}
	for _, s := range stacks {
		for _, b := range s.Branches {
			managed[b.Name] = true
		}
	}
	for _, f := range forget {
		managed[f] = true
	}

	var chains []Chain
	err = updateCatalog(commonDir, func(cat *Catalog) (bool, error) {
		chains = Chains(stacks, cat)
		next, changed := Project(cat, chains, stacks, managed, newRepoGit(repoDir))
		*cat = *next
		return changed, nil
	})
	if err != nil {
		return nil, err
	}
	return chains, nil
}

// PlanChains returns the chains Reconcile would produce, reading the catalog
// without writing it. Used by read-only paths such as `ezs sync --dry-run`.
func PlanChains(repoDir string, stacks []*config.Stack) ([]Chain, error) {
	commonDir, err := CommonDir(repoDir)
	if err != nil {
		return nil, err
	}
	cat, err := LoadCatalog(commonDir)
	if err != nil {
		return nil, err
	}
	return Chains(stacks, cat), nil
}

// ChainFor returns the chain that owns branch (holds it as an active,
// non-shared layer).
func ChainFor(chains []Chain, branch string) (Chain, bool) {
	for _, c := range chains {
		for _, b := range c.Own() {
			if b == branch {
				return c, true
			}
		}
	}
	return Chain{}, false
}

// ChainsSharing returns the chains, other than branch's own, that carry
// branch as a shared layer — the chains of its other subtrees.
func ChainsSharing(chains []Chain, branch string) []Chain {
	var out []Chain
	for _, c := range chains {
		for i, b := range c.Branches {
			if b == branch && i < c.Shared {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// Unique reports whether branch is a layer of exactly one chain. gh-stack
// resolves a stack from a branch name (positional or the current checkout)
// and refuses one that is in several stacks.
func Unique(chains []Chain, branch string) bool {
	n := 0
	for _, c := range chains {
		if c.Has(branch) {
			n++
		}
	}
	return n == 1
}

// RecordedBase returns the base gh-stack recorded for branch (its parent's
// tip as of the last restack), or "" when the branch isn't tracked.
func RecordedBase(repoDir, branch string) string {
	commonDir, err := CommonDir(repoDir)
	if err != nil {
		return ""
	}
	cat, err := LoadCatalog(commonDir)
	if err != nil {
		return ""
	}
	for _, s := range cat.Stacks {
		for _, b := range s.Branches {
			if b.Branch == branch {
				return b.Base
			}
		}
	}
	return ""
}

// repoGit implements Git with plain git subprocesses. Branch heads are read
// once with a single for-each-ref.
type repoGit struct {
	dir   string
	heads map[string]string
}

func newRepoGit(dir string) *repoGit { return &repoGit{dir: dir} }

func (r *repoGit) git(args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func (r *repoGit) Head(branch string) string {
	if r.heads == nil {
		r.heads = map[string]string{}
		for _, line := range strings.Split(r.git("for-each-ref", "--format=%(objectname) %(refname:strip=2)", "refs/heads"), "\n") {
			if sha, name, ok := strings.Cut(line, " "); ok {
				r.heads[name] = sha
			}
		}
	}
	return r.heads[branch]
}

func (r *repoGit) IsAncestor(commit, branch string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", commit, "refs/heads/"+branch)
	cmd.Dir = r.dir
	return cmd.Run() == nil
}

func (r *repoGit) MergeBase(parent, branch string) string {
	if r.Head(branch) == "" {
		return ""
	}
	ref := "refs/heads/" + parent
	if r.Head(parent) == "" {
		// A parent with no local branch (merged and pruned, or a remote-only
		// root): fall back to its remote-tracking ref when there is one.
		ref = r.git("for-each-ref", "--count=1", "--format=%(refname)", "refs/remotes/*/"+parent)
		if ref == "" {
			return ""
		}
	}
	return r.git("merge-base", ref, "refs/heads/"+branch)
}
