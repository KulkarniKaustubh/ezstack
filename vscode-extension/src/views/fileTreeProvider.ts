import * as vscode from "vscode";
import * as fs from "fs";
import * as path from "path";
import { FileGitState, WorktreeGitStatus } from "../types";

const GIT_STATE_LABELS: Record<FileGitState, string> = {
  modified: "M",
  staged: "S",
  untracked: "U",
  both: "MS",
  conflict: "!",
};

const GIT_STATE_COLORS: Record<FileGitState, string> = {
  modified: "gitDecoration.modifiedResourceForeground",
  staged: "gitDecoration.addedResourceForeground",
  untracked: "gitDecoration.untrackedResourceForeground",
  both: "gitDecoration.modifiedResourceForeground",
  conflict: "gitDecoration.conflictingResourceForeground",
};

export class FileNode extends vscode.TreeItem {
  constructor(
    public readonly absolutePath: string,
    public readonly isDirectory: boolean,
    worktreeRoot: string,
    isFavorite: boolean,
    gitState?: FileGitState,
    expanded?: boolean,
  ) {
    super(
      path.basename(absolutePath),
      isDirectory
        ? expanded
          ? vscode.TreeItemCollapsibleState.Expanded
          : vscode.TreeItemCollapsibleState.Collapsed
        : vscode.TreeItemCollapsibleState.None,
    );

    this.id = worktreeRoot + ":" + absolutePath;
    this.resourceUri = vscode.Uri.file(absolutePath);

    // Build description: git state + star indicator on the right
    const descParts: string[] = [];
    if (!isDirectory && gitState) {
      descParts.push(GIT_STATE_LABELS[gitState]);
    }
    if (isFavorite) {
      descParts.push("★");
    }
    if (descParts.length > 0) {
      this.description = descParts.join(" ");
    }

    if (isDirectory) {
      this.contextValue = isFavorite ? "favoriteFolder" : "folder";
      this.iconPath = vscode.ThemeIcon.Folder;
    } else {
      this.contextValue = isFavorite ? "favoriteFile" : "file";
      this.command = {
        command: "vscode.open",
        title: "Open File",
        arguments: [vscode.Uri.file(absolutePath)],
      };
      if (gitState) {
        this.iconPath = new vscode.ThemeIcon(
          "file",
          new vscode.ThemeColor(GIT_STATE_COLORS[gitState]),
        );
      } else {
        this.iconPath = vscode.ThemeIcon.File;
      }
    }
  }
}

const FAVORITES_STORAGE_KEY = "ezstack.favorites";

export class FileTreeProvider implements vscode.TreeDataProvider<FileNode> {
  private _onDidChangeTreeData = new vscode.EventEmitter<
    FileNode | undefined | void
  >();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  private worktreeRoot: string | undefined;
  private gitStatus: WorktreeGitStatus | undefined;
  private _onDidSelectBranch = new vscode.EventEmitter<string>();
  readonly onDidSelectBranch = this._onDidSelectBranch.event;

  private expandedState = new Map<string, Set<string>>();

  /** Favorite relative paths (shared across all worktrees). */
  private favorites = new Set<string>();
  private filterByFavorites = false;
  private globalState: vscode.Memento | undefined;

  /** Load persisted favorites from extension globalState. */
  initStorage(globalState: vscode.Memento): void {
    this.globalState = globalState;
    const saved = globalState.get<string[]>(FAVORITES_STORAGE_KEY, []);
    this.favorites = new Set(saved);
  }

  private saveFavorites(): void {
    this.globalState?.update(FAVORITES_STORAGE_KEY, [...this.favorites]);
  }

  toggleFavorite(absolutePath: string): void {
    if (!this.worktreeRoot) {
      return;
    }
    const relativePath = path.relative(this.worktreeRoot, absolutePath);
    if (this.favorites.has(relativePath)) {
      this.favorites.delete(relativePath);
    } else {
      this.favorites.add(relativePath);
    }
    this.saveFavorites();
    this._onDidChangeTreeData.fire();
  }

  isFavorite(absolutePath: string): boolean {
    if (!this.worktreeRoot) {
      return false;
    }
    const relativePath = path.relative(this.worktreeRoot, absolutePath);
    return this.favorites.has(relativePath);
  }

  /** Check if a path or any ancestor is a favorite. */
  private isUnderFavorite(absolutePath: string): boolean {
    if (!this.worktreeRoot) {
      return false;
    }
    const relativePath = path.relative(this.worktreeRoot, absolutePath);
    // Exact match
    if (this.favorites.has(relativePath)) {
      return true;
    }
    // Check if any favorite is a parent of this path
    for (const fav of this.favorites) {
      if (relativePath.startsWith(fav + path.sep)) {
        return true;
      }
    }
    return false;
  }

  /** Check if a directory contains any favorites (directly or nested). */
  private containsFavorite(absolutePath: string): boolean {
    if (!this.worktreeRoot) {
      return false;
    }
    const relativePath = path.relative(this.worktreeRoot, absolutePath);
    const prefix = relativePath + path.sep;
    for (const fav of this.favorites) {
      if (fav === relativePath || fav.startsWith(prefix)) {
        return true;
      }
    }
    return false;
  }

  setFilterByFavorites(enabled: boolean): void {
    this.filterByFavorites = enabled;
    vscode.commands.executeCommand(
      "setContext",
      "ezstack.filterByFavorites",
      enabled,
    );
    this._onDidChangeTreeData.fire();
  }

  getFilterByFavorites(): boolean {
    return this.filterByFavorites;
  }

  bindTreeView(treeView: vscode.TreeView<FileNode>): vscode.Disposable[] {
    return [
      treeView.onDidExpandElement((e) => {
        if (this.worktreeRoot && e.element.isDirectory) {
          this.getOrCreateExpanded(this.worktreeRoot).add(
            e.element.absolutePath,
          );
        }
      }),
      treeView.onDidCollapseElement((e) => {
        if (this.worktreeRoot && e.element.isDirectory) {
          this.getOrCreateExpanded(this.worktreeRoot).delete(
            e.element.absolutePath,
          );
        }
      }),
    ];
  }

  private getOrCreateExpanded(worktreePath: string): Set<string> {
    let set = this.expandedState.get(worktreePath);
    if (!set) {
      set = new Set();
      this.expandedState.set(worktreePath, set);
    }
    return set;
  }

  selectBranch(
    worktreePath: string,
    gitStatus?: WorktreeGitStatus,
  ): void {
    this.worktreeRoot = worktreePath;
    this.gitStatus = gitStatus;
    this._onDidSelectBranch.fire(worktreePath);
    this._onDidChangeTreeData.fire();
  }

  getSelectedWorktree(): string | undefined {
    return this.worktreeRoot;
  }

  updateGitStatus(gitStatus?: WorktreeGitStatus): void {
    this.gitStatus = gitStatus;
    this._onDidChangeTreeData.fire();
  }

  refresh(): void {
    this._onDidChangeTreeData.fire();
  }

  getTreeItem(element: FileNode): vscode.TreeItem {
    return element;
  }

  private getFileGitState(
    absolutePath: string,
    isDirectory: boolean,
  ): FileGitState | undefined {
    if (!this.gitStatus || !this.worktreeRoot) {
      return undefined;
    }
    const relativePath = path.relative(this.worktreeRoot, absolutePath);
    if (!isDirectory) {
      return this.gitStatus.files.get(relativePath);
    }
    // Aggregate: pick the most prominent state among children
    const prefix = relativePath + path.sep;
    let hasModified = false;
    let hasStaged = false;
    let hasUntracked = false;
    let hasConflict = false;
    for (const [filePath, state] of this.gitStatus.files) {
      if (!filePath.startsWith(prefix)) {
        continue;
      }
      if (state === "conflict") {
        hasConflict = true;
      } else if (state === "modified" || state === "both") {
        hasModified = true;
      } else if (state === "staged") {
        hasStaged = true;
      } else if (state === "untracked") {
        hasUntracked = true;
      }
    }
    if (hasConflict) {
      return "conflict";
    }
    if (hasModified) {
      return "modified";
    }
    if (hasStaged) {
      return "staged";
    }
    if (hasUntracked) {
      return "untracked";
    }
    return undefined;
  }

  private isExpanded(absolutePath: string): boolean {
    if (!this.worktreeRoot) {
      return false;
    }
    const set = this.expandedState.get(this.worktreeRoot);
    return set ? set.has(absolutePath) : false;
  }

  async getChildren(element?: FileNode): Promise<FileNode[]> {
    const dirPath = element ? element.absolutePath : this.worktreeRoot;
    if (!dirPath || !this.worktreeRoot) {
      return [];
    }

    try {
      const entries = await fs.promises.readdir(dirPath, {
        withFileTypes: true,
      });
      const nodes: FileNode[] = [];
      for (const entry of entries) {
        if (entry.name.startsWith(".") || entry.name === "node_modules") {
          continue;
        }
        const fullPath = path.join(dirPath, entry.name);
        const isDir = entry.isDirectory();

        // Filter by favorites: show items that are favorites, under a favorite,
        // or (for directories) contain a favorite deeper inside
        if (this.filterByFavorites) {
          if (this.isUnderFavorite(fullPath)) {
            // show — it's a favorite or inside one
          } else if (isDir && this.containsFavorite(fullPath)) {
            // show — it's a parent leading to a favorite
          } else {
            continue;
          }
        }

        const gitState = this.getFileGitState(fullPath, isDir);
        const fav = this.isFavorite(fullPath);
        const expanded = isDir ? this.isExpanded(fullPath) : undefined;
        nodes.push(
          new FileNode(fullPath, isDir, this.worktreeRoot, fav, gitState, expanded),
        );
      }
      nodes.sort((a, b) => {
        if (a.isDirectory !== b.isDirectory) {
          return a.isDirectory ? -1 : 1;
        }
        return path
          .basename(a.absolutePath)
          .localeCompare(path.basename(b.absolutePath));
      });
      return nodes;
    } catch {
      return [];
    }
  }

  dispose(): void {
    this._onDidChangeTreeData.dispose();
    this._onDidSelectBranch.dispose();
  }
}
