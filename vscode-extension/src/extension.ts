import * as path from "path";
import * as vscode from "vscode";
import { EzsCli } from "./ezsCli";
import { StackTreeProvider } from "./views/stackTreeProvider";
import { FileTreeProvider } from "./views/fileTreeProvider";
import { StatusBarManager } from "./views/statusBarManager";
import { FolderDecorationProvider } from "./views/folderDecorations";
import { ConfigWatcher } from "./configWatcher";
import { registerCommands } from "./commands/index";
import {
  registerFileNavigationCommands,
  updateContextKeys,
} from "./commands/fileNavigation";

export async function activate(
  context: vscode.ExtensionContext,
): Promise<void> {
  const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  if (!workspaceRoot) {
    return;
  }

  const cli = new EzsCli(workspaceRoot);

  // Stack overview tree (top panel)
  const treeProvider = new StackTreeProvider(cli);
  const treeView = vscode.window.createTreeView("ezstackStacks", {
    treeDataProvider: treeProvider,
    showCollapseAll: true,
  });
  context.subscriptions.push(treeView);

  // File explorer tree (bottom panel)
  const fileTreeProvider = new FileTreeProvider();
  fileTreeProvider.initStorage(context.globalState);
  const fileTreeView = vscode.window.createTreeView("ezstackFiles", {
    treeDataProvider: fileTreeProvider,
    showCollapseAll: true,
  });
  context.subscriptions.push(fileTreeView, fileTreeProvider);

  // Track expand/collapse for per-branch state persistence
  context.subscriptions.push(...fileTreeProvider.bindTreeView(fileTreeView));

  // Update file tree title when branch is selected
  fileTreeProvider.onDidSelectBranch((worktreePath) => {
    const branchName = path.basename(worktreePath);
    fileTreeView.title = `Files: ${branchName}`;
  });

  // Command to select a branch for the file explorer
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.selectBranch",
      (worktreePath: string) => {
        const gitStatus = treeProvider.getGitStatus(worktreePath);
        fileTreeProvider.selectBranch(worktreePath, gitStatus);
      },
    ),
  );

  // Favorites commands
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.toggleFavorite",
      (node: { absolutePath: string }) => {
        if (node?.absolutePath) {
          fileTreeProvider.toggleFavorite(node.absolutePath);
        }
      },
    ),
    vscode.commands.registerCommand("ezstack.filterFavorites", () => {
      fileTreeProvider.setFilterByFavorites(true);
    }),
    vscode.commands.registerCommand("ezstack.showAll", () => {
      fileTreeProvider.setFilterByFavorites(false);
    }),
    vscode.commands.registerCommand(
      "ezstack.copyPath",
      (node: { absolutePath: string }) => {
        if (node?.absolutePath) {
          vscode.env.clipboard.writeText(node.absolutePath);
        }
      },
    ),
    vscode.commands.registerCommand(
      "ezstack.copyRelativePath",
      (node: { absolutePath: string }) => {
        if (node?.absolutePath) {
          const selected = fileTreeProvider.getSelectedWorktree();
          const rel = selected
            ? path.relative(selected, node.absolutePath)
            : node.absolutePath;
          vscode.env.clipboard.writeText(rel);
        }
      },
    ),
    vscode.commands.registerCommand(
      "ezstack.revealInFinder",
      (node: { absolutePath: string }) => {
        if (node?.absolutePath) {
          vscode.commands.executeCommand(
            "revealFileInOS",
            vscode.Uri.file(node.absolutePath),
          );
        }
      },
    ),
    vscode.commands.registerCommand(
      "ezstack.openInTerminal",
      (node: { absolutePath: string; isDirectory: boolean }) => {
        if (node?.absolutePath) {
          const dir = node.isDirectory
            ? node.absolutePath
            : path.dirname(node.absolutePath);
          const terminal = vscode.window.createTerminal({ cwd: dir });
          terminal.show();
        }
      },
    ),
    vscode.commands.registerCommand(
      "ezstack.revealInExplorer",
      (node: { absolutePath: string }) => {
        if (node?.absolutePath) {
          vscode.commands.executeCommand(
            "revealInExplorer",
            vscode.Uri.file(node.absolutePath),
          );
        }
      },
    ),
  );

  const statusBar = new StatusBarManager(cli);
  context.subscriptions.push(statusBar);

  registerCommands(context, cli, treeProvider, statusBar, treeView);

  // Check if ezs is available — warn but don't bail
  const available = await cli.isAvailable();
  if (!available) {
    vscode.window.showWarningMessage(
      'ezstack: "ezs" CLI not found. Install it or set ezstack.cliPath in settings.',
    );
    return;
  }

  // Folder decorations (colored badges on workspace folders)
  const decorations = new FolderDecorationProvider(cli);
  context.subscriptions.push(
    vscode.window.registerFileDecorationProvider(decorations),
    decorations,
  );
  await decorations.refresh();

  // File navigation commands (open in next/prev PR, compare with prev)
  registerFileNavigationCommands(context, decorations);
  updateContextKeys(decorations);

  // Now that we know ezs works, do the initial data load
  await statusBar.update();

  // Follow active editor: switch file tree to the branch containing the focused file
  context.subscriptions.push(
    vscode.window.onDidChangeActiveTextEditor((editor) => {
      if (!editor) {
        return;
      }
      const filePath = editor.document.uri.fsPath;
      const stacks = treeProvider.getStacks();
      for (const stack of stacks) {
        for (const branch of stack.branches) {
          if (
            branch.worktree_path &&
            filePath.startsWith(branch.worktree_path + path.sep)
          ) {
            const currentSelection = fileTreeProvider.getSelectedWorktree();
            if (currentSelection !== branch.worktree_path) {
              const gitStatus = treeProvider.getGitStatus(
                branch.worktree_path,
              );
              fileTreeProvider.selectBranch(branch.worktree_path, gitStatus);
            }
            return;
          }
        }
      }
    }),
  );

  // Config watcher for auto-refresh
  const autoRefresh = vscode.workspace
    .getConfiguration("ezstack")
    .get<boolean>("autoRefresh", true);

  const pendingTimers: ReturnType<typeof setTimeout>[] = [];
  let debounceTimer: ReturnType<typeof setTimeout> | undefined;

  if (autoRefresh) {
    const watcher = new ConfigWatcher(workspaceRoot);
    watcher.onDidChange(() => {
      if (debounceTimer) {
        clearTimeout(debounceTimer);
      }
      debounceTimer = setTimeout(() => {
        treeProvider.refresh();
        void statusBar.update();
        void decorations.refresh();
      }, 500);
    });
    context.subscriptions.push(watcher);
  }

  // Refresh git status on file save (debounced)
  let saveTimer: ReturnType<typeof setTimeout> | undefined;
  context.subscriptions.push(
    vscode.workspace.onDidSaveTextDocument(() => {
      if (saveTimer) {
        clearTimeout(saveTimer);
      }
      saveTimer = setTimeout(() => {
        treeProvider.refresh();
        fileTreeProvider.refresh();
      }, 300);
    }),
  );

  // Listen for terminal close to refresh (after interactive commands)
  context.subscriptions.push(
    vscode.window.onDidCloseTerminal((terminal) => {
      if (terminal.name.startsWith("ezstack:")) {
        const timer = setTimeout(() => {
          treeProvider.refresh();
          void statusBar.update();
          void decorations.refresh();
          fileTreeProvider.refresh();
        }, 1000);
        pendingTimers.push(timer);
      }
    }),
  );

  // Clean up timers on deactivation
  context.subscriptions.push({
    dispose() {
      for (const t of pendingTimers) {
        clearTimeout(t);
      }
      if (debounceTimer) {
        clearTimeout(debounceTimer);
      }
      if (saveTimer) {
        clearTimeout(saveTimer);
      }
    },
  });
}

export function deactivate(): void {
  // Cleanup handled by disposables
}
