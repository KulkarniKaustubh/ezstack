import { describe, it, expect } from "vitest";
import { BranchNode, StackNode } from "./stackTreeProvider";
import type { StatusBranchJSON, StatusStackJSON, WorktreeGitStatus } from "../types";

function makeBranch(overrides: Partial<StatusBranchJSON> = {}): StatusBranchJSON {
  return {
    name: "feature-branch",
    parent: "main",
    is_merged: false,
    is_current: false,
    additions: 0,
    deletions: 0,
    ...overrides,
  };
}

function makeGitStatus(overrides: Partial<WorktreeGitStatus> = {}): WorktreeGitStatus {
  return {
    modified: 0,
    staged: 0,
    untracked: 0,
    ahead: 0,
    behind: 0,
    files: new Map(),
    ...overrides,
  };
}

describe("BranchNode", () => {
  describe("buildDescription", () => {
    it("shows PR number and state", () => {
      const node = new BranchNode(
        makeBranch({ pr_number: 42, pr_state: "OPEN" }),
        "abc123",
        false,
      );
      expect(node.description).toContain("PR #42");
      expect(node.description).toContain("[OPEN]");
    });

    it("shows CI summary", () => {
      const node = new BranchNode(
        makeBranch({ ci_summary: "3/3 passed" }),
        "abc123",
        false,
      );
      expect(node.description).toContain("CI: 3/3 passed");
    });

    it("shows diff stats", () => {
      const node = new BranchNode(
        makeBranch({ additions: 10, deletions: 5 }),
        "abc123",
        false,
      );
      expect(node.description).toContain("+10 -5");
    });

    it("shows git status counts", () => {
      const node = new BranchNode(
        makeBranch(),
        "abc123",
        false,
        makeGitStatus({ staged: 2, modified: 3, untracked: 1 }),
      );
      expect(node.description).toContain("+2");
      expect(node.description).toContain("~3");
      expect(node.description).toContain("?1");
    });

    it("shows ahead/behind indicators", () => {
      const node = new BranchNode(
        makeBranch(),
        "abc123",
        false,
        makeGitStatus({ ahead: 2, behind: 1 }),
      );
      const desc = node.description as string;
      expect(desc).toContain("\u21912");
      expect(desc).toContain("\u21931");
    });

    it("returns empty string for minimal branch", () => {
      const node = new BranchNode(makeBranch(), "abc123", false);
      expect(node.description).toBe("");
    });
  });

  describe("getIcon", () => {
    it("uses git-merge icon for merged branches", () => {
      const node = new BranchNode(
        makeBranch({ is_merged: true }),
        "abc123",
        false,
      );
      expect((node.iconPath as { id: string }).id).toBe("git-merge");
    });

    it("uses circle-filled for dirty worktree", () => {
      const node = new BranchNode(
        makeBranch(),
        "abc123",
        false,
        makeGitStatus({ modified: 1 }),
      );
      expect((node.iconPath as { id: string }).id).toBe("circle-filled");
    });

    it("uses circle-filled for current branch", () => {
      const node = new BranchNode(
        makeBranch({ is_current: true }),
        "abc123",
        false,
      );
      expect((node.iconPath as { id: string }).id).toBe("circle-filled");
    });

    it("uses git-branch for regular branches", () => {
      const node = new BranchNode(makeBranch(), "abc123", false);
      expect((node.iconPath as { id: string }).id).toBe("git-branch");
    });
  });

  describe("contextValue", () => {
    it("is branchWithPR when PR exists", () => {
      const node = new BranchNode(
        makeBranch({ pr_number: 1 }),
        "abc123",
        false,
      );
      expect(node.contextValue).toBe("branchWithPR");
    });

    it("is branch when no PR", () => {
      const node = new BranchNode(makeBranch(), "abc123", false);
      expect(node.contextValue).toBe("branch");
    });
  });

  // is_remote drives the (remote) tag in the CLI's `ezs ls`. Without a
  // matching tag in VS Code, the tree silently presented pickup branches
  // (`ezs new origin/<branch>`) as if they were the user's own — the
  // failure mode that motivated this fix.
  describe("(remote) tag", () => {
    it("prepends (remote) to the branch description when is_remote is true", () => {
      const node = new BranchNode(
        makeBranch({ is_remote: true, pr_number: 7 }),
        "abc123",
        false,
      );
      expect(node.description).toContain("(remote)");
      // Stays first so the tag is the leftmost cue, mirroring the CLI.
      expect(node.description?.toString().indexOf("(remote)")).toBeLessThan(
        node.description?.toString().indexOf("PR") ?? Infinity,
      );
    });

    it("does not show (remote) for user-owned branches", () => {
      const node = new BranchNode(makeBranch(), "abc123", false);
      expect(node.description).not.toContain("(remote)");
    });

    it("notes pickup status in the tooltip when is_remote is true", () => {
      const node = new BranchNode(makeBranch({ is_remote: true }), "h", false);
      const tooltip = node.tooltip as { value: string };
      expect(tooltip.value).toContain("(remote)");
    });
  });
});

describe("StackNode", () => {
  // root_is_remote is set by `ezs new -r` (pickup at the stack root). The
  // tree pane needs to surface the same `(remote)` cue the CLI shows or the
  // user can't tell their stack is anchored on someone else's PR.
  it("appends (remote) to the root description when root_is_remote is true", () => {
    const stack: StatusStackJSON = {
      hash: "abc1234",
      root: "alice/feature",
      root_is_remote: true,
      branches: [],
    };
    const node = new StackNode(stack);
    expect(node.description).toBe("root: alice/feature (remote)");
    // contextValue must stay "stack" so the menu entries gated on
    // `viewItem == stack` in package.json still show on pickup-rooted
    // stacks. The (remote) cue belongs in description + tooltip, not as
    // a capability gate.
    expect(node.contextValue).toBe("stack");
  });

  it("does not append (remote) for a user-owned stack root", () => {
    const stack: StatusStackJSON = {
      hash: "abc1234",
      root: "main",
      branches: [],
    };
    const node = new StackNode(stack);
    expect(node.description).toBe("root: main");
    expect(node.contextValue).toBe("stack");
  });
});
