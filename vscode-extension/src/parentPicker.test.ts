import { describe, it, expect } from "vitest";
import { buildParentPickChoices } from "./parentPicker";
import type { StackJSON } from "./types";

const stack = (overrides: Partial<StackJSON> & { hash: string }): StackJSON => ({
  hash: overrides.hash,
  name: overrides.name,
  root: overrides.root ?? "main",
  branches: overrides.branches ?? [],
});

const branch = (name: string, parent = "main") => ({
  name,
  parent,
  is_merged: false,
  is_current: false,
  additions: 0,
  deletions: 0,
});

describe("buildParentPickChoices", () => {
  it("returns empty list when there are no stacks and no git branches", () => {
    expect(buildParentPickChoices([], [])).toEqual([]);
  });

  it("orders roots first, then stack members, then unrelated git branches", () => {
    const stacks: StackJSON[] = [
      stack({
        hash: "abc1234",
        name: "feature-stack",
        root: "main",
        branches: [branch("feature-1"), branch("feature-2", "feature-1")],
      }),
    ];
    const gitBranches = ["main", "feature-1", "feature-2", "scratch"];

    const result = buildParentPickChoices(stacks, gitBranches);

    expect(result.map((c) => c.branchName)).toEqual([
      "main",
      "feature-1",
      "feature-2",
      "scratch",
    ]);
    expect(result.map((c) => c.group)).toEqual([
      "root",
      "stack-member",
      "stack-member",
      "git",
    ]);
  });

  it("deduplicates branches across groups (root wins over stack-member wins over git)", () => {
    const stacks: StackJSON[] = [
      stack({
        hash: "abc1234",
        root: "main",
        branches: [branch("main"), branch("feature-1")],
      }),
    ];
    const gitBranches = ["main", "feature-1", "feature-2"];

    const result = buildParentPickChoices(stacks, gitBranches);

    const mainEntries = result.filter((c) => c.branchName === "main");
    expect(mainEntries).toHaveLength(1);
    expect(mainEntries[0].group).toBe("root");

    const f1Entries = result.filter((c) => c.branchName === "feature-1");
    expect(f1Entries).toHaveLength(1);
    expect(f1Entries[0].group).toBe("stack-member");
  });

  it("collapses duplicate stack roots when multiple stacks share the same root", () => {
    const stacks: StackJSON[] = [
      stack({ hash: "abc1234", root: "main", branches: [branch("a")] }),
      stack({ hash: "def5678", root: "main", branches: [branch("b")] }),
      stack({ hash: "fff9999", root: "develop", branches: [branch("c", "develop")] }),
    ];

    const result = buildParentPickChoices(stacks, []);

    const roots = result.filter((c) => c.group === "root").map((c) => c.branchName);
    expect(roots).toEqual(["main", "develop"]);
  });

  it("uses stack name when present, otherwise falls back to hash", () => {
    const stacks: StackJSON[] = [
      stack({
        hash: "abc1234",
        name: "my-feature",
        root: "main",
        branches: [branch("a")],
      }),
      stack({
        hash: "def5678",
        name: undefined,
        root: "develop",
        branches: [branch("b", "develop")],
      }),
    ];

    const result = buildParentPickChoices(stacks, []);

    const aEntry = result.find((c) => c.branchName === "a");
    expect(aEntry?.stackName).toBe("my-feature");
    expect(aEntry?.description).toContain("my-feature");

    const bEntry = result.find((c) => c.branchName === "b");
    expect(bEntry?.stackName).toBe("def5678");
    expect(bEntry?.description).toContain("def5678");
  });

  it("ignores empty / falsy branch names", () => {
    const stacks: StackJSON[] = [
      stack({ hash: "abc1234", root: "", branches: [branch(""), branch("good")] }),
    ];
    const result = buildParentPickChoices(stacks, ["", "also-good"]);

    expect(result.map((c) => c.branchName)).toEqual(["good", "also-good"]);
  });

  it("description text reveals the safety distinction (new stack vs. add child)", () => {
    const stacks: StackJSON[] = [
      stack({
        hash: "abc1234",
        name: "my-stack",
        root: "main",
        branches: [branch("feature-tip")],
      }),
    ];
    const gitBranches = ["main", "feature-tip", "scratch"];

    const result = buildParentPickChoices(stacks, gitBranches);
    const byName = Object.fromEntries(result.map((c) => [c.branchName, c]));

    expect(byName["main"].description).toMatch(/new stack/i);
    expect(byName["feature-tip"].description).toMatch(/adds a child/i);
    expect(byName["scratch"].description).toMatch(/not tracked/i);
  });
});
