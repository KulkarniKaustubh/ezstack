import * as vscode from "vscode";
import { EzsCli } from "./ezsCli";
import { StackTreeProvider } from "./views/stackTreeProvider";
import { StatusBarManager } from "./views/statusBarManager";
import { ConfigWatcher } from "./configWatcher";
import { registerCommands } from "./commands/index";

export async function activate(
  context: vscode.ExtensionContext,
): Promise<void> {
  const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  if (!workspaceRoot) {
    return;
  }

  const cli = new EzsCli(workspaceRoot);

  // Check if ezs is available
  const available = await cli.isAvailable();
  if (!available) {
    vscode.window.showWarningMessage(
      'ezstack: "ezs" CLI not found. Install it or set ezstack.cliPath in settings.',
    );
    return;
  }

  // Tree view
  const treeProvider = new StackTreeProvider(cli);
  const treeView = vscode.window.createTreeView("ezstackStacks", {
    treeDataProvider: treeProvider,
    showCollapseAll: true,
  });
  context.subscriptions.push(treeView);

  // Status bar
  const statusBar = new StatusBarManager(cli);
  context.subscriptions.push(statusBar);
  await statusBar.update();

  // Config watcher for auto-refresh
  const autoRefresh = vscode.workspace
    .getConfiguration("ezstack")
    .get<boolean>("autoRefresh", true);

  if (autoRefresh) {
    const watcher = new ConfigWatcher(workspaceRoot);
    // Debounce: wait 500ms after last change before refreshing
    let debounceTimer: ReturnType<typeof setTimeout> | undefined;
    watcher.onDidChange(() => {
      if (debounceTimer) {
        clearTimeout(debounceTimer);
      }
      debounceTimer = setTimeout(() => {
        treeProvider.refresh();
        statusBar.update();
      }, 500);
    });
    context.subscriptions.push(watcher);
  }

  // Register commands
  registerCommands(context, cli, treeProvider, statusBar);

  // Listen for terminal close to refresh (after interactive commands)
  context.subscriptions.push(
    vscode.window.onDidCloseTerminal((terminal) => {
      if (terminal.name.startsWith("ezstack:")) {
        // Delay to allow config file to be written
        setTimeout(() => {
          treeProvider.refresh();
          statusBar.update();
        }, 1000);
      }
    }),
  );
}

export function deactivate(): void {
  // Cleanup handled by disposables
}
