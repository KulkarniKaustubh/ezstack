/** Mirrors the Go stackJSON struct from cmd/ezs/commands/list.go */
export interface StackJSON {
  hash: string;
  name?: string;
  root: string;
  branches: BranchJSON[];
}

/** Mirrors the Go branchJSON struct */
export interface BranchJSON {
  name: string;
  parent: string;
  is_merged: boolean;
  is_current: boolean;
  pr_number?: number;
  pr_url?: string;
  worktree_path?: string;
}

/** Mirrors the Go statusStackJSON struct (ezs status --json) */
export interface StatusStackJSON {
  hash: string;
  name?: string;
  root: string;
  branches: StatusBranchJSON[];
}

/** Mirrors the Go statusBranchJSON struct — branchJSON + PR/CI fields */
export interface StatusBranchJSON extends BranchJSON {
  pr_state?: "OPEN" | "DRAFT" | "MERGED" | "CLOSED" | "";
  ci_state?: "success" | "failure" | "pending" | "none" | "";
  ci_summary?: string;
  mergeable?: "MERGEABLE" | "CONFLICTING" | "UNKNOWN" | "";
  review_state?: "APPROVED" | "CHANGES_REQUESTED" | "REVIEW_REQUIRED" | "";
  additions?: number;
  deletions?: number;
}

/** Mirrors the Go syncInfoJSON struct from cmd/ezs/commands/sync.go */
export interface SyncInfoJSON {
  branch: string;
  needs_sync: boolean;
  reason?: string;
  parent?: string;
  behind_by?: number;
}
