import { execFile, execFileSync } from "child_process";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";
import {
  StackJSON,
  StatusStackJSON,
  SyncInfoJSON,
} from "./types";

const COMMON_PATHS = [
  // make install (XDG convention)
  path.join(os.homedir(), ".local", "bin", "ezs"),
  // go install (GOBIN or GOPATH/bin or ~/go/bin)
  process.env.GOBIN ? path.join(process.env.GOBIN, "ezs") : "",
  process.env.GOPATH ? path.join(process.env.GOPATH, "bin", "ezs") : "",
  path.join(os.homedir(), "go", "bin", "ezs"),
  // system-wide
  "/usr/local/bin/ezs",
].filter(Boolean);

function findEzsBinary(): string {
  // Try `which ezs` first (works if it's on PATH)
  try {
    const result = execFileSync("which", ["ezs"], { timeout: 3000 }).toString().trim();
    if (result && fs.existsSync(result)) {
      return result;
    }
  } catch {
    // not on PATH
  }

  // Check common install locations
  for (const p of COMMON_PATHS) {
    if (p && fs.existsSync(p)) {
      return p;
    }
  }

  return "ezs"; // fallback — will fail with a clear error
}

export class EzsCli {
  private resolvedPath: string | undefined;

  constructor(
    private workspaceRoot: string,
  ) {}

  private get cliPath(): string {
    const configured = vscode.workspace
      .getConfiguration("ezstack")
      .get<string>("cliPath", "ezs");

    // If user set an explicit path, use it
    if (configured !== "ezs") {
      return configured;
    }

    // Auto-resolve once
    if (!this.resolvedPath) {
      this.resolvedPath = findEzsBinary();
    }
    return this.resolvedPath;
  }

  private static stripAnsi(s: string): string {
    // eslint-disable-next-line no-control-regex
    return s.replace(/\x1b\[[0-9;]*m/g, "");
  }

  /** Run an ezs command and return stdout. */
  private exec(args: string[]): Promise<string> {
    return new Promise((resolve, reject) => {
      execFile(
        this.cliPath,
        args,
        { cwd: this.workspaceRoot, timeout: 30_000 },
        (error, stdout, stderr) => {
          if (error) {
            // Only use stderr lines that look like actual errors, not UI output
            const cleaned = EzsCli.stripAnsi(stderr).trim();
            reject(new Error(cleaned || error.message));
          } else {
            // ezs writes UI output to stderr even on success — ignore it
            resolve(stdout);
          }
        },
      );
    });
  }

  /** Run an ezs command with -y flag (skip confirmations). */
  private execYes(args: string[]): Promise<string> {
    return this.exec(["-y", ...args]);
  }

  /** Run an ezs command in the integrated terminal (for interactive use). */
  runInTerminal(args: string[]): vscode.Terminal {
    const terminal = vscode.window.createTerminal({
      name: `ezstack: ${args[0]}`,
      cwd: this.workspaceRoot,
    });
    terminal.show();
    terminal.sendText(`${this.cliPath} ${args.join(" ")}`);
    return terminal;
  }

  // ── Read operations ──

  async isAvailable(): Promise<boolean> {
    try {
      await this.exec(["--version"]);
      return true;
    } catch {
      return false;
    }
  }

  async getVersion(): Promise<string> {
    const out = await this.exec(["--version"]);
    return out.trim();
  }

  async listStacks(all = false): Promise<StackJSON[]> {
    const args = ["list", "--json"];
    if (all) {
      args.push("--all");
    }
    const out = await this.exec(args);
    return JSON.parse(out);
  }

  async statusStacks(all = false): Promise<StatusStackJSON[]> {
    const args = ["status", "--json"];
    if (all) {
      args.push("--all");
    }
    const out = await this.exec(args);
    return JSON.parse(out);
  }

  async syncDryRun(all = false): Promise<SyncInfoJSON[]> {
    const args = ["sync", "--dry-run", "--json"];
    if (all) {
      args.push("--all");
    }
    const out = await this.exec(args);
    return JSON.parse(out);
  }

  // ── Mutations (headless with -y) ──

  async push(force = false): Promise<void> {
    const args = ["push"];
    if (force) {
      args.push("--force");
    }
    await this.execYes(args);
  }

  async pushStack(force = false): Promise<void> {
    const args = ["push", "-s"];
    if (force) {
      args.push("--force");
    }
    await this.execYes(args);
  }

  async newBranch(name: string, parent?: string): Promise<void> {
    const args = ["new", name];
    if (parent) {
      args.push("-p", parent);
    }
    await this.execYes(args);
  }

  async deleteBranch(name: string): Promise<void> {
    await this.execYes(["delete", name]);
  }

  async reparent(branch: string, newParent: string): Promise<void> {
    await this.execYes(["reparent", branch, newParent]);
  }

  async prCreate(title: string, draft = false): Promise<void> {
    const args = ["pr", "create", "-t", title];
    if (draft) {
      args.push("-d");
    }
    await this.execYes(args);
  }

  async prUpdate(): Promise<void> {
    await this.execYes(["pr", "update"]);
  }

  async prMerge(method: "squash" | "merge" | "rebase" = "squash"): Promise<void> {
    await this.execYes(["pr", "merge", "-m", method]);
  }

  async prDraft(): Promise<void> {
    await this.execYes(["pr", "draft"]);
  }

  async prStack(): Promise<void> {
    await this.execYes(["pr", "stack"]);
  }

  // ── Interactive (terminal) ──

  syncInteractive(mode: "current" | "stack" | "all" = "stack"): vscode.Terminal {
    const args = ["sync"];
    if (mode === "stack") {
      args.push("-s");
    }
    if (mode === "all") {
      args.push("-a");
    }
    return this.runInTerminal(args);
  }
}
