/** Mirrors the Go stackJSON struct from cmd/ezs/commands/list.go */
export interface StackJSON {
  hash: string;
  name?: string;
  root: string;
  /** Set when the stack root is itself a tracked branch with its own PR. */
  root_base?: string;
  root_pr_number?: number;
  root_pr_url?: string;
  root_additions?: number;
  root_deletions?: number;
  branches: BranchJSON[];
  /** Public-fork stacking — true when this stack roots on the upstream
   *  default branch and fork mode is enabled for the repo. */
  is_fork_mode?: boolean;
  /** "owner/repo" of the upstream parent (only when is_fork_mode). */
  upstream_repo?: string;
  upstream_remote?: string;
  upstream_default_branch?: string;
}

/** Mirrors the Go branchJSON struct */
export interface BranchJSON {
  name: string;
  parent: string;
  is_merged: boolean;
  is_current: boolean;
  is_remote?: boolean;
  pr_number?: number;
  pr_url?: string;
  worktree_path?: string;
  /** Always emitted (no omitempty). */
  additions: number;
  deletions: number;
  /** Public-fork stacking: where this branch's PR lives. "" = origin
   *  (classic single-repo flow), "fork" = same-repo within the
   *  contributor's fork (intermediate stack PR), "upstream" = cross-repo
   *  PR in the upstream parent (bottom of the fork stack). */
  pr_target_repo?: "" | "fork" | "upstream";
  /** "owner/repo" of the repo that hosts this branch's PR — empty in the
   *  classic flow. */
  pr_target_repo_label?: string;
  /** When this branch's PR was promoted from a fork-side PR to a cross-
   *  repo PR via close-and-reopen, this is the closed fork-side PR #. */
  previous_pr_number?: number;
}

/** Mirrors the Go statusStackJSON struct (ezs status --json) */
export interface StatusStackJSON {
  hash: string;
  name?: string;
  root: string;
  root_base?: string;
  root_pr_number?: number;
  root_pr_url?: string;
  root_additions?: number;
  root_deletions?: number;
  branches: StatusBranchJSON[];
  is_fork_mode?: boolean;
  upstream_repo?: string;
  upstream_remote?: string;
  upstream_default_branch?: string;
}

/** Mirrors the Go statusBranchJSON struct — branchJSON + PR/CI fields */
export interface StatusBranchJSON extends BranchJSON {
  pr_state?: "OPEN" | "DRAFT" | "MERGED" | "CLOSED" | "";
  ci_state?: "success" | "failure" | "pending" | "none" | "";
  ci_summary?: string;
  mergeable?: "MERGEABLE" | "CONFLICTING" | "UNKNOWN" | "";
  /** "" appears for PRs without a review yet; gh also returns "COMMENTED". */
  review_state?: "APPROVED" | "CHANGES_REQUESTED" | "REVIEW_REQUIRED" | "COMMENTED" | "";
  /** Public-fork stacking: parent merged in upstream while this branch's
   *  PR is still fork-side. Run `ezs pr promote` to close-and-reopen the
   *  PR cross-repo against upstream. */
  is_promote_pending?: boolean;
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
