import * as vscode from "vscode";
import { EzsCli } from "../ezsCli";
import { StatusStackJSON } from "../types";

export class StatusBarManager {
  private item: vscode.StatusBarItem;

  constructor(private cli: EzsCli) {
    this.item = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Left,
      100,
    );
    this.item.command = "ezstack.goto";
    this.item.tooltip = "ezstack: Click to navigate branches";
  }

  async update(): Promise<void> {
    try {
      const stacks = await this.cli.listStacks();
      const current = this.findCurrentBranch(stacks);
      if (!current) {
        this.item.hide();
        return;
      }
      const stackName =
        current.stack.name || `Stack ${current.stack.hash.slice(0, 7)}`;
      this.item.text = `$(layers) ${current.branchName} | ${stackName}`;
      this.item.show();
    } catch {
      this.item.hide();
    }
  }

  private findCurrentBranch(
    stacks: StatusStackJSON[],
  ): { branchName: string; stack: StatusStackJSON } | null {
    for (const stack of stacks) {
      for (const b of stack.branches) {
        if (b.is_current) {
          return { branchName: b.name, stack };
        }
      }
    }
    return null;
  }

  dispose(): void {
    this.item.dispose();
  }
}
