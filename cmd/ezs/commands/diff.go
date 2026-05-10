package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
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
    -h, --help           Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFlag := fs.StringP("branch", "b", "", "Show diff for a specific branch")
	stat := fs.Bool("stat", false, "Show diffstat only")
	jsonFlag := fs.Bool("json", false, "Output file-level diff stats as JSON")
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

	var s *config.Stack
	var branchName string

	if *branchFlag != "" {
		branchName = *branchFlag
		s = mgr.FindStackForBranch(branchName)
		if s == nil {
			// Not a child branch — try matching it as a stack root.
			roots := mgr.GetStacksWithRoot(branchName)
			if len(roots) == 0 {
				return fmt.Errorf("branch %q not found in any stack", branchName)
			}
			s = roots[0]
		}
	} else {
		cs, branch, err := mgr.GetCurrentStack()
		if err != nil {
			return err
		}
		s = cs
		branchName = branch.Name
	}

	// Resolve refs through the same helper `ezs ls` uses so the diff matches
	// the line counts shown in `ezs ls`/`ezs status`. Notably: parents inside
	// the stack diff against the local ref (not origin/<parent>) so unpushed
	// commits on the parent don't leak into the child's diff.
	parentRef, branchRef, ok := resolveDiffRefs(g, s, branchName)
	if !ok {
		return fmt.Errorf("cannot determine diff base for branch %q in stack %q", branchName, s.DisplayName())
	}

	if *jsonFlag {
		return diffJSON(g, parentRef, branchRef)
	}

	diffArgs := []string{"diff", parentRef + "..." + branchRef}
	if *stat {
		diffArgs = append(diffArgs, "--stat")
	}
	diffArgs = append(diffArgs, fs.Args()...)

	return g.RunInteractive(diffArgs...)
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

func diffJSON(g *git.Git, parentRef, branchRef string) error {
	output, err := g.RunCapture("diff", "--numstat", parentRef+"..."+branchRef)
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
