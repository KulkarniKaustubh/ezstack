import type { StatusStack } from "../types/ezstack";

/** A single entry in the "select parent branch" dropdown. */
export interface ParentPickChoice {
  /** The branch name to submit when the user selects this option. */
  branchName: string;
  /** Right-aligned hint that explains what picking this branch does. */
  description: string;
  /** Bucket the choice belongs to — drives ordering and (optionally) optgroups. */
  group: ParentPickGroup;
  /** When `group` is "stack-member", the display name of the owning stack. */
  stackName?: string;
}

export type ParentPickGroup = "root" | "stack-member";

/**
 * Build the labeled options for the "select parent branch" dropdown.
 *
 * A flat list of branch names lets a user accidentally pick a stack tip when
 * they meant to start a fresh stack from main — the new branch then silently
 * lands inside the existing stack. The descriptions disambiguate:
 *
 *   - `root` entries (stack roots, e.g. `main`) → picking creates a NEW stack.
 *   - `stack-member` entries → picking ADDS a child to that stack.
 *
 * Order: roots first (most common new-stack target), then stack members
 * grouped by stack. Branch names are deduplicated; if the same branch shows
 * up as both a root and a stack member, the root entry wins.
 */
export function buildParentPickChoices(
  stacks: StatusStack[],
): ParentPickChoice[] {
  const choices: ParentPickChoice[] = [];
  const seen = new Set<string>();

  const add = (choice: ParentPickChoice) => {
    if (!choice.branchName || seen.has(choice.branchName)) return;
    seen.add(choice.branchName);
    choices.push(choice);
  };

  // Stack roots first.
  const seenRoots = new Set<string>();
  for (const s of stacks) {
    if (!s.root || seenRoots.has(s.root)) continue;
    seenRoots.add(s.root);
    add({
      branchName: s.root,
      description: "stack root — creates a NEW stack",
      group: "root",
    });
  }

  // Stack member branches.
  for (const s of stacks) {
    const stackName = s.name && s.name !== "" ? s.name : s.hash;
    for (const b of s.branches) {
      add({
        branchName: b.name,
        description: `in stack '${stackName}' — adds child to this stack`,
        group: "stack-member",
        stackName,
      });
    }
  }

  return choices;
}
