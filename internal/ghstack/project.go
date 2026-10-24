package ghstack

import (
	"encoding/json"
	"sort"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// Chain is one linear gh-stack stack derived from an ezstack stack tree: a
// root→leaf path. Where the tree forks, every path after the first repeats
// the ancestors it shares with an earlier chain (Shared counts them).
//
// Repeating ancestors is deliberate. gh-stack rebases the bottom layer of a
// stack onto its trunk with a plain `git rebase`, so a chain whose trunk was
// a branch that just got rewritten would replay that branch's old commits.
// With the fork point inside the chain instead, the fork's child is moved
// with `--onto` from its recorded base, exactly like any other layer; the
// repeated ancestors are already up to date and rebase as no-ops.
type Chain struct {
	StackHash string   // ezstack stack the chain belongs to
	Trunk     string   // the stack root
	Branches  []string // bottom→top, including merged layers
	Merged    map[string]bool
	Shared    int // leading layers that belong to an earlier chain
}

// Active returns the chain's non-merged layers bottom→top.
func (c Chain) Active() []string {
	var out []string
	for _, b := range c.Branches {
		if !c.Merged[b] {
			out = append(out, b)
		}
	}
	return out
}

// Own returns the active layers this chain doesn't share with an earlier
// one — the branches it is responsible for pushing, previewing and linking.
func (c Chain) Own() []string {
	var out []string
	for i, b := range c.Branches {
		if i >= c.Shared && !c.Merged[b] {
			out = append(out, b)
		}
	}
	return out
}

// Base is the branch the chain's own layers stack on: the last shared layer
// (the fork point) or, for a first chain, the trunk.
func (c Chain) Base() string {
	for i := c.Shared - 1; i >= 0; i-- {
		if !c.Merged[c.Branches[i]] {
			return c.Branches[i]
		}
	}
	return c.Trunk
}

// Top returns the chain's topmost non-merged layer ("" when fully merged).
func (c Chain) Top() string {
	a := c.Active()
	if len(a) == 0 {
		return ""
	}
	return a[len(a)-1]
}

// Has reports whether branch is a layer of the chain.
func (c Chain) Has(branch string) bool {
	for _, b := range c.Branches {
		if b == branch {
			return true
		}
	}
	return false
}

// Git is the read-only git access the projection needs.
type Git interface {
	// MergeBase returns the merge-base of parent and branch, or "" when either
	// ref is unavailable (e.g. a merged parent whose branch was deleted).
	MergeBase(parent, branch string) string
	// Head returns the commit a branch points at, or "" when it doesn't exist.
	Head(branch string) string
	// IsAncestor reports whether commit is an ancestor of (or equal to) branch.
	IsAncestor(commit, branch string) bool
}

// Chains decomposes ezstack stacks into root→leaf chains, in tree order (a
// chain's shared ancestors always belong to an earlier chain).
//
// At each fork the first child continues the current chain — preferring the
// child that already follows the fork point in the existing catalog, so a new
// fork doesn't reshuffle established stacks — and every other child starts a
// chain that repeats the path down to the fork point.
//
// Merged branches stay in place, flagged, so gh-stack drops their commits
// with `--onto`. Merged branches without a PR are omitted: gh-stack can only
// recognise a merged layer by its PR, and would otherwise try to rebase a
// branch that usually no longer exists.
func Chains(stacks []*config.Stack, old *Catalog) []Chain {
	follows := map[string]string{} // branch -> the layer above it in the old catalog
	if old != nil {
		for _, s := range old.Stacks {
			for i := 0; i+1 < len(s.Branches); i++ {
				if _, seen := follows[s.Branches[i].Branch]; !seen {
					follows[s.Branches[i].Branch] = s.Branches[i+1].Branch
				}
			}
		}
	}

	sorted := dependencyOrder(stacks)
	prefixes := rootPrefixes(sorted)

	var chains []Chain
	for _, s := range sorted {
		pre := prefixes[s]
		info := map[string]*config.Branch{}
		for _, b := range s.Branches {
			info[b.Name] = b
		}
		merged := func(name string) bool { b := info[name]; return b != nil && b.IsMerged }
		skip := func(name string) bool { b := info[name]; return b != nil && b.IsMerged && b.PRNumber == 0 }

		var hasActive func(name string, children config.BranchTree) bool
		hasActive = func(name string, children config.BranchTree) bool {
			if !merged(name) {
				return true
			}
			for k, c := range children {
				if hasActive(k, c) {
					return true
				}
			}
			return false
		}

		// walk appends name to chain ci.
		var walk func(ci int, name string, children config.BranchTree)
		walk = func(ci int, name string, children config.BranchTree) {
			if !skip(name) {
				chains[ci].Branches = append(chains[ci].Branches, name)
				if merged(name) {
					chains[ci].Merged[name] = true
				}
			}
			keys := sortedKeys(children)
			if len(keys) == 0 {
				return
			}
			// Continue into a child whose subtree still has something to
			// rebase: a chain whose top is a merged branch would end on the
			// fork point, which gh-stack can't address (it's in every chain
			// below the fork).
			cont := ""
			if f, ok := follows[name]; ok {
				if _, isChild := children[f]; isChild && hasActive(f, children[f]) {
					cont = f
				}
			}
			for _, k := range keys {
				if cont == "" && hasActive(k, children[k]) {
					cont = k
				}
			}
			if cont == "" {
				cont = keys[0]
				if f, ok := follows[name]; ok {
					if _, isChild := children[f]; isChild {
						cont = f
					}
				}
			}
			// Snapshot the path to the fork point before the continuing
			// child extends this chain.
			path := append([]string(nil), chains[ci].Branches...)
			walk(ci, cont, children[cont])
			for _, k := range keys {
				if k == cont {
					continue
				}
				c := Chain{StackHash: s.Hash, Trunk: s.Root, Merged: map[string]bool{},
					Branches: append([]string(nil), path...), Shared: len(path)}
				for _, p := range path {
					if merged(p) {
						c.Merged[p] = true
					}
				}
				chains = append(chains, c)
				walk(len(chains)-1, k, children[k])
			}
		}
		for _, k := range sortedKeys(s.Tree) {
			c := Chain{StackHash: s.Hash, Trunk: pre.trunk, Merged: map[string]bool{},
				Branches: append([]string(nil), pre.layers...), Shared: len(pre.layers)}
			for _, l := range pre.layers {
				if pre.merged[l] {
					c.Merged[l] = true
				}
			}
			chains = append(chains, c)
			walk(len(chains)-1, k, s.Tree[k])
		}
	}

	// Drop chains with nothing of their own left to rebase.
	out := chains[:0]
	for _, c := range chains {
		if len(c.Own()) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// rootPrefix is what a stack's chains start with: for a stack rooted on
// another stack's branch X, the layers from that stack's trunk down to X
// (shared with that stack's chains) and that trunk; otherwise nothing and the
// stack's own root. Rooting the chain at the real trunk, with X as a shared
// layer, keeps X's children moving with --onto when X is rewritten — with X
// as the trunk, gh-stack would plain-rebase them and replay X's old commits.
type rootPrefix struct {
	trunk  string
	layers []string
	merged map[string]bool
}

func rootPrefixes(stacks []*config.Stack) map[*config.Stack]rootPrefix {
	owner := map[string]*config.Stack{}
	branch := map[string]*config.Branch{}
	for _, s := range stacks {
		for _, b := range s.Branches {
			owner[b.Name] = s
			branch[b.Name] = b
		}
	}
	out := map[*config.Stack]rootPrefix{}
	var resolve func(s *config.Stack, seen map[*config.Stack]bool) rootPrefix
	resolve = func(s *config.Stack, seen map[*config.Stack]bool) rootPrefix {
		if p, ok := out[s]; ok {
			return p
		}
		p := rootPrefix{trunk: s.Root, merged: map[string]bool{}}
		if dep := owner[s.Root]; dep != nil && dep != s && !seen[s] {
			seen[s] = true
			base := resolve(dep, seen)
			p.trunk = base.trunk
			p.layers = append(p.layers, base.layers...)
			for k, v := range base.merged {
				p.merged[k] = v
			}
			// Path from dep's root down to s.Root, via tree parents.
			var path []string
			for name := s.Root; name != "" && name != dep.Root; {
				b := branch[name]
				if b == nil {
					break
				}
				if !(b.IsMerged && b.PRNumber == 0) {
					path = append([]string{name}, path...)
				}
				if b.IsMerged {
					p.merged[name] = true
				}
				name = b.BaseBranch
			}
			p.layers = append(p.layers, path...)
		}
		out[s] = p
		return p
	}
	for _, s := range stacks {
		resolve(s, map[*config.Stack]bool{})
	}
	return out
}

// dependencyOrder sorts stacks by hash, then moves every stack rooted on
// another stack's branch after that stack, so its chains are restacked once
// their trunk is up to date.
func dependencyOrder(stacks []*config.Stack) []*config.Stack {
	sorted := append([]*config.Stack(nil), stacks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Hash < sorted[j].Hash })
	owner := map[string]*config.Stack{} // branch -> stack containing it
	for _, s := range sorted {
		for _, b := range s.Branches {
			owner[b.Name] = s
		}
	}
	var out []*config.Stack
	placed := map[*config.Stack]bool{}
	visiting := map[*config.Stack]bool{}
	var place func(s *config.Stack)
	place = func(s *config.Stack) {
		if placed[s] || visiting[s] {
			return // visiting: a cycle, which a valid config can't have
		}
		visiting[s] = true
		if dep := owner[s.Root]; dep != nil && dep != s {
			place(dep)
		}
		placed[s] = true
		out = append(out, s)
	}
	for _, s := range sorted {
		place(s)
	}
	return out
}

// Project rewrites old so its ezstack-managed stacks match chains, returning
// the new catalog and whether anything changed. `managed` names every branch
// ezstack owns (plus any it just stopped tracking); other catalog stacks —
// ones built with gh-stack directly — are left untouched.
//
// Per layer it preserves gh-stack's recorded PR ref and `base` SHA for as
// long as that base is still in the branch's history. gh-stack only consults
// the recorded base when the parent's current tip is NOT in the branch's
// history (the parent was rewritten, merged, or replaced), and in each of
// those cases the old base is exactly the `--onto` anchor that drops the old
// parent's commits:
//
//   - parent squash-merged and pruned: base = old parent tip → replay only
//     the branch's own commits onto the trunk;
//   - branch reparented without rebasing: base = old parent tip → gh-stack
//     moves only the branch's own commits onto the new parent.
//
// Once the branch no longer contains the recorded base (it was rebased
// elsewhere), the base is recomputed as merge-base(parent, branch).
func Project(old *Catalog, chains []Chain, stacks []*config.Stack, managed map[string]bool, g Git) (*Catalog, bool) {
	if old == nil {
		old = &Catalog{SchemaVersion: catalogSchemaVersion}
	}
	info := map[string]*config.Branch{}
	for _, s := range stacks {
		for _, b := range s.Branches {
			info[b.Name] = b
		}
	}

	oldLayers := map[string]BranchRef{}
	var kept []Stack
	var oldManaged []Stack
	for _, s := range old.Stacks {
		isManaged := false
		for _, b := range s.Branches {
			if managed[b.Branch] {
				isManaged = true
				break
			}
		}
		if !isManaged {
			kept = append(kept, s)
			continue
		}
		oldManaged = append(oldManaged, s)
		for _, b := range s.Branches {
			if _, seen := oldLayers[b.Branch]; !seen {
				oldLayers[b.Branch] = b
			}
		}
	}

	usedIDs := map[int]bool{}
	next := &Catalog{SchemaVersion: catalogSchemaVersion, Repository: old.Repository}
	next.Stacks = append(next.Stacks, kept...)
	for _, c := range chains {
		st := Stack{Trunk: BranchRef{Branch: c.Trunk}}
		if i := matchOldStack(oldManaged, c, usedIDs); i >= 0 {
			usedIDs[i] = true
			st.ID, st.Number = oldManaged[i].ID, oldManaged[i].Number
			if oldManaged[i].Trunk.Branch == c.Trunk {
				// gh-stack maintains the trunk head itself during rebases;
				// keep its value so an unchanged stack doesn't churn the file.
				st.Trunk.Head = oldManaged[i].Trunk.Head
			}
		}
		if st.Trunk.Head == "" {
			st.Trunk.Head = g.Head(c.Trunk)
		}
		for i, name := range c.Branches {
			parent := c.Trunk
			if i > 0 {
				parent = c.Branches[i-1]
			}
			ref := BranchRef{Branch: name}
			ol, hadOld := oldLayers[name]
			if hadOld {
				ref.Head = ol.Head
				if ol.PullRequest != nil {
					pr := *ol.PullRequest
					ref.PullRequest = &pr
				}
			}
			if hadOld && ol.Base != "" && g.IsAncestor(ol.Base, name) {
				ref.Base = ol.Base
			} else {
				ref.Base = g.MergeBase(parent, name)
			}
			if b := info[name]; b != nil && b.PRNumber > 0 {
				if ref.PullRequest == nil || ref.PullRequest.Number != b.PRNumber {
					ref.PullRequest = &PullRequestRef{Number: b.PRNumber, URL: b.PRUrl}
				}
			}
			if c.Merged[name] && ref.PullRequest != nil {
				ref.PullRequest.Merged = true
			}
			st.Branches = append(st.Branches, ref)
		}
		next.Stacks = append(next.Stacks, st)
	}

	before, _ := json.Marshal(normalized(old))
	after, _ := json.Marshal(normalized(next))
	return next, string(before) != string(after)
}

// matchOldStack picks the unused old managed stack sharing the most of c's
// own layers (ties: first), so a stack keeps its GitHub stack id/number
// across reshapes. Returns -1 when nothing overlaps.
func matchOldStack(old []Stack, c Chain, used map[int]bool) int {
	own := map[string]bool{}
	for _, b := range c.Own() {
		own[b] = true
	}
	best, bestScore := -1, 0
	for i, s := range old {
		if used[i] {
			continue
		}
		score := 0
		for _, b := range s.Branches {
			if own[b.Branch] {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

func normalized(c *Catalog) *Catalog {
	cp := *c
	cp.SchemaVersion = catalogSchemaVersion
	if cp.Stacks == nil {
		cp.Stacks = []Stack{}
	}
	return &cp
}

func sortedKeys(t config.BranchTree) []string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
