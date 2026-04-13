package commands

import (
	"testing"

	"github.com/KulkarniKaustubh/ezstack/internal/config"
)

func TestTopoOrderStackBranches_Linear(t *testing.T) {
	// main -> A -> B -> C, but Branches stored in reverse order (child first).
	// Topological order must emit A, B, C regardless of input order.
	s := &config.Stack{
		Root: "A",
		Branches: []*config.Branch{
			{Name: "C", Parent: "B"},
			{Name: "B", Parent: "A"},
			{Name: "A", Parent: "main"},
		},
	}
	got := topoOrderStackBranches(s)
	wantOrder := []string{"A", "B", "C"}
	if len(got) != len(wantOrder) {
		t.Fatalf("len = %d, want %d", len(got), len(wantOrder))
	}
	for i, b := range got {
		if b.Name != wantOrder[i] {
			t.Errorf("position %d = %q, want %q (full = %v)", i, b.Name, wantOrder[i], names(got))
		}
	}
}

func TestTopoOrderStackBranches_Branched(t *testing.T) {
	// main -> A -> B1
	//          \-> B2 -> C
	// Any valid topo order is accepted as long as parents come before children.
	s := &config.Stack{
		Root: "A",
		Branches: []*config.Branch{
			{Name: "C", Parent: "B2"},
			{Name: "B1", Parent: "A"},
			{Name: "B2", Parent: "A"},
			{Name: "A", Parent: "main"},
		},
	}
	got := topoOrderStackBranches(s)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	pos := make(map[string]int)
	for i, b := range got {
		pos[b.Name] = i
	}
	if pos["A"] > pos["B1"] || pos["A"] > pos["B2"] {
		t.Errorf("A must precede B1 and B2: %v", names(got))
	}
	if pos["B2"] > pos["C"] {
		t.Errorf("B2 must precede C: %v", names(got))
	}
}

func TestTopoOrderStackBranches_Empty(t *testing.T) {
	if got := topoOrderStackBranches(nil); got != nil {
		t.Errorf("nil input = %v, want nil", got)
	}
	s := &config.Stack{}
	if got := topoOrderStackBranches(s); got != nil {
		t.Errorf("empty stack = %v, want nil", got)
	}
}

func TestTopoOrderStackBranches_ParentOutsideStack(t *testing.T) {
	// Root's parent is "main" which isn't in the stack. Root should still emit.
	s := &config.Stack{
		Root: "A",
		Branches: []*config.Branch{
			{Name: "A", Parent: "main"},
			{Name: "B", Parent: "A"},
		},
	}
	got := topoOrderStackBranches(s)
	if len(got) != 2 || got[0].Name != "A" || got[1].Name != "B" {
		t.Errorf("got %v, want [A B]", names(got))
	}
}

func names(bs []*config.Branch) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Name
	}
	return out
}
