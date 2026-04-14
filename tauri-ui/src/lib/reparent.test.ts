import { describe, it, expect } from "vitest";
import { checkReparent, describeRejection } from "./reparent";
import type { StatusStack, StatusBranch } from "../types/ezstack";

function branch(name: string, parent: string): StatusBranch {
  return { name, parent, is_merged: false, is_current: false };
}

// main
//  ├─ feat/a
//  │   └─ feat/b
//  │       └─ feat/c
//  └─ feat/x
const stackA: StatusStack = {
  hash: "aaa111",
  root: "main",
  branches: [
    branch("feat/a", "main"),
    branch("feat/b", "feat/a"),
    branch("feat/c", "feat/b"),
    branch("feat/x", "main"),
  ],
};

// develop
//  └─ chore/y
const stackB: StatusStack = {
  hash: "bbb222",
  root: "develop",
  branches: [branch("chore/y", "develop")],
};

const stacks = [stackA, stackB];

describe("checkReparent", () => {
  it("rejects reparenting a branch onto itself", () => {
    const res = checkReparent(stacks, "feat/a", "feat/a");
    expect(res.ok).toBe(false);
    if (!res.ok) expect(res.reason.kind).toBe("same-branch");
  });

  it("rejects reparenting onto the current parent", () => {
    const res = checkReparent(stacks, "feat/b", "feat/a");
    expect(res.ok).toBe(false);
    if (!res.ok) expect(res.reason.kind).toBe("same-parent");
  });

  it("rejects reparenting onto a direct child (would create a cycle)", () => {
    const res = checkReparent(stacks, "feat/a", "feat/b");
    expect(res.ok).toBe(false);
    if (!res.ok) expect(res.reason.kind).toBe("cycle");
  });

  it("rejects reparenting onto a transitive descendant", () => {
    const res = checkReparent(stacks, "feat/a", "feat/c");
    expect(res.ok).toBe(false);
    if (!res.ok) expect(res.reason.kind).toBe("cycle");
  });

  it("allows reparenting onto a sibling in the same stack", () => {
    expect(checkReparent(stacks, "feat/b", "feat/x").ok).toBe(true);
  });

  it("allows reparenting onto the stack root", () => {
    expect(checkReparent(stacks, "feat/b", "main").ok).toBe(true);
  });

  it("allows reparenting across stacks (graphs are disjoint)", () => {
    expect(checkReparent(stacks, "feat/a", "chore/y").ok).toBe(true);
  });

  it("treats an unknown branch as allowed (nothing to cycle against)", () => {
    expect(checkReparent(stacks, "unknown", "main").ok).toBe(true);
  });
});

describe("describeRejection", () => {
  it("produces a human-readable cycle message", () => {
    const msg = describeRejection({ kind: "cycle" }, "feat/a", "feat/c");
    expect(msg).toContain("feat/a");
    expect(msg).toContain("feat/c");
    expect(msg.toLowerCase()).toContain("descendant");
  });

  it("produces distinct messages per rejection kind", () => {
    const a = describeRejection({ kind: "same-branch" }, "x", "x");
    const b = describeRejection({ kind: "same-parent" }, "x", "y");
    const c = describeRejection({ kind: "cycle" }, "x", "y");
    expect(new Set([a, b, c]).size).toBe(3);
  });
});
