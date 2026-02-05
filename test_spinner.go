package main

import (
	"fmt"
	"time"

	"github.com/ezstack/ezstack/internal/ui"
)

func main() {
	fmt.Println("Testing delayed spinner...")
	fmt.Println()

	// Test 1: Fast operation (< 1.5s) - no spinner should show
	fmt.Println("Test 1: Fast operation (500ms) - no spinner should appear")
	err := ui.WithSpinner("Fast operation...", func() error {
		time.Sleep(500 * time.Millisecond)
		return nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Println("✓ Completed (no spinner shown)")
	fmt.Println()

	// Test 2: Slow operation (> 1.5s) - spinner should show
	fmt.Println("Test 2: Slow operation (3s) - spinner should appear after 1.5s")
	err = ui.WithSpinner("Slow operation...", func() error {
		time.Sleep(3 * time.Second)
		return nil
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
	fmt.Println("✓ Completed (spinner was shown)")
	fmt.Println()

	fmt.Println("All tests completed!")
}
