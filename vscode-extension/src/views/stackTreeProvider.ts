import * as vscode from "vscode";
import { EzsCli } from "../ezsCli";
import { StatusStackJSON, StatusBranchJSON } from "../types";

export type StackTreeItem = StackNode | BranchNode;

export class StackNode extends vscode.TreeItem {
  constructor(public readonly stack: StatusStackJSON) {
    super(
      stack.name || `Stack ${stack.hash.slice(0, 7)}`,
      vscode.TreeItemCollapsibleState.Expanded,
    );
    this.description = `root: ${stack.root}`;
    this.iconPath = new vscode.ThemeIcon("layers");
    this.contextValue = "stack";
  }
}

export class BranchNode extends vscode.TreeItem {
  constructor(
    public readonly branch: StatusBranchJSON,
    public readonly stackHash: string,
    hasChildren: boolean,
  ) {
    super(
      branch.name,
      hasChildren
        ? vscode.TreeItemCollapsibleState.Expanded
        : vscode.TreeItemCollapsibleState.None,
    );

    this.description = BranchNode.buildDescription(branch);
    this.iconPath = BranchNode.getIcon(branch);
    this.contextValue = branch.pr_number ? "branchWithPR" : "branch";

    this.tooltip = BranchNode.buildTooltip(branch);

    // Clicking a branch navigates to its worktree
    if (branch.worktree_path) {
      this.command = {
        command: "ezstack.openWorktree",
        title: "Open Worktree",
        arguments: [branch.worktree_path],
      };
    }
  }

  private static buildDescription(b: StatusBranchJSON): string {
    const parts: string[] = [];
    if (b.pr_number) {
      parts.push(`PR #${b.pr_number}`);
    }
    if (b.pr_state) {
      parts.push(`[${b.pr_state}]`);
    }
    if (b.ci_summary) {
      parts.push(`CI: ${b.ci_summary}`);
    } else if (b.ci_state && b.ci_state !== "none") {
      parts.push(`CI: ${b.ci_state}`);
    }
    if (b.review_state) {
      parts.push(b.review_state.replace(/_/g, " "));
    }
    return parts.join(" ");
  }

  private static getIcon(b: StatusBranchJSON): vscode.ThemeIcon {
    if (b.is_merged) {
      return new vscode.ThemeIcon(
        "git-merge",
        new vscode.ThemeColor("gitDecoration.deletedResourceForeground"),
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

  private static buildTooltip(b: StatusBranchJSON): vscode.MarkdownString {
    const md = new vscode.MarkdownString();
    md.appendMarkdown(`**${b.name}**\n\n`);
    md.appendMarkdown(`Parent: \`${b.parent}\`\n\n`);
    if (b.pr_number && b.pr_url) {
      md.appendMarkdown(`PR: [#${b.pr_number}](${b.pr_url})`);
      if (b.pr_state) {
        md.appendMarkdown(` (${b.pr_state})`);
      }
      md.appendMarkdown("\n\n");
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
    if (b.mergeable) {
      md.appendMarkdown(`Mergeable: ${b.mergeable}\n\n`);
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
  // Map from branch name → child branches (within same stack)
  private childrenMap = new Map<string, StatusBranchJSON[]>();
  // Map from branch name → stack hash
  private branchStackMap = new Map<string, string>();

  constructor(private cli: EzsCli) {}

  refresh(): void {
    this._onDidChangeTreeData.fire();
  }

  async fetchData(): Promise<void> {
    try {
      this.stacks = await this.cli.statusStacks(true);
    } catch {
      // Fall back to list (no status) if status fails
      try {
        const basic = await this.cli.listStacks(true);
        this.stacks = basic as StatusStackJSON[];
      } catch {
        this.stacks = [];
      }
    }

    // Build children map
    this.childrenMap.clear();
    this.branchStackMap.clear();
    for (const stack of this.stacks) {
      for (const b of stack.branches) {
        this.branchStackMap.set(b.name, stack.hash);
        const siblings = this.childrenMap.get(b.parent) ?? [];
        siblings.push(b);
        this.childrenMap.set(b.parent, siblings);
      }
    }
  }

  getTreeItem(element: StackTreeItem): vscode.TreeItem {
    return element;
  }

  async getChildren(element?: StackTreeItem): Promise<StackTreeItem[]> {
    if (!element) {
      // Root level: fetch data and return stacks
      await this.fetchData();
      if (this.stacks.length === 0) {
        return [];
      }
      return this.stacks.map((s) => new StackNode(s));
    }

    if (element instanceof StackNode) {
      // Stack level: return top-level branches (children of root)
      const root = element.stack.root;
      const topBranches = this.childrenMap.get(root) ?? [];
      return topBranches.map(
        (b) =>
          new BranchNode(
            b,
            element.stack.hash,
            (this.childrenMap.get(b.name) ?? []).length > 0,
          ),
      );
    }

    if (element instanceof BranchNode) {
      // Branch level: return child branches
      const children = this.childrenMap.get(element.branch.name) ?? [];
      return children.map(
        (b) =>
          new BranchNode(
            b,
            element.stackHash,
            (this.childrenMap.get(b.name) ?? []).length > 0,
          ),
      );
    }

    return [];
  }
}
