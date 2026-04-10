import * as vscode from "vscode";
import * as path from "path";
import * as fs from "fs";
import { FolderDecorationProvider } from "../views/folderDecorations";
import { shortBranchName } from "../branchUtils";

interface FileArg {
  absolutePath?: string;
}

/** Resolve a file URI from either a tree node argument or the active editor. */
function resolveFileUri(node?: FileArg): vscode.Uri | undefined {
  if (node?.absolutePath) {
    return vscode.Uri.file(node.absolutePath);
  }
  return vscode.window.activeTextEditor?.document.uri;
}

export function registerFileNavigationCommands(
  context: vscode.ExtensionContext,
  decorations: FolderDecorationProvider,
): void {
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.openInNextPR",
      (node?: FileArg) => navigateFile(decorations, "next", node),
    ),
    vscode.commands.registerCommand(
      "ezstack.openInPreviousPR",
      (node?: FileArg) => navigateFile(decorations, "prev", node),
    ),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.compareWithPreviousPR",
      (node?: FileArg) => compareWithPrevious(decorations, node),
    ),
  );

  context.subscriptions.push(
    vscode.window.onDidChangeActiveTextEditor(() =>
      updateContextKeys(decorations),
    ),
  );
}

async function navigateFile(
  decorations: FolderDecorationProvider,
  direction: "next" | "prev",
  node?: FileArg,
): Promise<void> {
  const fileUri = resolveFileUri(node);
  if (!fileUri) {
    vscode.window.showInformationMessage("No file selected.");
    return;
  }

  const ctx = decorations.getContextForUri(fileUri);
  if (!ctx || !ctx.branch.worktree_path) {
    vscode.window.showInformationMessage(
      "File is not in a tracked stack worktree.",
    );
    return;
  }

  const neighbors = decorations.getNeighbors(ctx.branch.worktree_path);
  if (!neighbors) {
    return;
  }

  const target = direction === "next" ? neighbors.next : neighbors.prev;
  if (!target) {
    const label = direction === "next" ? "next" : "previous";
    vscode.window.showInformationMessage(`No ${label} PR in the stack.`);
    return;
  }

  if (!target.worktree_path) {
    vscode.window.showWarningMessage(
      `Worktree for "${shortBranchName(target.name)}" does not exist locally.`,
    );
    return;
  }

  const relativePath = path.relative(
    ctx.branch.worktree_path,
    fileUri.fsPath,
  );
  const targetPath = path.join(target.worktree_path, relativePath);

  try {
    await fs.promises.access(targetPath);
    const doc = await vscode.workspace.openTextDocument(
      vscode.Uri.file(targetPath),
    );
    await vscode.window.showTextDocument(doc);
  } catch {
    vscode.window.showWarningMessage(
      `File does not exist in "${shortBranchName(target.name)}": ${relativePath}`,
    );
  }
}

async function compareWithPrevious(
  decorations: FolderDecorationProvider,
  node?: FileArg,
): Promise<void> {
  const fileUri = resolveFileUri(node);
  if (!fileUri) {
    vscode.window.showInformationMessage("No file selected.");
    return;
  }

  const ctx = decorations.getContextForUri(fileUri);
  if (!ctx || !ctx.branch.worktree_path) {
    vscode.window.showInformationMessage(
      "File is not in a tracked stack worktree.",
    );
    return;
  }

  const neighbors = decorations.getNeighbors(ctx.branch.worktree_path);
  const prev = neighbors?.prev;
  if (!prev) {
    vscode.window.showInformationMessage(
      "No previous PR in the stack to compare against.",
    );
    return;
  }

  if (!prev.worktree_path) {
    vscode.window.showWarningMessage(
      `Worktree for "${shortBranchName(prev.name)}" does not exist locally.`,
    );
    return;
  }

  const filePath = fileUri.fsPath;
  const relativePath = path.relative(ctx.branch.worktree_path, filePath);
  const prevPath = path.join(prev.worktree_path, relativePath);

  try {
    await fs.promises.access(prevPath);
  } catch {
    vscode.window.showWarningMessage(
      `File does not exist in "${shortBranchName(prev.name)}": ${relativePath}`,
    );
    return;
  }

  const prevLabel = shortBranchName(prev.name);
  const curLabel = shortBranchName(ctx.branch.name);
  const fileName = path.basename(filePath);
  const title = `${fileName}: ${prevLabel} ↔ ${curLabel}`;

  await vscode.commands.executeCommand(
    "vscode.diff",
    vscode.Uri.file(prevPath),
    vscode.Uri.file(filePath),
    title,
  );
}

export function updateContextKeys(
  decorations: FolderDecorationProvider,
): void {
  const editor = vscode.window.activeTextEditor;
  if (!editor) {
    vscode.commands.executeCommand("setContext", "ezstack.hasNextPR", false);
    vscode.commands.executeCommand(
      "setContext",
      "ezstack.hasPreviousPR",
      false,
    );
    return;
  }

  const ctx = decorations.getContextForUri(editor.document.uri);
  if (!ctx || !ctx.branch.worktree_path) {
    vscode.commands.executeCommand("setContext", "ezstack.hasNextPR", false);
    vscode.commands.executeCommand(
      "setContext",
      "ezstack.hasPreviousPR",
      false,
    );
    return;
  }

  const neighbors = decorations.getNeighbors(ctx.branch.worktree_path);
  vscode.commands.executeCommand(
    "setContext",
    "ezstack.hasNextPR",
    !!neighbors?.next,
  );
  vscode.commands.executeCommand(
    "setContext",
    "ezstack.hasPreviousPR",
    !!neighbors?.prev,
  );
}
