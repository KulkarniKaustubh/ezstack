package commands

import (
	"fmt"
	"strings"
	"testing"
)

func TestPushToRemotes_AllSucceed(t *testing.T) {
	var seen []string
	fn := func(remote string) error {
		seen = append(seen, remote)
		return nil
	}
	err := pushToRemotes([]string{"origin", "fork"}, fn)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(seen) != 2 || seen[0] != "origin" || seen[1] != "fork" {
		t.Errorf("remotes visited = %v, want [origin fork] in order", seen)
	}
}

func TestPushToRemotes_AggregateError(t *testing.T) {
	var seen []string
	fn := func(remote string) error {
		seen = append(seen, remote)
		if remote == "fork" || remote == "backup" {
			return fmt.Errorf("boom %s", remote)
		}
		return nil
	}
	err := pushToRemotes([]string{"origin", "fork", "backup"}, fn)
	if err == nil {
		t.Fatal("expected aggregate error, got nil")
	}
	// Every remote is attempted even when earlier ones fail.
	if len(seen) != 3 {
		t.Errorf("visited %d remotes, want 3 (no short-circuit)", len(seen))
	}
	msg := err.Error()
	if !strings.Contains(msg, "2") || !strings.Contains(msg, "3") {
		t.Errorf("error message %q should mention 2 of 3", msg)
	}
}

func TestPushToRemotes_EmptyList(t *testing.T) {
	called := false
	err := pushToRemotes(nil, func(string) error { called = true; return nil })
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if called {
		t.Error("fn should not be called for empty remote list")
	}
}
