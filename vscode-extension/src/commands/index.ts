import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { EzsCli } from "../ezsCli";
import { buildParentPickChoices, ParentPickChoice } from "../parentPicker";
import { StackTreeProvider, StackNode, BranchNode, RootRepoNode, StackTreeItem } from "../views/stackTreeProvider";
import { StatusBarManager } from "../views/statusBarManager";

const PR_TEMPLATE_PATHS = [
  ".github/PULL_REQUEST_TEMPLATE.md",
  ".github/pull_request_template.md",
  "PULL_REQUEST_TEMPLATE.md",
  "pull_request_template.md",
  "docs/pull_request_template.md",
];

function findPRTemplate(workspaceRoot: string): string | undefined {
  for (const p of PR_TEMPLATE_PATHS) {
    const full = path.join(workspaceRoot, p);
    if (fs.existsSync(full)) {
      try {
        return fs.readFileSync(full, "utf-8");
      } catch {
        // ignore
      }
    }
  }
  return undefined;
}

export function registerCommands(
  context: vscode.ExtensionContext,
  cli: EzsCli,
  treeProvider: StackTreeProvider,
  statusBar: StatusBarManager,
  treeView: vscode.TreeView<StackTreeItem>,
): void {
  const refreshAll = async () => {
    treeProvider.refresh();
    await statusBar.update();
  };

  const outputChannel = cli.getOutputChannel();

  /** Run a CLI mutation with progress notification, success/error toasts, and refresh. */
  const runWithFeedback = async (
    progressLabel: string,
    successLabel: string,
    fn: () => Promise<void>,
  ): Promise<void> => {
    try {
      await vscode.window.withProgress(
        { location: vscode.ProgressLocation.Notification, title: progressLabel },
        fn,
      );
      await refreshAll();
      const action = await vscode.window.showInformationMessage(
        successLabel,
        "Show Output",
      );
      if (action === "Show Output") {
        outputChannel.show();
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      const action = await vscode.window.showErrorMessage(msg, "Show Output");
      if (action === "Show Output") {
        outputChannel.show();
      }
    }
  };

  // ── Refresh ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.refresh", refreshAll),
  );

  // ── Expand All ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.expandAll", async () => {
      const roots = await treeProvider.getChildren();
      for (const root of roots) {
        await treeView.reveal(root, { expand: 10 });
      }
    }),
  );

  // ── New Branch ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.newBranch",
      async (node?: StackNode) => {
        const name = await vscode.window.showInputBox({
          prompt: "New branch name",
          placeHolder: "feature-part-2",
        });
        if (!name) {
          return;
        }

        let parent: string | undefined;
        try {
          if (node instanceof StackNode) {
            const candidates = [
              node.stack.root,
              ...node.stack.branches.map((b) => b.name),
            ];
            parent = await vscode.window.showQuickPick(candidates, {
              placeHolder: "Select parent branch in this stack",
            });
          } else {
            const [stacks, gitBranches] = await Promise.all([
              cli.listStacks(true).catch(() => []),
              cli.getLocalBranches().catch(() => []),
            ]);
            const choices = buildParentPickChoices(stacks, gitBranches);
            if (choices.length > 0) {
              type Item = vscode.QuickPickItem & { choice: ParentPickChoice };
              const items: Item[] = choices.map((c) => ({
                label: c.branchName,
                description: c.description,
                choice: c,
              }));
              const picked = await vscode.window.showQuickPick(items, {
                placeHolder:
                  "Select parent branch (or Esc to use current branch)",
                matchOnDescription: true,
              });
              parent = picked?.choice.branchName;
            }
          }
        } catch {
          // If listing fails, proceed without parent selection
        }

        await runWithFeedback(
          "Creating branch...",
          `Created branch "${name}".`,
          () => cli.newBranch(name, parent),
        );
      },
    ),
  );

  // ── Sync ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.sync",
      async (node?: StackNode) => {
        if (node instanceof StackNode) {
          cli.syncInteractive("stack");
          return;
        }
        const mode = await vscode.window.showQuickPick(
          [
            { label: "Current stack", value: "stack" as const },
            { label: "Current branch only", value: "current" as const },
            { label: "All stacks", value: "all" as const },
          ],
          { placeHolder: "Sync scope" },
        );
        if (!mode) {
          return;
        }
        cli.syncInteractive(mode.value);
      },
    ),
  );

  // ── Sync Branch (headless, specific branch) ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.syncBranch",
      async (node?: BranchNode) => {
        const branch = node instanceof BranchNode ? node.branch.name : undefined;
        if (!branch) {
          return;
        }
        await runWithFeedback(
          `Syncing "${branch}"...`,
          `Synced "${branch}".`,
          () => cli.syncBranch(branch),
        );
      },
    ),
  );

  // ── Push ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.push", (node?: BranchNode) => {
      const branch = node instanceof BranchNode ? node.branch.name : undefined;
      const label = branch ? `Pushing "${branch}"...` : "Pushing branch...";
      const success = branch ? `Pushed "${branch}".` : "Branch pushed.";
      return runWithFeedback(label, success, () =>
        cli.push(false, branch && !node?.branch.is_current ? branch : undefined),
      );
    }),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.pushStack", () =>
      runWithFeedback("Pushing stack...", "Stack pushed.", () =>
        cli.pushStack(),
      ),
    ),
  );

  // ── PR Create ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.prCreate",
      async (node?: BranchNode) => {
        let branchName: string | undefined;
        if (node) {
          branchName = node.branch.name;
        } else {
          const stacks = await cli.listStacks(true);
          const branches = stacks
            .flatMap((s) => s.branches)
            .filter((b) => !b.pr_number);
          if (branches.length === 0) {
            vscode.window.showInformationMessage(
              "All branches already have PRs.",
            );
            return;
          }
          const pick = await vscode.window.showQuickPick(
            branches.map((b) => ({
              label: b.name,
              detail: b.worktree_path,
            })),
            { placeHolder: "Select branch to create PR for" },
          );
          if (!pick) {
            return;
          }
          branchName = pick.label;
        }

        const title = await vscode.window.showInputBox({
          prompt: `PR title for "${branchName}"`,
          placeHolder: "Add feature X",
          value: branchName,
        });
        if (!title) {
          return;
        }

        const draftPick = await vscode.window.showQuickPick(
          [
            { label: "Ready for review", value: false },
            { label: "Draft", value: true },
          ],
          { placeHolder: "PR type" },
        );
        if (!draftPick) {
          return;
        }

        const workspaceRoot =
          vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? "";
        const template = findPRTemplate(workspaceRoot);
        const doc = await vscode.workspace.openTextDocument({
          language: "markdown",
          content: template ?? "",
        });
        await vscode.window.showTextDocument(doc, { preview: false });

        const action = await vscode.window.showInformationMessage(
          `Write the PR description for "${branchName}", then click "Create PR".`,
          "Create PR",
          "Cancel",
        );
        if (action !== "Create PR") {
          await vscode.commands.executeCommand(
            "workbench.action.closeActiveEditor",
          );
          return;
        }

        const body = doc.getText().trim();
        await vscode.commands.executeCommand(
          "workbench.action.closeActiveEditor",
        );

        await runWithFeedback(
          "Creating PR...",
          `PR created for "${branchName}".`,
          () =>
            cli.prCreate(title, {
              draft: draftPick.value,
              body: body || undefined,
              branch: branchName,
            }),
        );
      },
    ),
  );

  // ── PR Update ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.prUpdate",
      async (node?: BranchNode) => {
        const branch = node?.branch.name;
        await runWithFeedback("Updating PR...", "PR updated.", () =>
          cli.prUpdate(branch),
        );
      },
    ),
  );

  // ── PR Merge ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.prMerge",
      async (node?: BranchNode) => {
        const branch = node?.branch.name;
        const method = await vscode.window.showQuickPick(
          [
            { label: "Squash and merge", value: "squash" as const },
            { label: "Create merge commit", value: "merge" as const },
            { label: "Rebase and merge", value: "rebase" as const },
          ],
          { placeHolder: "Merge method" },
        );
        if (!method) {
          return;
        }

        const confirm = await vscode.window.showWarningMessage(
          `Merge this PR using ${method.label}?`,
          { modal: true },
          "Merge",
        );
        if (confirm !== "Merge") {
          return;
        }

        await runWithFeedback("Merging PR...", "PR merged.", () =>
          cli.prMerge(method.value, branch),
        );
      },
    ),
  );

  // ── PR Draft Toggle ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.prDraft",
      async (node?: BranchNode) => {
        const branch = node?.branch.name;
        await runWithFeedback("Toggling draft...", "Draft status toggled.", () =>
          cli.prDraft(branch),
        );
      },
    ),
  );

  // ── PR Stack ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.prStack", () =>
      runWithFeedback(
        "Updating stack info in PRs...",
        "Stack info updated in PRs.",
        () => cli.prStack(),
      ),
    ),
  );

  // ── Go to Branch ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.goto", async () => {
      try {
        const stacks = await cli.listStacks(true);
        const branches = stacks.flatMap((s) =>
          s.branches.map((b) => ({
            label: b.name,
            description: b.is_current ? "(current)" : "",
            detail: b.worktree_path || undefined,
            worktreePath: b.worktree_path,
          })),
        );

        const pick = await vscode.window.showQuickPick(branches, {
          placeHolder: "Select branch to navigate to",
        });
        if (!pick?.worktreePath) {
          return;
        }

        const uri = vscode.Uri.file(pick.worktreePath);
        await vscode.commands.executeCommand("revealInExplorer", uri);
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        vscode.window.showErrorMessage(`Failed to list branches: ${msg}`);
      }
    }),
  );

  // ── Up / Down navigation ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.up", async () => {
      try {
        const stacks = await cli.listStacks();
        const current = stacks
          .flatMap((s) => s.branches)
          .find((b) => b.is_current);
        if (!current) {
          vscode.window.showInformationMessage("No current branch found in any stack.");
          return;
        }
        if (!current.parent) {
          vscode.window.showInformationMessage("Already at the top of the stack.");
          return;
        }
        const parent = stacks
          .flatMap((s) => s.branches)
          .find((b) => b.name === current.parent);
        if (!parent) {
          vscode.window.showInformationMessage(
            `Parent "${current.parent}" is the stack root.`,
          );
          return;
        }
        if (!parent.worktree_path) {
          vscode.window.showInformationMessage(
            `Parent "${parent.name}" has no worktree.`,
          );
          return;
        }
        const uri = vscode.Uri.file(parent.worktree_path);
        await vscode.commands.executeCommand("revealInExplorer", uri);
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        vscode.window.showErrorMessage(`Failed to navigate up: ${msg}`);
      }
    }),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.down", async () => {
      try {
        const stacks = await cli.listStacks();
        const current = stacks
          .flatMap((s) => s.branches)
          .find((b) => b.is_current);
        if (!current) {
          vscode.window.showInformationMessage("No current branch found in any stack.");
          return;
        }
        const children = stacks
          .flatMap((s) => s.branches)
          .filter((b) => b.parent === current.name);
        if (children.length === 0) {
          vscode.window.showInformationMessage("Already at the bottom of the stack.");
          return;
        }
        let target = children[0];
        if (children.length > 1) {
          const pick = await vscode.window.showQuickPick(
            children.map((c) => ({
              label: c.name,
              worktreePath: c.worktree_path,
            })),
            { placeHolder: "Select child branch" },
          );
          if (!pick) {
            return;
          }
          target =
            children.find((c) => c.name === pick.label) ?? children[0];
        }
        if (target.worktree_path) {
          const uri = vscode.Uri.file(target.worktree_path);
          await vscode.commands.executeCommand("revealInExplorer", uri);
        }
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        vscode.window.showErrorMessage(`Failed to navigate down: ${msg}`);
      }
    }),
  );

  // ── Delete Branch ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.delete",
      async (node?: BranchNode) => {
        let branchName: string | undefined;
        if (node) {
          branchName = node.branch.name;
        } else {
          const stacks = await cli.listStacks(true);
          const branches = stacks.flatMap((s) =>
            s.branches.map((b) => b.name),
          );
          branchName = await vscode.window.showQuickPick(branches, {
            placeHolder: "Select branch to delete",
          });
        }
        if (!branchName) {
          return;
        }

        const confirm = await vscode.window.showWarningMessage(
          `Delete branch "${branchName}" and its worktree?`,
          { modal: true },
          "Delete",
        );
        if (confirm !== "Delete") {
          return;
        }

        await runWithFeedback(
          "Deleting branch...",
          `Deleted branch "${branchName}".`,
          () => cli.deleteBranch(branchName!),
        );
      },
    ),
  );

  // ── Reparent ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.reparent",
      async (node?: BranchNode) => {
        const stacks = await cli.listStacks(true);

        let branchName: string | undefined;
        if (node) {
          branchName = node.branch.name;
        } else {
          const branches = stacks.flatMap((s) =>
            s.branches.map((b) => b.name),
          );
          branchName = await vscode.window.showQuickPick(branches, {
            placeHolder: "Select branch to reparent",
          });
        }
        if (!branchName) {
          return;
        }

        const candidates = [
          ...new Set(
            stacks.flatMap((s) => [
              s.root,
              ...s.branches.map((b) => b.name),
            ]),
          ),
        ].filter((n) => n !== branchName);

        const newParent = await vscode.window.showQuickPick(candidates, {
          placeHolder: `Select new parent for "${branchName}"`,
        });
        if (!newParent) {
          return;
        }

        await runWithFeedback(
          "Reparenting...",
          `Reparented "${branchName}" onto "${newParent}".`,
          () => cli.reparent(branchName!, newParent),
        );
      },
    ),
  );

  // ── Open PR in Browser ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.openPR",
      async (node?: BranchNode) => {
        let prUrl: string | undefined;
        if (node?.branch.pr_url) {
          prUrl = node.branch.pr_url;
        } else {
          try {
            const stacks = await cli.listStacks(true);
            const withPRs = stacks
              .flatMap((s) => s.branches)
              .filter((b) => b.pr_url);
            if (withPRs.length === 0) {
              vscode.window.showInformationMessage("No branches have PRs.");
              return;
            }
            const pick = await vscode.window.showQuickPick(
              withPRs.map((b) => ({
                label: b.name,
                description: `PR #${b.pr_number}`,
                prUrl: b.pr_url,
              })),
              { placeHolder: "Select branch to open PR" },
            );
            if (!pick) {
              return;
            }
            prUrl = pick.prUrl;
          } catch {
            vscode.window.showErrorMessage("Failed to list branches.");
            return;
          }
        }
        if (prUrl) {
          await vscode.env.openExternal(vscode.Uri.parse(prUrl));
        }
      },
    ),
  );

  // ── Open Worktree Folder (new window) ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.openWorktree",
      async (pathOrNode?: string | BranchNode) => {
        let worktreePath: string | undefined;
        if (typeof pathOrNode === "string") {
          worktreePath = pathOrNode;
        } else if (pathOrNode instanceof BranchNode) {
          worktreePath = pathOrNode.branch.worktree_path;
        }
        if (!worktreePath) {
          return;
        }
        const uri = vscode.Uri.file(worktreePath);
        await vscode.commands.executeCommand("vscode.openFolder", uri, {
          forceNewWindow: true,
        });
      },
    ),
  );

  // ── Rename Stack ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.renameStack",
      async (node?: StackNode) => {
        let stackHash: string | undefined;
        let currentName: string | undefined;

        if (node instanceof StackNode) {
          stackHash = node.stack.hash;
          currentName = node.stack.name;
        } else {
          const stacks = await cli.listStacks(true);
          if (stacks.length === 0) {
            vscode.window.showInformationMessage("No stacks found.");
            return;
          }
          const pick = await vscode.window.showQuickPick(
            stacks.map((s) => ({
              label: s.name || s.hash,
              description: s.name ? s.hash : undefined,
              detail: `root: ${s.root}, ${s.branches.length} branch${s.branches.length === 1 ? "" : "es"}`,
              hash: s.hash,
              stackName: s.name,
            })),
            { placeHolder: "Select stack to rename" },
          );
          if (!pick) {
            return;
          }
          stackHash = pick.hash;
          currentName = pick.stackName;
        }

        const newName = await vscode.window.showInputBox({
          prompt: "Stack name (leave empty to clear)",
          placeHolder: "my-feature",
          value: currentName ?? "",
        });
        if (newName === undefined) {
          return;
        }

        await runWithFeedback(
          "Renaming stack...",
          newName
            ? `Renamed stack to "${newName}".`
            : `Cleared name for stack ${stackHash}.`,
          () => cli.renameStack(stackHash!, newName),
        );
      },
    ),
  );

  // ── Open Branch in Terminal ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.openBranchTerminal",
      (node?: BranchNode | RootRepoNode) => {
        let cwd: string | undefined;
        if (node instanceof BranchNode) {
          cwd = node.branch.worktree_path;
        } else if (node instanceof RootRepoNode) {
          cwd = node.repoPath;
        }
        if (cwd) {
          const terminal = vscode.window.createTerminal({ cwd });
          terminal.show();
        }
      },
    ),
  );

  // ── Open Agent ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.openAgent",
      (node?: StackNode | BranchNode) => {
        if (node instanceof StackNode) {
          cli.openAgent(node.stack.hash);
        } else if (node instanceof BranchNode) {
          cli.openAgentOnBranch(node.branch.name);
        } else {
          // No node context — let the CLI handle interactive selection
          cli.runInTerminal(["agent"]);
        }
      },
    ),
  );

  // ── Edit Agent Prompt ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.editAgentPrompt",
      async () => {
        const promptType = await vscode.window.showQuickPick(
          [
            { label: "Work Session Prompt", description: "Used when running 'ezs agent' on an existing stack", value: "work" as const },
            { label: "Feature Builder Prompt", description: "Used when running 'ezs agent feature'", value: "feature" as const },
          ],
          { placeHolder: "Select which agent prompt to edit" },
        );
        if (!promptType) {
          return;
        }

        try {
          const promptPath = await cli.ensureAgentPromptFile(promptType.value);
          const doc = await vscode.workspace.openTextDocument(vscode.Uri.file(promptPath));
          await vscode.window.showTextDocument(doc, { preview: false });
        } catch (e: unknown) {
          const msg = e instanceof Error ? e.message : String(e);
          vscode.window.showErrorMessage(`Failed to open agent prompt: ${msg}`);
        }
      },
    ),
  );

  // ── Build Feature with Agent ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.openAgentFeature",
      async (node?: StackNode) => {
        let stackHash: string | undefined;

        if (node instanceof StackNode) {
          stackHash = node.stack.hash;
        } else {
          // Pick a stack
          try {
            const stacks = await cli.listStacks(true);
            if (stacks.length === 0) {
              vscode.window.showInformationMessage("No stacks found. Create one with 'ezs new'.");
              return;
            }
            if (stacks.length === 1) {
              stackHash = stacks[0].hash;
            } else {
              const pick = await vscode.window.showQuickPick(
                stacks.map((s) => ({
                  label: s.name || s.hash,
                  description: s.name ? s.hash : undefined,
                  detail: `root: ${s.root}, ${s.branches.length} branch${s.branches.length === 1 ? "" : "es"}`,
                  hash: s.hash,
                })),
                { placeHolder: "Select stack to build feature on" },
              );
              if (!pick) {
                return;
              }
              stackHash = pick.hash;
            }
          } catch {
            vscode.window.showErrorMessage("Failed to list stacks.");
            return;
          }
        }

        const description = await vscode.window.showInputBox({
          prompt: "Describe the feature to build",
          placeHolder: "Add user authentication with JWT tokens",
        });
        if (!description) {
          return;
        }

        cli.openAgentFeature(stackHash!, description);
      },
    ),
  );

  // ── Fetch & Pull ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.gitPull",
      (node?: RootRepoNode) => {
        const cwd = node instanceof RootRepoNode ? node.repoPath : cli.getWorkspaceRoot();
        // Launch the user's default shell (don't hardcode `bash` — it
        // isn't always on the PATH that VS Code resolves for shellPath,
        // particularly on macOS GUI launches and Apple Silicon Homebrew
        // setups). sendText runs git via the user's interactive shell.
        const terminal = vscode.window.createTerminal({
          name: "ezstack: fetch & pull",
          cwd,
        });
        terminal.show();
        terminal.sendText("git fetch && git pull");
      },
    ),
  );

  // ── Doctor ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.doctor", async () => {
      try {
        const report = await vscode.window.withProgress(
          {
            location: vscode.ProgressLocation.Notification,
            title: "ezstack doctor...",
          },
          () => cli.doctor(),
        );
        outputChannel.clear();
        outputChannel.appendLine("ezstack doctor");
        outputChannel.appendLine("──────────────");
        outputChannel.appendLine(report);
        outputChannel.show();
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        vscode.window.showErrorMessage(`ezs doctor failed: ${msg}`);
      }
    }),
  );

  // ── Sync Stack with Options ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.syncStackOptions", async () => {
      const options = await vscode.window.showQuickPick(
        [
          { label: "Plain sync", description: "Rebase children onto parents" },
          { label: "Sync + stats", description: "Print commits-ahead summary after sync", picked: false },
          { label: "Sync + squash", description: "Squash each child to one commit before rebase" },
          { label: "Sync + stats + squash", description: "Both" },
        ],
        { placeHolder: "Choose sync mode" },
      );
      if (!options) {
        return;
      }
      const stats = options.label.includes("stats");
      const squash = options.label.includes("squash");
      await runWithFeedback(
        "Syncing stack...",
        "Stack synced.",
        () => cli.syncStack({ stats, squash }),
      );
    }),
  );

  // ── PR Draft-All ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.prDraftAll", async () => {
      const confirm = await vscode.window.showWarningMessage(
        "Create draft PRs for every branch in the current stack that doesn't have one?",
        { modal: true },
        "Create Draft PRs",
      );
      if (confirm !== "Create Draft PRs") {
        return;
      }
      await runWithFeedback(
        "Creating draft PRs across stack...",
        "Draft PRs created.",
        () => cli.prDraftAll(),
      );
    }),
  );

  // ── Delete Cascade ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.deleteCascade",
      async (node?: BranchNode) => {
        let branchName: string | undefined;
        if (node instanceof BranchNode) {
          branchName = node.branch.name;
        } else {
          const stacks = await cli.listStacks(true);
          const branches = stacks.flatMap((s) => s.branches);
          if (branches.length === 0) {
            vscode.window.showInformationMessage("No branches to delete.");
            return;
          }
          const pick = await vscode.window.showQuickPick(
            branches.map((b) => ({ label: b.name, detail: b.worktree_path })),
            { placeHolder: "Branch to delete (with all descendants)" },
          );
          if (!pick) {
            return;
          }
          branchName = pick.label;
        }
        const confirm = await vscode.window.showWarningMessage(
          `Cascade-delete "${branchName}" and all of its descendants? This removes their worktrees and branches.`,
          { modal: true },
          "Cascade Delete",
        );
        if (confirm !== "Cascade Delete") {
          return;
        }
        await runWithFeedback(
          `Cascade-deleting "${branchName}"...`,
          `Cascade-deleted "${branchName}".`,
          () => cli.deleteBranchCascade(branchName!),
        );
      },
    ),
  );

  // ── Config Export ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.configExport", async () => {
      const uri = await vscode.window.showSaveDialog({
        title: "Export ezstack config",
        filters: { "JSON": ["json"] },
        defaultUri: vscode.Uri.file("ezstack-config.json"),
      });
      if (!uri) {
        return;
      }
      await runWithFeedback(
        "Exporting config...",
        `Config exported to ${uri.fsPath}.`,
        () => cli.configExport(uri.fsPath),
      );
    }),
  );

  // ── Config Import ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.configImport", async () => {
      const uris = await vscode.window.showOpenDialog({
        title: "Import ezstack config",
        canSelectMany: false,
        canSelectFolders: false,
        filters: { "JSON": ["json"] },
      });
      if (!uris || uris.length === 0) {
        return;
      }
      const confirm = await vscode.window.showWarningMessage(
        `Import config from ${uris[0].fsPath}? This replaces your current ezstack config.`,
        { modal: true },
        "Import",
      );
      if (confirm !== "Import") {
        return;
      }
      await runWithFeedback(
        "Importing config...",
        "Config imported.",
        () => cli.configImport(uris[0].fsPath),
      );
    }),
  );

  // ── Push with Options ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.pushWithOptions",
      async (node?: BranchNode) => {
        const stackPick = await vscode.window.showQuickPick(
          [
            { label: "Current branch", value: "current" },
            { label: "Whole stack", value: "stack" },
          ],
          { placeHolder: "Push scope" },
        );
        if (!stackPick) {
          return;
        }
        const flagPicks = await vscode.window.showQuickPick(
          [
            { label: "--force", description: "Force-with-lease overwrite" },
            { label: "--verify", description: "Require pre-push hook to exist and pass" },
            { label: "--all-remotes", description: "Push to origin AND configured fork remote" },
          ],
          { placeHolder: "Optional flags (Tab to multi-select)", canPickMany: true },
        );
        if (!flagPicks) {
          return;
        }
        const flags = new Set(flagPicks.map((f) => f.label));
        const branchOverride =
          node instanceof BranchNode ? node.branch.name : undefined;
        await runWithFeedback(
          "Pushing...",
          "Push complete.",
          () =>
            cli.pushWithFlags({
              branch: stackPick.value === "current" ? branchOverride : undefined,
              stack: stackPick.value === "stack",
              force: flags.has("--force"),
              verify: flags.has("--verify"),
              allRemotes: flags.has("--all-remotes"),
            }),
        );
      },
    ),
  );

  // ── Open Agent with Options ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.openAgentWithOptions",
      async (node?: StackNode | BranchNode) => {
        let stackHash: string | undefined;
        if (node instanceof StackNode) {
          stackHash = node.stack.hash;
        } else if (node instanceof BranchNode) {
          stackHash = node.stackHash;
        } else {
          const stacks = await cli.listStacks();
          if (stacks.length === 0) {
            vscode.window.showInformationMessage("No stacks to open agent on.");
            return;
          }
          if (stacks.length === 1) {
            stackHash = stacks[0].hash;
          } else {
            const pick = await vscode.window.showQuickPick(
              stacks.map((s) => ({
                label: s.name || s.hash,
                detail: s.branches.map((b) => b.name).join(" → "),
                value: s.hash,
              })),
              { placeHolder: "Select stack" },
            );
            if (!pick) {
              return;
            }
            stackHash = pick.value;
          }
        }

        const noPushPick = await vscode.window.showQuickPick(
          [
            { label: "Allow push", value: false },
            { label: "Block push (--no-push)", value: true },
          ],
          { placeHolder: "Push gate" },
        );
        if (!noPushPick) {
          return;
        }

        const preset = await vscode.window.showInputBox({
          prompt: "Preset name (~/.ezstack/agent-presets/<name>.md), or empty for none",
          placeHolder: "reviewer",
        });
        if (preset === undefined) {
          return; // user cancelled
        }
        cli.openAgentWithFlags(stackHash!, {
          noPush: noPushPick.value,
          preset: preset || undefined,
        });
      },
    ),
  );
}
