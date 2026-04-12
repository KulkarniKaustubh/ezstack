package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/KulkarniKaustubh/ezstack/internal/git"
	"github.com/KulkarniKaustubh/ezstack/internal/stack"
	"github.com/KulkarniKaustubh/ezstack/internal/ui"
	"github.com/spf13/pflag"
)

func Log(args []string) error {
	fs := pflag.NewFlagSet("log", pflag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `%sShow commits in a branch since its parent%s

%sUSAGE%s
    ezs log [options]

%sDESCRIPTION%s
    Shows the commits in a branch that are not in its parent branch.
    Useful for seeing what work has been done on a specific branch.

%sOPTIONS%s
    -b, --branch <name>  Show log for a specific branch (default: current)
    --json               Output as JSON
    -h, --help           Show this help message
`, ui.Bold, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset, ui.Cyan, ui.Reset)
	}
	branchFlag := fs.StringP("branch", "b", "", "Show log for a specific branch")
	jsonFlag := fs.Bool("json", false, "Output as JSON")
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

	// Use origin/ for the parent when available
	parentRef := parent
	if g.RemoteBranchExists(parent) {
		parentRef = "origin/" + parent
	}

	if *jsonFlag {
		return logJSON(g, parentRef, branchName)
	}

	// Interactive: pass through to git log
	return g.RunInteractive("log", "--oneline", parentRef+".."+branchName)
}

type commitJSON struct {
	Hash    string `json:"hash"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

type logOutputJSON struct {
	Branch  string       `json:"branch"`
	Parent  string       `json:"parent"`
	Commits []commitJSON `json:"commits"`
	Count   int          `json:"count"`
}

func logJSON(g *git.Git, parentRef, branchName string) error {
	// Use record separator (ASCII 0x1E) as delimiter — won't appear in git fields
	const sep = "\x1e"
	format := "%H" + sep + "%s" + sep + "%an" + sep + "%aI"

	output, err := g.RunCapture("log", "--format="+format, parentRef+".."+branchName)
	if err != nil {
		return fmt.Errorf("git log failed: %w", err)
	}

	commits := make([]commitJSON, 0)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, sep, 4)
		if len(parts) < 4 {
			continue
		}
		commits = append(commits, commitJSON{
			Hash:    parts[0],
			Message: parts[1],
			Author:  parts[2],
			Date:    parts[3],
		})
	}

	result := logOutputJSON{
		Branch:  branchName,
		Parent:  parentRef,
		Commits: commits,
		Count:   len(commits),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
