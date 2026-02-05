// Package helpers provides shared utility functions for the ezstack CLI.
package helpers

import (
	"os"
	"path/filepath"
)

// DefaultBaseBranch is the default base branch name when not configured.
const DefaultBaseBranch = "main"

// ExpandPath expands a path, replacing ~ with the user's home directory.
func ExpandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

// MergeFlags merges short flags into long flags.
// Pass pairs of (shortFlag, longFlag) pointers: short1, long1, short2, long2, etc.
func MergeFlags(pairs ...*bool) {
	for i := 0; i+1 < len(pairs); i += 2 {
		if *pairs[i] {
			*pairs[i+1] = true
		}
	}
}
