package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/git"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/v4/internal/ui"
	"github.com/spf13/pflag"
)

func Diff(args []string) error {
	fs := pflag.NewFlagSet("diff", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sShow diff against parent branch%s

%sUSAGE%s
    ezs diff [options] [-- git-diff-options]

%sDESCRIPTION%s
    Shows the diff between the current branch and its parent in the stack.
    Any arguments after -- are passed directly to git diff.

%sOPTIONS%s
    -b, --branch <name>  Show diff for a specific branch (default: current)
    --stat               Show diffstat only
    --json               Output file-level diff stats as JSON
    --vs-pr              Diff the local branch against origin/<branch> — i.e.
                         show exactly what your next push would add to the PR.
                         Fails if the branch has not been pushed.
    -h, --help           Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFlag := fs.StringP("branch", "b", "", "Show diff for a specific branch")
	stat := fs.Bool("stat", false, "Show diffstat only")
	jsonFlag := fs.Bool("json", false, "Output file-level diff stats as JSON")
	vsPR := fs.Bool("vs-pr", false, "Diff local branch against origin/<branch>")
	helpFlag := fs.BoolP("help", "h", false, "Show help")

	if err := fs.Parse(args); err != nil {
		if err == pflag.ErrHelp {
			return nil
		}
		return err
	}
	if *helpFlag {
		fs.Usage()
		return nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	g := git.New(cwd)
	mgr, err := stack.NewReadOnlyManager(cwd)
	if err != nil {
		return err
	}

	var branchName string
	var parent string

	if *branchFlag != "" {
		branch := mgr.GetBranch(*branchFlag)
		if branch == nil {
			return fmt.Errorf("branch %q not found in any stack", *branchFlag)
		}
		branchName = branch.Name
		parent = branch.Parent
	} else {
		_, branch, err := mgr.GetCurrentStack()
		if err != nil {
			return err
		}
		branchName = branch.Name
		parent = branch.Parent
	}

	// --vs-pr: base is origin/<branch>, end is working tree (if current) or
	// the local branch ref. This answers "what would my next push change on
	// the PR?". Only valid once the branch has been pushed.
	if *vsPR {
		if !g.RemoteBranchExists(branchName) {
			return fmt.Errorf("branch %q has not been pushed — nothing on origin to compare against", branchName)
		}
		base := "origin/" + branchName
		end := branchName
		if cur, _ := g.CurrentBranch(); cur == branchName {
			end = ""
		}

		if *jsonFlag {
			return diffJSON(g, base, end)
		}
		diffArgs := []string{"diff", base}
		if end != "" {
			diffArgs = append(diffArgs, end)
		}
		if *stat {
			diffArgs = append(diffArgs, "--stat")
		}
		diffArgs = append(diffArgs, fs.Args()...)
		return g.RunInteractive(diffArgs...)
	}

	// Use origin/ for the parent when available to get consistent diffs
	parentRef := parent
	if g.RemoteBranchExists(parent) {
		parentRef = "origin/" + parent
	}

	base, end, err := diffRange(g, parentRef, branchName)
	if err != nil {
		return err
	}

	if *jsonFlag {
		return diffJSON(g, base, end)
	}

	diffArgs := []string{"diff", base}
	if end != "" {
		diffArgs = append(diffArgs, end)
	}
	if *stat {
		diffArgs = append(diffArgs, "--stat")
	}
	diffArgs = append(diffArgs, fs.Args()...)

	return g.RunInteractive(diffArgs...)
}

// diffRange returns the base and end refs for `git diff`. When branchName is
// the currently checked-out branch, end is empty so that `git diff <base>`
// compares the merge-base to the working tree, including unstaged and staged
// changes. Otherwise we diff committed state only.
func diffRange(g *git.Git, parentRef, branchName string) (string, string, error) {
	mb, err := g.RunCapture("merge-base", parentRef, branchName)
	if err != nil {
		return parentRef + "..." + branchName, "", nil
	}
	base := strings.TrimSpace(mb)
	if base == "" {
		return parentRef + "..." + branchName, "", nil
	}
	cur, _ := g.CurrentBranch()
	if cur == branchName {
		return base, "", nil
	}
	return base, branchName, nil
}

type diffFileJSON struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type diffOutputJSON struct {
	Files        []diffFileJSON `json:"files"`
	TotalFiles   int            `json:"total_files"`
	TotalAdded   int            `json:"total_additions"`
	TotalDeleted int            `json:"total_deletions"`
}

func diffJSON(g *git.Git, base, end string) error {
	args := []string{"diff", "--numstat", base}
	if end != "" {
		args = append(args, end)
	}
	output, err := g.RunCapture(args...)
	if err != nil {
		return fmt.Errorf("git diff failed: %w", err)
	}

	files := make([]diffFileJSON, 0)
	totalAdded := 0
	totalDeleted := 0

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		deleted, _ := strconv.Atoi(parts[1])
		filePath := parts[2]
		// Handle renames: "old => new"
		if idx := strings.Index(filePath, " => "); idx != -1 {
			filePath = filePath[idx+4:]
		}
		files = append(files, diffFileJSON{
			Path:      filePath,
			Additions: added,
			Deletions: deleted,
		})
		totalAdded += added
		totalDeleted += deleted
	}

	result := diffOutputJSON{
		Files:        files,
		TotalFiles:   len(files),
		TotalAdded:   totalAdded,
		TotalDeleted: totalDeleted,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
