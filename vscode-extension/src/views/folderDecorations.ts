import * as vscode from "vscode";
import * as path from "path";
import { EzsCli } from "../ezsCli";
import { StackJSON, BranchJSON } from "../types";

const TICKET_RE = /nos-(\d+)/i;
const PALETTE_SIZE = 8;

interface BranchContext {
  stack: StackJSON;
  branch: BranchJSON;
  position: number;
  stackSize: number;
  ticketNumber: string | undefined;
  colorIndex: number;
}

function colorIndexForTicket(ticketNumber: string): number {
  let hash = 0;
  for (let i = 0; i < ticketNumber.length; i++) {
    hash = ((hash << 5) - hash + ticketNumber.charCodeAt(i)) | 0;
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
    } catch {
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
        const match = branch.name.match(TICKET_RE);
        const ticketNumber = match ? match[1] : undefined;
        this.worktreeMap.set(normalized, {
          stack,
          branch,
          position: i,
          stackSize: stack.branches.length,
          ticketNumber,
          colorIndex: ticketNumber ? colorIndexForTicket(ticketNumber) : 0,
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
    const parts = ctx.branch.name.split(".");
    const desc = parts.length > 1 ? parts[parts.length - 1] : ctx.branch.name;
    const ticket = ctx.ticketNumber ? `NOS-${ctx.ticketNumber}` : "Stack";
    const tooltip = `${ticket} [${ctx.position + 1}/${ctx.stackSize}] ${desc}`;

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
