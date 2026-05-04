package itests

import (
	"fmt"
	"os"
	"strconv"

	"github.com/KulkarniKaustubh/ezstack/v4/internal/config"
)

// concurrentSaveHelperMain is the body executed when the test binary is
// re-invoked with EZS_CONCURRENT_SAVE_HELPER=1. It performs a fixed number
// of LoadStackConfig + add-a-stack + Save iterations against the
// EZSTACK_HOME the parent points it at, then exits 0 on success.
//
// Used by TestStackConfig_MultiProcessConcurrentSave to verify that
// flock + three-way merge survive real cross-process contention (not
// just goroutines). Goroutines in one process share heap state and miss
// the inter-process race that flock is supposed to defend against.
func concurrentSaveHelperMain() {
	repo := os.Getenv("EZS_HELPER_REPO")
	prefix := os.Getenv("EZS_HELPER_PREFIX")
	itersStr := os.Getenv("EZS_HELPER_ITERS")
	if repo == "" || prefix == "" || itersStr == "" {
		fmt.Fprintln(os.Stderr, "concurrent save helper: missing EZS_HELPER_REPO/PREFIX/ITERS")
		os.Exit(2)
	}
	iters, err := strconv.Atoi(itersStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "concurrent save helper: bad ITERS %q: %v\n", itersStr, err)
		os.Exit(2)
	}

	for i := 0; i < iters; i++ {
		sc, err := config.LoadStackConfig(repo)
		if err != nil {
			fmt.Fprintf(os.Stderr, "iter %d: load: %v\n", i, err)
			os.Exit(1)
		}
		hash := config.GenerateStackHash(fmt.Sprintf("%s-%03d", prefix, i))
		sc.Stacks[hash] = &config.Stack{
			Hash: hash,
			Root: "main",
			Tree: config.BranchTree{fmt.Sprintf("%s-%03d", prefix, i): config.BranchTree{}},
		}
		if err := sc.Save(repo); err != nil {
			fmt.Fprintf(os.Stderr, "iter %d: save: %v\n", i, err)
			os.Exit(1)
		}
	}
}
