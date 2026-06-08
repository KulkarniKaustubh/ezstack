package main

import (
	"fmt"
	"strings"
)

// resolveRepoOverride extracts a --repo override from args and applies the
// precedence flag > EZSTACK_REPO env > none. It returns args with every --repo
// token removed (so the downstream dispatcher and subcommand parsers never see
// them — identical to how -y/--yes is stripped), the resolved repo path ("" when
// no override applies), and a source label ("--repo" or "EZSTACK_REPO") used to
// name the source in error messages.
//
// Accepted forms: "--repo <path>" (two tokens) and "--repo=<path>" (one token).
// The last occurrence wins. A "--repo" with no following token, or an explicitly
// empty value, is a usage error.
func resolveRepoOverride(args []string, env string) (remaining []string, repoPath, source string, err error) {
	remaining = make([]string, 0, len(args))
	flagSet := false
	flagVal := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--repo":
			if i+1 >= len(args) {
				return nil, "", "", fmt.Errorf("flag needs an argument: --repo")
			}
			flagVal = args[i+1]
			flagSet = true
			i++ // consume the value token
		case strings.HasPrefix(arg, "--repo="):
			flagVal = strings.TrimPrefix(arg, "--repo=")
			flagSet = true
		default:
			remaining = append(remaining, arg)
		}
	}

	if flagSet {
		if flagVal == "" {
			return nil, "", "", fmt.Errorf("--repo requires a non-empty path")
		}
		return remaining, flagVal, "--repo", nil
	}
	if env != "" {
		return remaining, env, "EZSTACK_REPO", nil
	}
	return remaining, "", "", nil
}

// repoSourceLabel renders the override source for user-facing errors, e.g.
// "--repo /path/to/repo" or "EZSTACK_REPO=/path/to/repo".
func repoSourceLabel(source, path string) string {
	if source == "--repo" {
		return fmt.Sprintf("--repo %s", path)
	}
	return fmt.Sprintf("EZSTACK_REPO=%s", path)
}
