/** Mirrors the Go stackJSON struct from cmd/ezs/commands/list.go */
export interface StackJSON {
  hash: string;
  name?: string;
  root: string;
  /** Set when the stack root is itself a tracked branch with its own PR. */
  root_base?: string;
  root_pr_number?: number;
  root_pr_url?: string;
  /** True when the stack root is a pickup branch (`ezs new -r`); drives the `(remote)` tag. */
  root_is_remote?: boolean;
  root_additions?: number;
  root_deletions?: number;
  branches: BranchJSON[];
}

/** Mirrors the Go branchJSON struct */
export interface BranchJSON {
  name: string;
  parent: string;
  is_merged: boolean;
  is_current: boolean;
  /** True when the branch is a pickup (`ezs new origin/<branch>`); drives the `(remote)` tag. */
  is_remote?: boolean;
  pr_number?: number;
  pr_url?: string;
  worktree_path?: string;
  /** Always emitted (no omitempty). */
  additions: number;
  deletions: number;
}

/** Mirrors the Go statusStackJSON struct (ezs status --json) */
export interface StatusStackJSON {
  hash: string;
  name?: string;
  root: string;
  root_base?: string;
  root_pr_number?: number;
  root_pr_url?: string;
  /** True when the stack root is a pickup branch (`ezs new -r`); drives the `(remote)` tag. */
  root_is_remote?: boolean;
  root_additions?: number;
  root_deletions?: number;
  branches: StatusBranchJSON[];
}

/** Mirrors the Go statusBranchJSON struct — branchJSON + PR/CI fields */
export interface StatusBranchJSON extends BranchJSON {
  pr_state?: "OPEN" | "DRAFT" | "MERGED" | "CLOSED" | "";
  ci_state?: "success" | "failure" | "pending" | "none" | "";
  ci_summary?: string;
  mergeable?: "MERGEABLE" | "CONFLICTING" | "UNKNOWN" | "";
  /** "" appears for PRs without a review yet; gh also returns "COMMENTED". */
  review_state?: "APPROVED" | "CHANGES_REQUESTED" | "REVIEW_REQUIRED" | "COMMENTED" | "";
}

/** Per-file git status indicator. */
export type FileGitState = "modified" | "staged" | "untracked" | "both" | "conflict";

/** Git working tree status for a worktree. */
export interface WorktreeGitStatus {
  modified: number;
  staged: number;
  untracked: number;
  ahead: number;
  behind: number;
  /** Map of relative file path → git state. */
  files: Map<string, FileGitState>;
}

/** Mirrors the Go syncInfoJSON struct from cmd/ezs/commands/sync.go */
export interface SyncInfoJSON {
  branch: string;
  needs_sync: boolean;
  /** Set when the parent branch has been merged into the stack root. */
  merged_parent?: string;
  /** Set when the branch is behind its parent (rebase needed). */
  behind_parent?: string;
  /** Number of commits this branch is behind its parent. */
  behind_by?: number;
  /** The branch this stack is rooted on (typically the default base). */
  stack_root: string;
}

/** Mirrors the Go diffOutputJSON struct from cmd/ezs/commands/diff.go */
export interface DiffFileJSON {
  path: string;
  additions: number;
  deletions: number;
}

export interface DiffOutputJSON {
  files: DiffFileJSON[];
  total_files: number;
  total_additions: number;
  total_deletions: number;
}

/** Mirrors the Go logOutputJSON struct from cmd/ezs/commands/log.go */
export interface CommitJSON {
  hash: string;
  message: string;
  author: string;
  date: string;
}

export interface LogOutputJSON {
  branch: string;
  parent: string;
  commits: CommitJSON[];
  count: number;
}
