package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
)

// Run executes the hook at ~/.ezstack/hooks/<name> if it exists and is executable.
// Hook stdout/stderr/stdin are inherited so the hook can interact with the user.
// Non-existent hooks are a no-op. A non-zero exit from the hook is returned as an error.
func Run(name string, env map[string]string) error {
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
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
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

func lookup(name string) (string, bool, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, "hooks", name)
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
