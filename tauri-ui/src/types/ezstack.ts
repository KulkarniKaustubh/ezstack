export interface Branch {
  name: string;
  parent: string;
  is_merged: boolean;
  is_current: boolean;
  pr_number?: number;
  pr_url?: string;
  worktree_path?: string;
  // Per-branch line deltas relative to parent. The CLI started emitting
  // these in v4.7; the Rust types.rs::Branch struct mirrors them, and the
  // frontend reads them on the StatusBranch subclass via inheritance.
  additions?: number;
  deletions?: number;
}

export interface StatusBranch extends Branch {
  pr_state?: "OPEN" | "DRAFT" | "MERGED" | "CLOSED";
  ci_state?: "success" | "failure" | "pending" | "error" | "none" | "unknown";
  ci_summary?: string;
  mergeable?: "MERGEABLE" | "CONFLICTING" | "UNKNOWN";
  review_state?: "APPROVED" | "CHANGES_REQUESTED" | "REVIEW_REQUIRED";
  // additions/deletions are inherited from Branch above.
}

// Stack root metadata. The CLI emits these so the desktop can display the
// PR status and line counts of the root branch (typically the one that
// targets `main`) without recomputing from `branches`. They are optional
// because old CLIs (< v4.7) don't emit them and the frontend must still
// render those payloads.
export interface StackRootInfo {
  root_base?: string;
  root_pr_number?: number;
  root_pr_url?: string;
  root_additions?: number;
  root_deletions?: number;
}

export interface StatusStack extends StackRootInfo {
  hash: string;
  name?: string;
  root: string;
  branches: StatusBranch[];
}

// Mirrors Rust types.rs::Stack — the non-status variant the CLI emits for
// commands that don't enrich with PR/CI state. The desktop primarily uses
// StatusStack but having Stack defined keeps the schemas symmetric (and
// type-checked) so future commands that return plain Stack don't drift.
export interface Stack extends StackRootInfo {
  hash: string;
  name?: string;
  root: string;
  branches: Branch[];
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
  jump_host: string;
  remote_repo_path: string;
  label: string;
}

export interface ConnectionProfile {
  id: string;
  name: string;
  host: string;
  user: string;
  port: number;
  key_path: string;
  jump_host: string;
  last_repo_path: string;
}

export interface RemoteRepoSummary {
  repo_path: string;
  worktree_base_dir?: string;
  default_base_branch?: string;
  exists: boolean;
}

export type DiagnosticStatus = "ok" | "warn" | "fail" | "skip";

export interface DiagnosticStep {
  name: string;
  status: DiagnosticStatus;
  message: string;
  duration_ms?: number;
}

export interface ConnectionHealth {
  ok: boolean;
  latency_ms: number;
  message?: string;
}

export interface HostFingerprint {
  key_type: string;
  bits: number;
  fingerprint: string;
}

// Mirrors the CLI's `diffOutputJSON` (cmd/ezs/commands/diff.go) — the
// JSON shape returned by `ezs diff --json`. The Rust diff_branch handler
// passes this through verbatim as a string in CommandResult.stdout, so
// the frontend parses on demand.
export interface DiffFile {
  path: string;
  additions: number;
  deletions: number;
}

export interface DiffOutput {
  files: DiffFile[];
  total_files: number;
  total_additions: number;
  total_deletions: number;
}

// Mirrors the CLI's `logOutputJSON` (cmd/ezs/commands/log.go).
export interface CommitEntry {
  hash: string;
  message: string;
  author: string;
  date: string;
}

export interface LogOutput {
  branch: string;
  parent: string;
  commits: CommitEntry[];
  count: number;
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
