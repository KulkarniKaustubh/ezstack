// Package hooks runs user-defined shell hooks from ~/.ezstack/hooks/.
//
// Exit-code contract:
//   - pre-*  hooks abort the action on non-zero exit (Run returns an error)
//   - post-* hooks only warn on non-zero exit (callers should log but not fail)
//
// Hooks are never invoked via a shell; the file at ~/.ezstack/hooks/<name>
// is executed directly, so it must have the executable bit set and include a
// shebang line if it is a script. Non-existent, non-executable, or directory
// entries are treated as "no hook installed" (a no-op).
//
// Context is passed via environment variables so hooks can make informed
// decisions without re-parsing ezstack state:
//
//	EZS_HOOK        — hook name (e.g. "pre-push")
//	EZS_REPO_ROOT   — absolute path to the repo
//	EZS_BRANCH      — current branch (if known)
//	EZS_STACK_HASH  — current stack hash (if known)
//	EZS_STACK_NAME  — current stack name (if set)
//
// Callers build a Context and pass it to Run. Missing fields are simply
// omitted from the environment.
package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
)

// Context carries optional metadata that ezstack exposes to hooks via env vars.
// All fields are optional; empty fields are not set in the environment.
type Context struct {
	RepoRoot  string
	Branch    string
	StackHash string
	StackName string
	// Extra lets callers inject additional EZS_* vars without plumbing new
	// fields through; keys are used verbatim so callers must include any
	// prefix they want.
	Extra map[string]string
}

// Run executes ~/.ezstack/hooks/<name> if it exists and is executable. A
// non-existent hook is a no-op. A non-zero exit from a hook is returned as
// an error (callers decide whether that's fatal or a warning).
//
// Hook stdout/stderr/stdin are inherited so hooks can interact with the user.
func Run(name string, ctx *Context) error {
	path, ok, err := lookup(name)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	cmd := exec.Command(path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = buildEnv(name, ctx)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hook '%s' failed: %w", name, err)
	}
	return nil
}

// Exists reports whether an executable hook with the given name is installed.
func Exists(name string) bool {
	_, ok, _ := lookup(name)
	return ok
}

// HooksDir returns the absolute path to the hooks directory. It does not
// create the directory.
func HooksDir() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hooks"), nil
}

func lookup(name string) (string, bool, error) {
	hooksDir, err := HooksDir()
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(hooksDir, name)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", false, nil
	}
	if info.Mode()&0111 == 0 {
		return "", false, nil
	}
	return path, true, nil
}

func buildEnv(name string, ctx *Context) []string {
	env := os.Environ()
	env = append(env, "EZS_HOOK="+name)
	if ctx == nil {
		return env
	}
	if ctx.RepoRoot != "" {
		env = append(env, "EZS_REPO_ROOT="+ctx.RepoRoot)
	}
	if ctx.Branch != "" {
		env = append(env, "EZS_BRANCH="+ctx.Branch)
	}
	if ctx.StackHash != "" {
		env = append(env, "EZS_STACK_HASH="+ctx.StackHash)
	}
	if ctx.StackName != "" {
		env = append(env, "EZS_STACK_NAME="+ctx.StackName)
	}
	for k, v := range ctx.Extra {
		env = append(env, k+"="+v)
	}
	return env
}
