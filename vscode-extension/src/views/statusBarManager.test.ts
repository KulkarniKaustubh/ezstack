import { describe, it, expect } from "vitest";
import type { StatusStackJSON } from "../types";

// Re-implement findCurrentBranch to test it (it's a private method).
function findCurrentBranch(
  stacks: StatusStackJSON[],
): {
  branchName: string;
  stack: StatusStackJSON;
  position: number;
  stackSize: number;
} | null {
  for (const stack of stacks) {
    for (let i = 0; i < stack.branches.length; i++) {
      const b = stack.branches[i];
      if (b.is_current) {
        return {
          branchName: b.name,
          stack,
          position: i,
          stackSize: stack.branches.length,
        };
      }
    }
  }
  return null;
}

describe("findCurrentBranch", () => {
  it("returns null for empty stacks", () => {
    expect(findCurrentBranch([])).toBeNull();
  });

  it("finds current branch with correct position", () => {
    const stacks: StatusStackJSON[] = [
      {
        hash: "abc",
        root: "main",
        branches: [
          { name: "feat-a", parent: "main", is_merged: false, is_current: false, additions: 0, deletions: 0 },
          { name: "feat-b", parent: "feat-a", is_merged: false, is_current: true, additions: 0, deletions: 0 },
          { name: "feat-c", parent: "feat-b", is_merged: false, is_current: false, additions: 0, deletions: 0 },
        ],
      },
    ];
    const result = findCurrentBranch(stacks);
    expect(result).not.toBeNull();
    expect(result!.branchName).toBe("feat-b");
    expect(result!.position).toBe(1);
    expect(result!.stackSize).toBe(3);
  });

  it("returns null when no branch is current", () => {
    const stacks: StatusStackJSON[] = [
      {
        hash: "abc",
        root: "main",
        branches: [
          { name: "feat-a", parent: "main", is_merged: false, is_current: false, additions: 0, deletions: 0 },
        ],
      },
    ];
    expect(findCurrentBranch(stacks)).toBeNull();
  });
});
