export interface Branch {
  name: string;
  parent: string;
  is_merged: boolean;
  is_current: boolean;
  pr_number?: number;
  pr_url?: string;
  worktree_path?: string;
}

export interface StatusBranch extends Branch {
  pr_state?: "OPEN" | "DRAFT" | "MERGED" | "CLOSED";
  ci_state?: "success" | "failure" | "pending" | "error" | "none" | "unknown";
  ci_summary?: string;
  mergeable?: "MERGEABLE" | "CONFLICTING" | "UNKNOWN";
  review_state?: "APPROVED" | "CHANGES_REQUESTED" | "REVIEW_REQUIRED";
}

export interface StatusStack {
  hash: string;
  name?: string;
  root: string;
  branches: StatusBranch[];
}

export interface RepoConfig {
  repo_path: string;
  worktree_base_dir: string;
  default_base_branch?: string;
  sync_strategy?: string;
}

export interface CommandResult {
  stdout: string;
  stderr: string;
  exit_code: number;
}

export interface SshConnection {
  host: string;
  user: string;
  port: number;
  key_path: string;
  remote_repo_path: string;
}

export interface TreeNode {
  branch: StatusBranch;
  children: TreeNode[];
  depth: number;
}

export function buildTree(stack: StatusStack): TreeNode[] {
  const childrenMap = new Map<string, StatusBranch[]>();
  for (const b of stack.branches) {
    const siblings = childrenMap.get(b.parent) || [];
    siblings.push(b);
    childrenMap.set(b.parent, siblings);
  }

  const visited = new Set<string>();

  function buildNode(branchName: string, depth: number): TreeNode[] {
    if (visited.has(branchName)) return [];
    visited.add(branchName);

    const children = childrenMap.get(branchName) || [];
    return children.map((b) => ({
      branch: b,
      children: buildNode(b.name, depth + 1),
      depth,
    }));
  }

  return buildNode(stack.root, 0);
}
