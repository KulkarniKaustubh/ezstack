import { describe, it, expect } from "vitest";
import { BranchNode } from "./stackTreeProvider";
import type { StatusBranchJSON, WorktreeGitStatus } from "../types";

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

  describe("public-fork stacking chips", () => {
    it("shows ↑upstream chip for cross-repo PR", () => {
      const node = new BranchNode(
        makeBranch({
          pr_number: 100,
          pr_target_repo: "upstream",
          pr_target_repo_label: "kubernetes/kubernetes",
        }),
        "abc123",
        false,
      );
      expect(node.description).toContain("[↑kubernetes/kubernetes]");
    });

    it("shows [fork] chip for fork-side intermediate PR", () => {
      const node = new BranchNode(
        makeBranch({ pr_number: 101, pr_target_repo: "fork" }),
        "abc123",
        false,
      );
      expect(node.description).toContain("[fork]");
    });

    it("shows promote-pending alert when parent merged in upstream", () => {
      const node = new BranchNode(
        makeBranch({
          pr_number: 101,
          pr_target_repo: "fork",
          is_promote_pending: true,
        }),
        "abc123",
        false,
      );
      expect(node.description).toContain("⇡ promote");
    });

    it("omits chips for classic single-repo PRs (no fork mode)", () => {
      const node = new BranchNode(
        makeBranch({ pr_number: 50, pr_state: "OPEN" }),
        "abc123",
        false,
      );
      const desc = node.description as string;
      expect(desc).not.toContain("[fork]");
      expect(desc).not.toContain("↑");
      expect(desc).not.toContain("⇡");
    });
  });
});
