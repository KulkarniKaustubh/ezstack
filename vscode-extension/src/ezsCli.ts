import { execFile, execFileSync } from "child_process";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";
import {
  StackJSON,
  StatusStackJSON,
  WorktreeGitStatus,
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
  // Homebrew on Apple Silicon (not on default PATH for GUI launches)
  "/opt/homebrew/bin/ezs",
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
  private outputChannel: vscode.OutputChannel;

  constructor(
    private workspaceRoot: string,
  ) {
    this.outputChannel = vscode.window.createOutputChannel("ezstack");
  }

  getWorkspaceRoot(): string {
    return this.workspaceRoot;
  }

  getOutputChannel(): vscode.OutputChannel {
    return this.outputChannel;
  }

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

  /** Quote a string for safe shell use. */
  private static shellQuote(s: string): string {
    if (/^[a-zA-Z0-9_./:@=-]+$/.test(s)) {
      return s; // safe, no quoting needed
    }
    return `'${s.replace(/'/g, "'\\''")}'`;
  }

  private static stripAnsi(s: string): string {
    // eslint-disable-next-line no-control-regex
    return s.replace(/\x1b\[[0-9;]*[a-zA-Z]/g, "");
  }

  /** Parse JSON output from the CLI, throwing a descriptive error on failure. */
  private static parseJSON<T>(raw: string, command: string): T {
    try {
      return JSON.parse(raw);
    } catch {
      throw new Error(
        `ezs ${command} returned invalid JSON: ${raw.substring(0, 200)}`,
      );
    }
  }

  /** Run an ezs command and return stdout. */
  private exec(args: string[]): Promise<string> {
    return new Promise((resolve, reject) => {
      const cmdStr = `ezs ${args.join(" ")}`;
      execFile(
        this.cliPath,
        args,
        { cwd: this.workspaceRoot, timeout: 30_000 },
        (error, stdout, stderr) => {
          const cleanStderr = EzsCli.stripAnsi(stderr).trim();

          if (error) {
            this.outputChannel.appendLine(`> ${cmdStr}`);
            if (cleanStderr) {
              this.outputChannel.appendLine(cleanStderr);
            }
            this.outputChannel.appendLine("");
            reject(new Error(cleanStderr || error.message));
          } else {
            // Log UI output (stderr) on success
            if (cleanStderr) {
              this.outputChannel.appendLine(`> ${cmdStr}`);
              this.outputChannel.appendLine(cleanStderr);
              this.outputChannel.appendLine("");
            }
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
      shellPath: this.cliPath,
      shellArgs: args,
    });
    terminal.show();
    return terminal;
  }

  /** Fetch and pull in the given directory. */
  async gitFetchPull(cwd?: string): Promise<void> {
    await this.execGit(["fetch"], cwd);
    await this.execGit(["pull"], cwd);
  }

  /** Run a git command and return stdout. */
  private execGit(args: string[], cwd?: string): Promise<string> {
    return new Promise((resolve, reject) => {
      execFile(
        "git",
        args,
        { cwd: cwd ?? this.workspaceRoot, timeout: 10_000 },
        (error, stdout, stderr) => {
          if (error) {
            const detail = stderr?.trim() || error.message;
            reject(new Error(detail));
          } else {
            resolve(stdout);
          }
        },
      );
    });
  }

  /** Get git working tree status for a worktree directory. */
  async getWorktreeGitStatus(worktreePath: string): Promise<WorktreeGitStatus> {
    const result: WorktreeGitStatus = {
      modified: 0,
      staged: 0,
      untracked: 0,
      ahead: 0,
      behind: 0,
      files: new Map(),
    };

    try {
      const porcelain = await this.execGit(
        ["status", "--porcelain", "--branch"],
        worktreePath,
      );
      const lines = porcelain.trim().split("\n").filter(Boolean);
      for (const line of lines) {
        if (line.startsWith("##")) {
          const aheadMatch = line.match(/ahead (\d+)/);
          const behindMatch = line.match(/behind (\d+)/);
          if (aheadMatch) {
            result.ahead = parseInt(aheadMatch[1], 10);
          }
          if (behindMatch) {
            result.behind = parseInt(behindMatch[1], 10);
          }
          continue;
        }
        const indexChar = line[0];
        const worktreeChar = line[1];
        // File path starts at column 3; handle renames ("old -> new")
        let filePath = line.substring(3);
        const arrowIdx = filePath.indexOf(" -> ");
        if (arrowIdx !== -1) {
          filePath = filePath.substring(arrowIdx + 4);
        }

        if (indexChar === "?" && worktreeChar === "?") {
          result.untracked++;
          result.files.set(filePath, "untracked");
        } else if (indexChar === "U" || worktreeChar === "U") {
          result.files.set(filePath, "conflict");
        } else {
          const isStagedChange = indexChar !== " " && indexChar !== "?";
          const isWorktreeChange = worktreeChar !== " " && worktreeChar !== "?";
          if (isStagedChange) {
            result.staged++;
          }
          if (isWorktreeChange) {
            result.modified++;
          }
          if (isStagedChange && isWorktreeChange) {
            result.files.set(filePath, "both");
          } else if (isStagedChange) {
            result.files.set(filePath, "staged");
          } else {
            result.files.set(filePath, "modified");
          }
        }
      }
    } catch {
      // git status failed — return zeros
    }

    return result;
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

  /**
   * Return the parsed semver of the resolved `ezs` binary, or null if the
   * version line couldn't be parsed. Used by the activation gate to detect
   * when the extension is paired with a too-old CLI that lacks features the
   * extension calls (e.g. `--cascade`, `--draft-all`, `pr stack`,
   * `config export/import`, `stack rename`, `agent --preset`).
   *
   * Matches `ezstack version X.Y.Z` (the literal `cmd/ezs/main.go` format)
   * with optional pre-release/build suffix (e.g. `4.7.0-rc.1+abc`). The
   * leading "version " anchor is intentional: it stops a trailing build
   * hash like `1.2.3-2025.05.03+abc` from contributing a spurious second
   * triple match further down the string.
   */
  async getVersionSemver(): Promise<{ major: number; minor: number; patch: number } | null> {
    try {
      const out = await this.getVersion();
      const m = out.match(/version\s+v?(\d+)\.(\d+)\.(\d+)\b/i);
      if (!m) return null;
      return { major: parseInt(m[1], 10), minor: parseInt(m[2], 10), patch: parseInt(m[3], 10) };
    } catch {
      return null;
    }
  }

  /**
   * Exposed only for tests — `getVersion` shells out to the CLI and is
   * inconvenient to stub. parseVersionSemver runs the same regex against
   * an arbitrary string so tests can pin both happy-path output and
   * adversarial inputs (build metadata, garbage, blank lines).
   */
  static parseVersionSemver(out: string): { major: number; minor: number; patch: number } | null {
    const m = out.match(/version\s+v?(\d+)\.(\d+)\.(\d+)\b/i);
    if (!m) return null;
    return { major: parseInt(m[1], 10), minor: parseInt(m[2], 10), patch: parseInt(m[3], 10) };
  }

  async getLocalBranches(): Promise<string[]> {
    const out = await this.execGit(["branch", "--format=%(refname:short)"]);
    return out.trim().split("\n").filter(Boolean);
  }

  async listStacks(all = false): Promise<StackJSON[]> {
    const args = ["list", "--json"];
    if (all) {
      args.push("--all");
    }
    const out = await this.exec(args);
    return EzsCli.parseJSON(out, "list --json");
  }

  async statusStacks(all = false): Promise<StatusStackJSON[]> {
    const args = ["status", "--json"];
    if (all) {
      args.push("--all");
    }
    const out = await this.exec(args);
    return EzsCli.parseJSON(out, "status --json");
  }

  // ── Mutations (headless with -y) ──

  async push(force = false, branch?: string): Promise<void> {
    const args = ["push"];
    if (branch) {
      args.push("--branch", branch);
    }
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

  async prCreate(title: string, opts?: { draft?: boolean; body?: string; branch?: string }): Promise<void> {
    const args = ["pr", "create", "-t", title];
    if (opts?.draft) {
      args.push("-d");
    }
    if (opts?.body) {
      args.push("-b", opts.body);
    }
    if (opts?.branch) {
      args.push("--branch", opts.branch);
    }
    await this.execYes(args);
  }

  async prUpdate(branch?: string): Promise<void> {
    const args = ["pr", "update"];
    if (branch) {
      args.push("--branch", branch);
    }
    await this.execYes(args);
  }

  async prMerge(method: "squash" | "merge" | "rebase" = "squash", branch?: string): Promise<void> {
    const args = ["pr", "merge", "-m", method];
    if (branch) {
      args.push("--branch", branch);
    }
    await this.execYes(args);
  }

  async prDraft(branch?: string): Promise<void> {
    const args = ["pr", "draft"];
    if (branch) {
      args.push("--branch", branch);
    }
    await this.execYes(args);
  }

  async prStack(): Promise<void> {
    await this.execYes(["pr", "stack"]);
  }

  async renameStack(stackHash: string, name: string): Promise<void> {
    await this.exec(["stack", "rename", stackHash, name]);
  }

  // ── Agent (terminal) ──

  openAgent(stackHash: string): vscode.Terminal {
    return this.runInTerminal(["agent", "-s", stackHash]);
  }

  openAgentOnBranch(branchName: string): vscode.Terminal {
    return this.runInTerminal(["agent", "-b", branchName]);
  }

  openAgentFeature(stackHash: string, description: string): vscode.Terminal {
    return this.runInTerminal(["agent", "-s", stackHash, "feature", description]);
  }

  // ── Agent prompts ──

  /** Get the path to an agent prompt file. */
  getAgentPromptPath(type: "work" | "feature"): string {
    const filename = type === "work"
      ? "agent-work-prompt.md"
      : "agent-feature-prompt.md";
    const ezstackHome = process.env.EZSTACK_HOME || path.join(os.homedir(), ".ezstack");
    return path.join(ezstackHome, filename);
  }

  /** Ensure the agent prompt file exists (creates starter comment if missing). */
  async ensureAgentPromptFile(type: "work" | "feature"): Promise<string> {
    const promptPath = this.getAgentPromptPath(type);
    if (!fs.existsSync(promptPath)) {
      const dir = path.dirname(promptPath);
      if (!fs.existsSync(dir)) {
        fs.mkdirSync(dir, { recursive: true });
      }
      const starter = [
        `# Custom instructions for ezs agent (${type} session)`,
        "# Lines here are injected into the shipped prompt.",
        '# To fully override the shipped prompt, add "override: full" as the first line.',
        "#",
        "# Examples:",
        "#   - Always run tests before committing",
        "#   - Use conventional commits (feat:, fix:, etc.)",
        "#   - This repo uses pnpm, not npm",
        "",
      ].join("\n");
      fs.writeFileSync(promptPath, starter);
    }
    return promptPath;
  }

  async syncBranch(branch: string): Promise<void> {
    await this.execYes(["sync", "--branch", branch]);
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

  // ── Diagnostics ──

  /** Run `ezs doctor` and return its full stderr/stdout output as a single string. */
  async doctor(): Promise<string> {
    return new Promise((resolve, reject) => {
      execFile(
        this.cliPath,
        ["doctor"],
        { cwd: this.workspaceRoot, timeout: 30_000 },
        (error, stdout, stderr) => {
          // doctor exits non-zero when problems are detected, but the diagnostic
          // output we want to surface is on stderr regardless. Combine both
          // and let the caller decide whether the (possibly non-zero) exit
          // code matters.
          const combined = EzsCli.stripAnsi(`${stderr}\n${stdout}`).trim();
          if (error && combined === "") {
            reject(new Error(error.message));
            return;
          }
          resolve(combined);
        },
      );
    });
  }

  // ── Sync extras ──

  /** Sync the current stack with optional --stats / --squash. */
  async syncStack(opts?: { stats?: boolean; squash?: boolean }): Promise<void> {
    const args = ["sync", "-s"];
    if (opts?.stats) {
      args.push("--stats");
    }
    if (opts?.squash) {
      args.push("--squash");
    }
    await this.execYes(args);
  }

  // ── PR draft-all ──

  /** Create draft PRs for every branch in the current stack without one. */
  async prDraftAll(): Promise<void> {
    await this.execYes(["pr", "--draft-all"]);
  }

  // ── Delete with cascade ──

  /** Delete a branch and all its descendants. */
  async deleteBranchCascade(name: string): Promise<void> {
    await this.execYes(["delete", name, "--cascade"]);
  }

  // ── Config export / import ──

  /** Export the global ezstack config (token redacted) to the given path. */
  async configExport(filePath: string): Promise<void> {
    await this.exec(["config", "export", filePath]);
  }

  /** Import a previously-exported config file, replacing the current one. */
  async configImport(filePath: string): Promise<void> {
    await this.execYes(["config", "import", filePath]);
  }

  // ── Push extras ──

  /**
   * Push with optional --verify (require pre-push hook) and --all-remotes
   * (push to origin AND configured fork remote).
   */
  async pushWithFlags(opts: {
    branch?: string;
    force?: boolean;
    verify?: boolean;
    allRemotes?: boolean;
    stack?: boolean;
  }): Promise<void> {
    const args = ["push"];
    if (opts.stack) {
      args.push("-s");
    }
    if (opts.branch) {
      args.push("--branch", opts.branch);
    }
    if (opts.force) {
      args.push("--force");
    }
    if (opts.verify) {
      args.push("--verify");
    }
    if (opts.allRemotes) {
      args.push("--all-remotes");
    }
    await this.execYes(args);
  }

  // ── Agent extras ──

  /** Open the agent on a stack with --no-push and/or --preset overlays. */
  openAgentWithFlags(
    stackHash: string,
    opts?: { noPush?: boolean; preset?: string },
  ): vscode.Terminal {
    const args = ["agent", "-s", stackHash];
    if (opts?.noPush) {
      args.push("--no-push");
    }
    if (opts?.preset) {
      args.push("--preset", opts.preset);
    }
    return this.runInTerminal(args);
  }
}
