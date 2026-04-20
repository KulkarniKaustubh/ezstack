import { describe, it, expect } from "vitest";
import { buildParentPickChoices } from "./parent-picker";
import type { StatusStack, StatusBranch } from "../types/ezstack";

const branch = (name: string, parent = "main"): StatusBranch => ({
  name,
  parent,
  is_merged: false,
  is_current: false,
});

const stack = (
  overrides: Partial<StatusStack> & { hash: string },
): StatusStack => ({
  hash: overrides.hash,
  name: overrides.name,
  root: overrides.root ?? "main",
  branches: overrides.branches ?? [],
});

describe("buildParentPickChoices (tauri)", () => {
  it("returns empty list for no stacks", () => {
    expect(buildParentPickChoices([])).toEqual([]);
  });

  it("orders roots first, then stack members", () => {
    const result = buildParentPickChoices([
      stack({
        hash: "abc1234",
        name: "feat",
        root: "main",
        branches: [branch("feature-1"), branch("feature-2", "feature-1")],
      }),
    ]);

    expect(result.map((c) => c.branchName)).toEqual([
      "main",
      "feature-1",
      "feature-2",
    ]);
    expect(result.map((c) => c.group)).toEqual([
      "root",
      "stack-member",
      "stack-member",
    ]);
  });

  it("collapses duplicate roots when stacks share one", () => {
    const result = buildParentPickChoices([
      stack({ hash: "a", root: "main", branches: [branch("a")] }),
      stack({ hash: "b", root: "main", branches: [branch("b")] }),
      stack({ hash: "c", root: "develop", branches: [branch("c", "develop")] }),
    ]);

    const roots = result.filter((c) => c.group === "root").map((c) => c.branchName);
    expect(roots).toEqual(["main", "develop"]);
  });

  it("never lists a branch twice (root wins over stack member)", () => {
    const result = buildParentPickChoices([
      stack({
        hash: "a",
        root: "main",
        branches: [branch("main"), branch("feature-1")],
      }),
    ]);

    const names = result.map((c) => c.branchName);
    expect(new Set(names).size).toBe(names.length);
    expect(result.find((c) => c.branchName === "main")?.group).toBe("root");
  });

  it("falls back to hash when stack name is missing", () => {
    const result = buildParentPickChoices([
      stack({ hash: "abc1234", root: "main", branches: [branch("a")] }),
    ]);
    const a = result.find((c) => c.branchName === "a");
    expect(a?.stackName).toBe("abc1234");
    expect(a?.description).toContain("abc1234");
  });

  it("description text reveals the safety distinction", () => {
    const result = buildParentPickChoices([
      stack({
        hash: "abc1234",
        name: "my-stack",
        root: "main",
        branches: [branch("feature-tip")],
      }),
    ]);
    const byName = Object.fromEntries(result.map((c) => [c.branchName, c]));
    expect(byName["main"].description).toMatch(/new stack/i);
    expect(byName["feature-tip"].description).toMatch(/adds child/i);
  });

  it("ignores empty branch / root names", () => {
    const result = buildParentPickChoices([
      stack({ hash: "a", root: "", branches: [branch(""), branch("good")] }),
    ]);
    expect(result.map((c) => c.branchName)).toEqual(["good"]);
  });
});
