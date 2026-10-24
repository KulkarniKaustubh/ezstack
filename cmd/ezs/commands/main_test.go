package commands

import (
	"os"
	"testing"
)

// TestMain points EZSTACK_HOME at a throwaway directory for the whole package
// so no test can read or write the developer's real ~/.ezstack. Tests that
// need their own home still override it with t.Setenv.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ezstack-commands-test-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("EZSTACK_HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
