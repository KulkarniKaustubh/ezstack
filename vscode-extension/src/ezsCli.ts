import { execFile } from "child_process";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";
import {
  DiffOutputJSON,
  LogOutputJSON,
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

/**
 * Synchronous best-effort lookup using fs.existsSync over a fixed list of
 * conventional install locations. A handful of stat calls is cheap (sub-ms
 * even on slow disks), so it's safe to run during activation. Returns null
 * when no common path matches; the async resolver below picks up the slack
 * for users with custom installs.
 */
function findEzsBinarySync(): string | null {
  for (const p of COMMON_PATHS) {
    if (p && fs.existsSync(p)) {
      return p;
    }
  }
  return null;
}

/**
 * Async fallback: shell out to `which` only if the sync lookup missed. This
 * was previously execFileSync inside the cliPath getter, which could hang
 * activation up to 3 seconds when the user's shell or filesystem was slow
 * (a real complaint on macOS GUI launches where PATH isn't inherited and
 * `which` ends up sourcing the user's profile).
 */
function findEzsBinaryAsync(): Promise<string | null> {
  return new Promise((resolve) => {
    execFile("which", ["ezs"], { timeout: 3000 }, (err, stdout) => {
      if (err) {
        resolve(null);
        return;
      }
      const result = stdout.toString().trim();
      if (result && fs.existsSync(result)) {
        resolve(result);
        return;
      }
      resolve(null);
    });
  });
}

export class EzsCli {
  private resolvedPath: string | undefined;
  private outputChannel: vscode.OutputChannel;

  constructor(
    private workspaceRoot: string,
  ) {
    this.outputChannel = vscode.window.createOutputChannel("ezstack");
    // Resolve synchronously against COMMON_PATHS first — fast and covers
    // the vast majority of installs (`make install`, homebrew, go install).
    this.resolvedPath = findEzsBinarySync() ?? undefined;
    // For users with ezs at a non-standard location, fire an async `which`
    // lookup that updates resolvedPath when it returns. Not awaited so
    // activation isn't gated on it. The literal "ezs" fallback below works
    // in the meantime as long as ezs is on the spawned process's PATH.
    if (!this.resolvedPath) {
      void findEzsBinaryAsync().then((p) => {
        if (p) {
          this.resolvedPath = p;
        }
      });
    }
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

    // Resolution happens in the constructor (sync COMMON_PATHS check, plus
    // an async `which` lookup that may still be in flight on first use).
    // The literal "ezs" fallback relies on PATH and produces a clear error
    // if the binary isn't installed.
    return this.resolvedPath ?? "ezs";
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

  /**
   * Create a PR. Pass `auto: true` to delegate title/body drafting to the
   * configured `agent_command` (`ezs pr create --auto`); `title` may then
   * be omitted. When `auto` is unset, `title` is required and the CLI
   * uses it verbatim. Explicit `title`/`body` win over AI output.
   */
  async prCreate(title: string | undefined, opts?: { draft?: boolean; body?: string; branch?: string; auto?: boolean }): Promise<void> {
    if (title === undefined && !opts?.auto) {
      throw new Error("prCreate: title is required unless auto is set");
    }
    const args = ["pr", "create"];
    if (title !== undefined) {
      args.push("-t", title);
    }
    if (opts?.draft) {
      args.push("-d");
    }
    if (opts?.body) {
      args.push("-b", opts.body);
    }
    if (opts?.branch) {
      args.push("--branch", opts.branch);
    }
    if (opts?.auto) {
      args.push("--auto");
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

  // ── Stack membership ──

  /**
   * Add a branch to a stack. `parent` attaches under an existing branch;
   * `base` starts a new stack rooted on a non-default base branch
   * (e.g. develop, staging). Mutually exclusive — caller passes one.
   */
  async stackBranch(
    branch: string,
    target: { parent?: string; base?: string },
  ): Promise<void> {
    const args = ["stack", "-b", branch];
    if (target.base) {
      args.push("-B", target.base);
    } else if (target.parent) {
      args.push("-p", target.parent);
    }
    await this.execYes(args);
  }

  /** Remove a branch from ezstack tracking (keeps git branch + worktree). */
  async unstackBranch(branch: string): Promise<void> {
    await this.execYes(["unstack", "-b", branch]);
  }

  // ── Commit / amend (stack-aware) ──

  /**
   * Commit and auto-sync child branches. `--no-push` is passed unconditionally
   * — the VS Code surface has no place to answer the CLI's interactive push
   * prompt, and committing-then-not-pushing is the safer default. Users who
   * want to push afterwards can use the existing Push Branch / Push Stack
   * actions on the same branch.
   */
  async commit(
    message: string,
    opts?: { all?: boolean; merge?: boolean; rebase?: boolean },
  ): Promise<void> {
    const args = ["commit", "-m", message, "--no-push"];
    if (opts?.all) {
      args.push("-a");
    }
    if (opts?.merge) {
      args.push("--merge");
    } else if (opts?.rebase) {
      args.push("--rebase");
    }
    await this.execYes(args);
  }

  /**
   * Amend the last commit and auto-sync child branches. When `message` is
   * omitted, the existing commit message is preserved (--no-edit). Like
   * commit, push is suppressed — amend rewrites history, so the intended
   * follow-up is Push with Options... → --force.
   */
  async amend(
    opts?: { message?: string; all?: boolean; merge?: boolean; rebase?: boolean },
  ): Promise<void> {
    const args = ["amend", "--no-push"];
    if (opts?.message) {
      args.push("-m", opts.message);
    } else {
      args.push("--no-edit");
    }
    if (opts?.all) {
      args.push("-a");
    }
    if (opts?.merge) {
      args.push("--merge");
    } else if (opts?.rebase) {
      args.push("--rebase");
    }
    await this.execYes(args);
  }

  // ── Inspection (diff / log) ──

  /** Diff a branch against its parent. JSON file-level numstat. */
  async diffJson(branch?: string): Promise<DiffOutputJSON> {
    const args = ["diff", "--json"];
    if (branch) {
      args.push("--branch", branch);
    }
    const out = await this.exec(args);
    return EzsCli.parseJSON(out, "diff --json");
  }

  /** Diff a branch against its parent. Plain `git diff --stat` output. */
  async diffStat(branch?: string): Promise<string> {
    const args = ["diff", "--stat"];
    if (branch) {
      args.push("--branch", branch);
    }
    return this.exec(args);
  }

  /** Commits in `branch` since its parent. JSON. */
  async log(branch?: string): Promise<LogOutputJSON> {
    const args = ["log", "--json"];
    if (branch) {
      args.push("--branch", branch);
    }
    const out = await this.exec(args);
    return EzsCli.parseJSON(out, "log --json");
  }

  // ── PR refresh ──

  /**
   * Reconcile the local PR cache against GitHub. Scope:
   *  - stack=true     → refresh every PR in the current stack (-s)
   *  - branch present → refresh that one
   *  - neither        → current branch
   *
   * Stack and branch are mutually exclusive on the CLI side; the helper
   * preserves that by giving stack priority over branch.
   */
  async prRefresh(opts?: { branch?: string; stack?: boolean }): Promise<void> {
    const args = ["pr", "refresh"];
    if (opts?.stack) {
      args.push("-s");
    } else if (opts?.branch) {
      args.push("--branch", opts.branch);
    }
    await this.execYes(args);
  }
}
