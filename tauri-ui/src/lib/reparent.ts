import type { StatusStack } from "../types/ezstack";

export type ReparentRejection =
  | { kind: "same-branch" }
  | { kind: "same-parent" }
  | { kind: "cycle" };

export type ReparentCheck = { ok: true } | { ok: false; reason: ReparentRejection };

function findStackContaining(stacks: StatusStack[], branchName: string): StatusStack | undefined {
  return stacks.find((s) => s.branches.some((b) => b.name === branchName));
}

function collectDescendants(stack: StatusStack, root: string): Set<string> {
  const childrenOf = new Map<string, string[]>();
  for (const b of stack.branches) {
    const siblings = childrenOf.get(b.parent) ?? [];
    siblings.push(b.name);
    childrenOf.set(b.parent, siblings);
  }
  const seen = new Set<string>();
  const queue = [root];
  while (queue.length > 0) {
    const name = queue.pop()!;
    if (seen.has(name)) continue;
    seen.add(name);
    for (const child of childrenOf.get(name) ?? []) queue.push(child);
  }
  return seen;
}

/**
 * Validate whether `branch` can be reparented onto `newParent` without
 * creating a cycle or a no-op. Cross-stack reparents are allowed and never
 * create cycles because the graphs are disjoint.
 */
export function checkReparent(
  stacks: StatusStack[],
  branch: string,
  newParent: string,
): ReparentCheck {
  if (branch === newParent) {
    return { ok: false, reason: { kind: "same-branch" } };
  }
  const stack = findStackContaining(stacks, branch);
  if (!stack) return { ok: true };

  const current = stack.branches.find((b) => b.name === branch);
  if (current && current.parent === newParent) {
    return { ok: false, reason: { kind: "same-parent" } };
  }

  const descendants = collectDescendants(stack, branch);
  if (descendants.has(newParent)) {
    return { ok: false, reason: { kind: "cycle" } };
  }
  return { ok: true };
}

export function describeRejection(reason: ReparentRejection, branch: string, newParent: string): string {
  switch (reason.kind) {
    case "same-branch":
      return `Cannot reparent ${branch} onto itself.`;
    case "same-parent":
      return `${branch} is already parented on ${newParent}.`;
    case "cycle":
      return `Cannot reparent ${branch} onto ${newParent}: ${newParent} is a descendant of ${branch}.`;
  }
}
