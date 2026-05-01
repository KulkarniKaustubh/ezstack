package git

import (
	"os/exec"
	"strings"
)

// EnableRerere ensures `rerere.enabled` and `rerere.autoupdate` are set on the
// repository. Idempotent: it only writes when a setting is unset at the
// repo-local scope, so a user who has explicitly set `rerere.enabled = false`
// in this repo's config keeps that value untouched. A user who has set it to
// `false` in their *global* config (~/.gitconfig) WILL be overridden here —
// the repo-local true wins by git config precedence — which matches the
// intent of opt-in scoped behaviour for ezstack-managed repos.
//
// rerere ("reuse recorded resolution") records the conflict-and-resolution
// hunks the first time a conflict is resolved, then auto-applies that
// resolution on subsequent rebases that re-encounter the same textual conflict.
// In a stacked workflow this catches several edge cases the `--onto oldBase`
// fix doesn't:
//   - The user runs `git rebase --abort` and retries; rerere replays their
//     prior resolution.
//   - Two siblings touch the same hunk, so each independently re-conflicts
//     against an updated parent — rerere replays after the first manual fix.
//   - The cross-stack-parent fallback path in syncStackInternal where no
//     PreSyncCommit is available.
//
// Written at the repo level (not global) so it stays scoped to the project.
func EnableRerere(repoDir string) error {
	if repoDir == "" {
		return nil
	}
	if err := setIfUnset(repoDir, "rerere.enabled", "true"); err != nil {
		return err
	}
	return setIfUnset(repoDir, "rerere.autoupdate", "true")
}

func setIfUnset(repoDir, key, value string) error {
	getCmd := exec.Command("git", "config", "--local", "--get", key)
	getCmd.Dir = repoDir
	out, _ := getCmd.Output()
	if strings.TrimSpace(string(out)) != "" {
		return nil
	}
	setCmd := exec.Command("git", "config", "--local", key, value)
	setCmd.Dir = repoDir
	return setCmd.Run()
}
