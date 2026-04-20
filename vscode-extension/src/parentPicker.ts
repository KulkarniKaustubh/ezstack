import { StackJSON } from "./types";

/** A single entry in the "select parent branch" picker. */
export interface ParentPickChoice {
  /** The branch name shown as the primary label. */
  branchName: string;
  /**
   * Right-aligned hint that tells the user what picking this branch *does*:
   * create a new stack vs. add a child to an existing stack vs. unrelated git branch.
   */
  description: string;
  /** "root" / "stack-member" / "git" — used to drive separators or icons. */
  group: ParentPickGroup;
  /** When `group` is "stack-member", the display name of the owning stack. */
  stackName?: string;
}

export type ParentPickGroup = "root" | "stack-member" | "git";

/**
 * Build the labeled options for the "select parent branch" picker.
 *
 * Without labels, a flat list of branch names lets a user accidentally pick a
 * stack tip when they meant to start a fresh stack from main — the new branch
 * silently lands inside the existing stack. The descriptions disambiguate:
 *
 *   - `root` entries (stack roots, e.g. `main`) → picking creates a NEW stack.
 *   - `stack-member` entries → picking ADDS a child to that stack.
 *   - `git` entries → plain local branches not tracked by any stack.
 *
 * Order: roots first (most common new-stack target), then stack members
 * grouped by stack, then untracked git branches.
 *
 * Branch names are deduplicated across groups: a branch that's both a stack
 * root and a local git branch only appears once, in the "root" group.
 */
export function buildParentPickChoices(
  stacks: StackJSON[],
  gitBranches: string[],
): ParentPickChoice[] {
  const choices: ParentPickChoice[] = [];
  const seen = new Set<string>();

  const add = (choice: ParentPickChoice) => {
    if (!choice.branchName || seen.has(choice.branchName)) return;
    seen.add(choice.branchName);
    choices.push(choice);
  };

  // Stack roots — picking these creates a new stack from that base.
  const seenRoots = new Set<string>();
  for (const s of stacks) {
    if (!s.root || seenRoots.has(s.root)) continue;
    seenRoots.add(s.root);
    add({
      branchName: s.root,
      description: "stack root — creates a NEW stack from this branch",
      group: "root",
    });
  }

  // Stack member branches — picking these adds a child to that stack.
  for (const s of stacks) {
    const stackName = s.name && s.name !== "" ? s.name : s.hash;
    for (const b of s.branches) {
      add({
        branchName: b.name,
        description: `in stack '${stackName}' — adds a child to this stack`,
        group: "stack-member",
        stackName,
      });
    }
  }

  // Other local git branches — neither a stack root nor a tracked branch.
  for (const branch of gitBranches) {
    add({
      branchName: branch,
      description: "local git branch (not tracked by any stack)",
      group: "git",
    });
  }

  return choices;
}
