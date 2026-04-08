import * as vscode from "vscode";
import * as path from "path";
import * as os from "os";

/**
 * Watches ~/.ezstack/stacks.json and .git/HEAD for changes
 * to trigger automatic tree view refreshes.
 */
export class ConfigWatcher {
  private watchers: vscode.FileSystemWatcher[] = [];
  private _onDidChange = new vscode.EventEmitter<void>();
  readonly onDidChange = this._onDidChange.event;

  constructor(workspaceRoot?: string) {
    const ezstackDir = process.env.EZSTACK_HOME || path.join(os.homedir(), ".ezstack");

    // Watch stacks.json — changes after every ezs mutation
    const stacksPattern = new vscode.RelativePattern(
      vscode.Uri.file(ezstackDir),
      "stacks.json",
    );
    const stacksWatcher = vscode.workspace.createFileSystemWatcher(stacksPattern);
    stacksWatcher.onDidChange(() => this._onDidChange.fire());
    stacksWatcher.onDidCreate(() => this._onDidChange.fire());
    stacksWatcher.onDidDelete(() => this._onDidChange.fire());
    this.watchers.push(stacksWatcher);

    // Watch .git/HEAD — changes on branch switch
    if (workspaceRoot) {
      const gitHeadPattern = new vscode.RelativePattern(
        vscode.Uri.file(path.join(workspaceRoot, ".git")),
        "HEAD",
      );
      const gitWatcher = vscode.workspace.createFileSystemWatcher(gitHeadPattern);
      gitWatcher.onDidChange(() => this._onDidChange.fire());
      this.watchers.push(gitWatcher);
    }
  }

  dispose(): void {
    for (const w of this.watchers) {
      w.dispose();
    }
    this._onDidChange.dispose();
  }
}
