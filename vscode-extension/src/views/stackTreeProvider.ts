import * as vscode from "vscode";
import * as path from "path";
import { EzsCli } from "../ezsCli";
import { StatusStackJSON, StatusBranchJSON, WorktreeGitStatus } from "../types";

export type StackTreeItem = RootRepoNode | StackNode | BranchNode;

export class RootRepoNode extends vscode.TreeItem {
  constructor(public readonly repoPath: string) {
    super(path.basename(repoPath), vscode.TreeItemCollapsibleState.None);
    this.description = "main";
    this.iconPath = new vscode.ThemeIcon("repo");
    this.contextValue = "rootRepo";
    this.command = {
      command: "ezstack.selectBranch",
      title: "Select Root Repo",
      arguments: [repoPath],
    };
  }
}

export class StackNode extends vscode.TreeItem {
  constructor(public readonly stack: StatusStackJSON) {
    super(
      stack.name || `Stack ${stack.hash.slice(0, 7)}`,
      vscode.TreeItemCollapsibleState.Expanded,
    );
    // Mirror the CLI: a stack rooted on a pickup branch (`ezs new -r`) shows
    // `(remote)` next to the root so the tree explains the same context as
    // `ezs ls`. Without this, VS Code users couldn't tell their stack was
    // anchored on someone else's PR vs. their own branch.
    const rootLabel = stack.root_is_remote
      ? `root: ${stack.root} (remote)`
      : `root: ${stack.root}`;
    const descParts = [rootLabel];
    // Public-fork stacking: surface the upstream parent so the user can
    // see at a glance that this stack contributes cross-repo.
    if (stack.is_fork_mode && stack.upstream_repo) {
      descParts.push(`fork → ${stack.upstream_repo}`);
    }
    this.description = descParts.join("  ·  ");
    this.iconPath = new vscode.ThemeIcon("layers");
    // contextValue is "stackFork" for fork-mode stacks (fork-specific menu
    // entries key off it); otherwise "stack" — including pickup-rooted
    // stacks (root_is_remote), where (remote) is a label, not a capability
    // gate, so menu entries gated on `viewItem == stack` still appear.
    this.contextValue = stack.is_fork_mode ? "stackFork" : "stack";
    if (stack.is_fork_mode && stack.upstream_repo) {
      const md = new vscode.MarkdownString();
      md.appendMarkdown(`**Fork mode** — bottom PR cross-repo to **${stack.upstream_repo}**, intermediates in your fork.\n\n`);
      md.appendMarkdown(`Run \`ezs pr promote\` after the bottom PR merges to advance the chain.`);
      this.tooltip = md;
    }
  }
}

export class BranchNode extends vscode.TreeItem {
  constructor(
    public readonly branch: StatusBranchJSON,
    public readonly stackHash: string,
    hasChildren: boolean,
    gitStatus?: WorktreeGitStatus,
  ) {
    super(
      branch.name,
      hasChildren
        ? vscode.TreeItemCollapsibleState.Expanded
        : vscode.TreeItemCollapsibleState.None,
    );

    this.description = BranchNode.buildDescription(branch, gitStatus);
    this.iconPath = BranchNode.getIcon(branch, gitStatus);
    this.contextValue = branch.pr_number ? "branchWithPR" : "branch";

    this.tooltip = BranchNode.buildTooltip(branch, gitStatus);

    if (branch.worktree_path) {
      this.command = {
        command: "ezstack.selectBranch",
        title: "Select Branch",
        arguments: [branch.worktree_path],
      };
    }
  }

  private static buildDescription(
    b: StatusBranchJSON,
    git?: WorktreeGitStatus,
  ): string {
    const parts: string[] = [];
    // Mirror the CLI's `(remote)` tag for pickup branches so the user can
    // tell at a glance which branches are theirs vs. another contributor's
    // — bulk sync skips IsRemote branches and a force-push to one would
    // clobber the contributor's history, so the visual cue is load-bearing.
    if (b.is_remote) {
      parts.push("(remote)");
    }
    if (b.pr_number) {
      parts.push(`PR #${b.pr_number}`);
    }
    if (b.pr_state) {
      parts.push(`[${b.pr_state}]`);
    }
    // Public-fork stacking chip: render right after the PR # so reviewers
    // see "PR #123 [↑org/repo]" for cross-repo and "[fork]" for fork-side
    // intermediates. Promote-pending alert sits next to it for urgency.
    if (b.pr_target_repo === "upstream") {
      parts.push(`[↑${b.pr_target_repo_label || "upstream"}]`);
    } else if (b.pr_target_repo === "fork") {
      parts.push("[fork]");
    }
    if (b.is_promote_pending) {
      parts.push("⇡ promote");
    }
    if (b.ci_summary) {
      parts.push(`CI: ${b.ci_summary}`);
    } else if (b.ci_state && b.ci_state !== "none") {
      parts.push(`CI: ${b.ci_state}`);
    }
    if (b.review_state) {
      parts.push(b.review_state.replace(/_/g, " "));
    }
    if (b.additions || b.deletions) {
      parts.push(`+${b.additions ?? 0} -${b.deletions ?? 0}`);
    }

    if (git) {
      const gitParts: string[] = [];
      if (git.staged > 0) {
        gitParts.push(`+${git.staged}`);
      }
      if (git.modified > 0) {
        gitParts.push(`~${git.modified}`);
      }
      if (git.untracked > 0) {
        gitParts.push(`?${git.untracked}`);
      }
      if (gitParts.length > 0) {
        parts.push(gitParts.join(" "));
      }
      if (git.ahead > 0 || git.behind > 0) {
        const sync: string[] = [];
        if (git.ahead > 0) {
          sync.push(`↑${git.ahead}`);
        }
        if (git.behind > 0) {
          sync.push(`↓${git.behind}`);
        }
        parts.push(sync.join(" "));
      }
    }

    return parts.join("  ");
  }

  private static getIcon(
    b: StatusBranchJSON,
    git?: WorktreeGitStatus,
  ): vscode.ThemeIcon {
    if (b.is_merged) {
      return new vscode.ThemeIcon(
        "git-merge",
        new vscode.ThemeColor("gitDecoration.deletedResourceForeground"),
      );
    }
    if (git && (git.modified > 0 || git.staged > 0 || git.untracked > 0)) {
      return new vscode.ThemeIcon(
        "circle-filled",
        new vscode.ThemeColor("gitDecoration.modifiedResourceForeground"),
      );
    }
    if (b.is_current) {
      return new vscode.ThemeIcon(
        "circle-filled",
        new vscode.ThemeColor("gitDecoration.addedResourceForeground"),
      );
    }
    return new vscode.ThemeIcon("git-branch");
  }

  private static buildTooltip(
    b: StatusBranchJSON,
    git?: WorktreeGitStatus,
  ): vscode.MarkdownString {
    const md = new vscode.MarkdownString();
    md.appendMarkdown(`**${b.name}**${b.is_remote ? " *(remote)*" : ""}\n\n`);
    md.appendMarkdown(`Parent: \`${b.parent}\`\n\n`);
    if (b.is_remote) {
      md.appendMarkdown(
        "*Pickup branch — belongs to another contributor. Excluded from " +
          "bulk sync (`ezs sync -a`) by default.*\n\n",
      );
    }
    if (b.pr_number && b.pr_url) {
      md.appendMarkdown(`PR: [#${b.pr_number}](${b.pr_url})`);
      if (b.pr_state) {
        md.appendMarkdown(` (${b.pr_state})`);
      }
      md.appendMarkdown("\n\n");
      if (b.pr_target_repo === "upstream" && b.pr_target_repo_label) {
        md.appendMarkdown(`Cross-repo PR in **${b.pr_target_repo_label}**\n\n`);
      } else if (b.pr_target_repo === "fork" && b.pr_target_repo_label) {
        md.appendMarkdown(`Fork-side PR in **${b.pr_target_repo_label}**\n\n`);
      }
      if (b.previous_pr_number) {
        md.appendMarkdown(`Replaces #${b.previous_pr_number}\n\n`);
      }
      if (b.is_promote_pending) {
        md.appendMarkdown(`**Promotion pending** — run \`ezs pr promote\` to close-and-reopen this PR cross-repo against upstream.\n\n`);
      }
    }
    if (b.ci_state && b.ci_state !== "none") {
      md.appendMarkdown(`CI: ${b.ci_state}`);
      if (b.ci_summary) {
        md.appendMarkdown(` — ${b.ci_summary}`);
      }
      md.appendMarkdown("\n\n");
    }
    if (b.review_state) {
      md.appendMarkdown(`Review: ${b.review_state.replace(/_/g, " ")}\n\n`);
    }
    if (b.additions || b.deletions) {
      md.appendMarkdown(
        `Code Changes: +${b.additions ?? 0} / -${b.deletions ?? 0} lines\n\n`,
      );
    }
    if (b.mergeable) {
      md.appendMarkdown(`Mergeable: ${b.mergeable}\n\n`);
    }

    if (git) {
      const lines: string[] = [];
      if (git.staged > 0) {
        lines.push(`Staged: ${git.staged} file${git.staged !== 1 ? "s" : ""}`);
      }
      if (git.modified > 0) {
        lines.push(`Modified: ${git.modified} file${git.modified !== 1 ? "s" : ""}`);
      }
      if (git.untracked > 0) {
        lines.push(`Untracked: ${git.untracked} file${git.untracked !== 1 ? "s" : ""}`);
      }
      if (git.ahead > 0) {
        lines.push(`${git.ahead} commit${git.ahead !== 1 ? "s" : ""} ahead of remote`);
      }
      if (git.behind > 0) {
        lines.push(`${git.behind} commit${git.behind !== 1 ? "s" : ""} behind remote`);
      }
      if (lines.length > 0) {
        md.appendMarkdown(`---\n\n**Git Status:**\n\n`);
        for (const line of lines) {
          md.appendMarkdown(`- ${line}\n`);
        }
        md.appendMarkdown("\n");
      } else if (!b.is_merged) {
        md.appendMarkdown(`---\n\n*Working tree clean*\n\n`);
      }
    }

    if (b.worktree_path) {
      md.appendMarkdown(`Worktree: \`${b.worktree_path}\`\n\n`);
    }
    if (b.is_merged) {
      md.appendMarkdown("*Merged*\n\n");
    }
    md.isTrusted = true;
    return md;
  }
}

export class StackTreeProvider
  implements vscode.TreeDataProvider<StackTreeItem>
{
  private _onDidChangeTreeData = new vscode.EventEmitter<
    StackTreeItem | undefined | void
  >();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  private stacks: StatusStackJSON[] = [];
  private childrenMap = new Map<string, StatusBranchJSON[]>();
  private gitStatusMap = new Map<string, WorktreeGitStatus>();
  private parentMap = new Map<string, StackTreeItem>();
  private nodeCache = new Map<string, StackTreeItem>();
  private fetchSeq = 0;

  constructor(private cli: EzsCli) {}

  refresh(): void {
    this._onDidChangeTreeData.fire();
  }

  getStacks(): StatusStackJSON[] {
    return this.stacks;
  }

  getGitStatus(worktreePath: string): WorktreeGitStatus | undefined {
    return this.gitStatusMap.get(worktreePath);
  }

  private childrenKey(stackHash: string, parentName: string): string {
    return `${stackHash}:${parentName}`;
  }

  private nodeKey(element: StackTreeItem): string {
    if (element instanceof RootRepoNode) {
      return `root:${element.repoPath}`;
    }
    if (element instanceof StackNode) {
      return `stack:${element.stack.hash}`;
    }
    return `branch:${element.stackHash}:${element.branch.name}`;
  }

  async fetchData(): Promise<void> {
    const seq = ++this.fetchSeq;

    let fetched: StatusStackJSON[];
    try {
      fetched = await this.cli.statusStacks(true);
    } catch {
      try {
        // Fallback: basic listStacks lacks PR/CI status fields, but
        // is_merged and worktree_path are already on BranchJSON — preserve
        // them so merged branches don't render as not-merged just because
        // gh is unavailable.
        const basic = await this.cli.listStacks(true);
        fetched = basic.map((s) => ({
          ...s,
          branches: s.branches.map((b) => ({
            ...b,
            worktree_path: b.worktree_path ?? "",
          })),
        })) as unknown as StatusStackJSON[];
      } catch {
        fetched = [];
      }
    }

    // If a newer fetch was started while we were awaiting, abandon this one
    // BEFORE mutating any shared state.
    if (seq !== this.fetchSeq) {
      return;
    }

    this.stacks = fetched;
    this.childrenMap.clear();
    this.parentMap.clear();
    this.nodeCache.clear();
    for (const stack of this.stacks) {
      for (const b of stack.branches) {
        const key = this.childrenKey(stack.hash, b.parent);
        const siblings = this.childrenMap.get(key) ?? [];
        siblings.push(b);
        this.childrenMap.set(key, siblings);
      }
    }

    this.gitStatusMap.clear();
    const allBranches = this.stacks.flatMap((s) => s.branches);
    const newStatusMap = new Map<string, WorktreeGitStatus>();
    const statusPromises = allBranches
      .filter((b) => b.worktree_path && !b.is_merged)
      .map(async (b) => {
        try {
          const status = await this.cli.getWorktreeGitStatus(b.worktree_path!);
          newStatusMap.set(b.worktree_path!, status);
        } catch {
          // Individual git status failures are non-fatal
        }
      });
    await Promise.all(statusPromises);

    // Only apply results if this is still the latest fetch
    if (seq === this.fetchSeq) {
      this.gitStatusMap = newStatusMap;
    }
  }

  getTreeItem(element: StackTreeItem): vscode.TreeItem {
    return element;
  }

  private makeBranchNode(b: StatusBranchJSON, hash: string): BranchNode {
    const hasChildren =
      (this.childrenMap.get(this.childrenKey(hash, b.name)) ?? []).length > 0;
    const gitStatus = b.worktree_path
      ? this.gitStatusMap.get(b.worktree_path)
      : undefined;
    return new BranchNode(b, hash, hasChildren, gitStatus);
  }

  async getChildren(element?: StackTreeItem): Promise<StackTreeItem[]> {
    if (!element) {
      await this.fetchData();
      const items: StackTreeItem[] = [];
      const repoRoot = this.cli.getWorkspaceRoot();
      if (repoRoot) {
        const rootNode = new RootRepoNode(repoRoot);
        items.push(rootNode);
        this.nodeCache.set(this.nodeKey(rootNode), rootNode);
      }
      const stackNodes = this.stacks.map((s) => new StackNode(s));
      for (const n of stackNodes) {
        this.nodeCache.set(this.nodeKey(n), n);
      }
      items.push(...stackNodes);
      return items;
    }

    if (element instanceof StackNode) {
      const hash = element.stack.hash;
      const key = this.childrenKey(hash, element.stack.root);
      const topBranches = this.childrenMap.get(key) ?? [];
      const nodes = topBranches.map((b) => this.makeBranchNode(b, hash));
      for (const n of nodes) {
        const nk = this.nodeKey(n);
        this.nodeCache.set(nk, n);
        this.parentMap.set(nk, element);
      }
      return nodes;
    }

    if (element instanceof BranchNode) {
      const hash = element.stackHash;
      const key = this.childrenKey(hash, element.branch.name);
      const children = this.childrenMap.get(key) ?? [];
      const nodes = children.map((b) => this.makeBranchNode(b, hash));
      for (const n of nodes) {
        const nk = this.nodeKey(n);
        this.nodeCache.set(nk, n);
        this.parentMap.set(nk, element);
      }
      return nodes;
    }

    return [];
  }

  getParent(element: StackTreeItem): StackTreeItem | undefined {
    return this.parentMap.get(this.nodeKey(element));
  }
}
