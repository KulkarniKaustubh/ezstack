import * as vscode from "vscode";
import * as path from "path";
import { EzsCli } from "../ezsCli";
import { StackJSON, BranchJSON } from "../types";
import { extractTicket, shortBranchName } from "../branchUtils";

const PALETTE_SIZE = 8;

interface BranchContext {
  stack: StackJSON;
  branch: BranchJSON;
  position: number;
  stackSize: number;
  ticket: string | undefined;
  colorIndex: number;
}

function colorIndexForHash(value: string): number {
  let hash = 0;
  for (let i = 0; i < value.length; i++) {
    hash = ((hash << 5) - hash + value.charCodeAt(i)) | 0;
  }
  return ((hash % PALETTE_SIZE) + PALETTE_SIZE) % PALETTE_SIZE;
}

export class FolderDecorationProvider
  implements vscode.FileDecorationProvider, vscode.Disposable
{
  private readonly _onDidChangeFileDecorations =
    new vscode.EventEmitter<vscode.Uri | vscode.Uri[] | undefined>();
  readonly onDidChangeFileDecorations = this._onDidChangeFileDecorations.event;
  private worktreeMap = new Map<string, BranchContext>();
  private workspaceFolderPaths = new Set<string>();
  private readonly disposables: vscode.Disposable[] = [];

  constructor(private readonly cli: EzsCli) {
    this.refreshWorkspaceFolders();
    this.disposables.push(this._onDidChangeFileDecorations);
    this.disposables.push(
      vscode.workspace.onDidChangeWorkspaceFolders(() => {
        this.refreshWorkspaceFolders();
        this._onDidChangeFileDecorations.fire(undefined);
      }),
    );
  }

  private refreshWorkspaceFolders(): void {
    this.workspaceFolderPaths.clear();
    for (const folder of vscode.workspace.workspaceFolders ?? []) {
      this.workspaceFolderPaths.add(path.normalize(folder.uri.fsPath));
    }
  }

  async refresh(): Promise<void> {
    try {
      const stacks = await this.cli.listStacks(true);
      this.rebuildMap(stacks);
    } catch (err) {
      console.warn("ezstack: failed to refresh folder decorations:", err instanceof Error ? err.message : err);
      this.worktreeMap.clear();
    }
    this._onDidChangeFileDecorations.fire(undefined);
  }

  private rebuildMap(stacks: StackJSON[]): void {
    this.worktreeMap.clear();
    for (const stack of stacks) {
      for (let i = 0; i < stack.branches.length; i++) {
        const branch = stack.branches[i];
        if (!branch.worktree_path) {
          continue;
        }
        const normalized = path.normalize(branch.worktree_path);
        const ticket = extractTicket(branch.name);
        this.worktreeMap.set(normalized, {
          stack,
          branch,
          position: i,
          stackSize: stack.branches.length,
          ticket,
          colorIndex: ticket ? colorIndexForHash(ticket) : 0,
        });
      }
    }
  }

  provideFileDecoration(uri: vscode.Uri): vscode.FileDecoration | undefined {
    const normalized = path.normalize(uri.fsPath);
    if (!this.workspaceFolderPaths.has(normalized)) {
      return undefined;
    }
    const ctx = this.worktreeMap.get(normalized);
    if (!ctx) {
      return undefined;
    }
    const badge =
      ctx.position < 9
        ? String(ctx.position + 1)
        : String.fromCharCode(65 + ctx.position - 9);
    const desc = shortBranchName(ctx.branch.name);
    const label = ctx.ticket ?? "Stack";
    const tooltip = `${label} [${ctx.position + 1}/${ctx.stackSize}] ${desc}`;

    return {
      badge,
      tooltip,
      color: new vscode.ThemeColor(
        `ezstack.stack${ctx.colorIndex % PALETTE_SIZE}`,
      ),
    };
  }

  /** Look up a URI's branch context by walking up to find a worktree root. */
  getContextForUri(uri: vscode.Uri): BranchContext | undefined {
    let dir = uri.fsPath;
    while (dir !== path.dirname(dir)) {
      const ctx = this.worktreeMap.get(path.normalize(dir));
      if (ctx) {
        return ctx;
      }
      dir = path.dirname(dir);
    }
    return undefined;
  }

  /** Get adjacent branches for a given worktree path. */
  getNeighbors(
    worktreePath: string,
  ): { prev?: BranchJSON; next?: BranchJSON } | undefined {
    const ctx = this.worktreeMap.get(path.normalize(worktreePath));
    if (!ctx) {
      return undefined;
    }
    const branches = ctx.stack.branches;
    return {
      prev: ctx.position > 0 ? branches[ctx.position - 1] : undefined,
      next:
        ctx.position < branches.length - 1
          ? branches[ctx.position + 1]
          : undefined,
    };
  }

  dispose(): void {
    this.disposables.forEach((d) => d.dispose());
  }
}
