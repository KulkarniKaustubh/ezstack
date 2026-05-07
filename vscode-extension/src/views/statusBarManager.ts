import * as vscode from "vscode";
import { EzsCli } from "../ezsCli";
import { StatusStackJSON } from "../types";
import { extractTicket, shortBranchName } from "../branchUtils";

export class StatusBarManager {
  private item: vscode.StatusBarItem;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(private cli: EzsCli) {
    this.item = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Left,
      100,
    );
    this.item.command = "ezstack.goto";
    this.item.tooltip = "ezstack: Click to navigate branches";
    this.disposables.push(this.item);
    this.disposables.push(
      vscode.window.onDidChangeActiveTextEditor(() => void this.update()),
    );
  }

  async update(): Promise<void> {
    try {
      const stacks = await this.cli.listStacks();
      const current = this.findCurrentBranch(stacks);
      if (!current) {
        this.item.hide();
        return;
      }
      const { branchName, stack, position, stackSize } = current;
      const ticket = extractTicket(branchName) ?? "";
      const desc = shortBranchName(branchName);
      const posLabel = `[${position + 1}/${stackSize}]`;
      const prefix = ticket ? `${ticket} ${posLabel}` : posLabel;
      // Public-fork stacking: append a small suffix so the user knows the
      // active stack contributes cross-repo to upstream.
      const forkSuffix = stack.is_fork_mode && stack.upstream_repo
        ? ` (fork→${stack.upstream_repo})`
        : "";
      this.item.text = `$(layers) ${prefix} ${desc}${forkSuffix}`;
      const stackLabel = stack.name || `Stack ${stack.hash.slice(0, 7)}`;
      const forkLine = stack.is_fork_mode && stack.upstream_repo
        ? `\nFork mode → ${stack.upstream_repo}`
        : "";
      this.item.tooltip = `${branchName} | ${stackLabel}${forkLine}\nClick to navigate branches`;
      this.item.show();
    } catch {
      this.item.hide();
    }
  }

  private findCurrentBranch(
    stacks: StatusStackJSON[],
  ): {
    branchName: string;
    stack: StatusStackJSON;
    position: number;
    stackSize: number;
  } | null {
    for (const stack of stacks) {
      for (let i = 0; i < stack.branches.length; i++) {
        const b = stack.branches[i];
        if (b.is_current) {
          return {
            branchName: b.name,
            stack,
            position: i,
            stackSize: stack.branches.length,
          };
        }
      }
    }
    return null;
  }

  dispose(): void {
    this.disposables.forEach((d) => d.dispose());
  }
}
