import * as vscode from "vscode";
import { EzsCli } from "../ezsCli";
import { StackTreeProvider, BranchNode } from "../views/stackTreeProvider";
import { StatusBarManager } from "../views/statusBarManager";

export function registerCommands(
  context: vscode.ExtensionContext,
  cli: EzsCli,
  treeProvider: StackTreeProvider,
  statusBar: StatusBarManager,
): void {
  const refreshAll = async () => {
    treeProvider.refresh();
    await statusBar.update();
  };

  const withProgress = <T>(
    title: string,
    fn: () => Promise<T>,
  ): Thenable<T> => {
    return vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title },
      fn,
    );
  };

  // ── Refresh ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.refresh", refreshAll),
  );

  // ── New Branch ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.newBranch", async () => {
      const name = await vscode.window.showInputBox({
        prompt: "New branch name",
        placeHolder: "feature-part-2",
      });
      if (!name) {
        return;
      }

      // Optionally pick a parent from the current stack
      let parent: string | undefined;
      try {
        const stacks = await cli.listStacks();
        const allBranches = stacks.flatMap((s) => [
          s.root,
          ...s.branches.map((b) => b.name),
        ]);
        const unique = [...new Set(allBranches)];
        if (unique.length > 0) {
          parent = await vscode.window.showQuickPick(unique, {
            placeHolder: "Select parent branch (or Esc for current branch)",
          });
        }
      } catch {
        // If listing fails, proceed without parent selection
      }

      await withProgress("Creating branch...", async () => {
        await cli.newBranch(name, parent);
      });
      await refreshAll();
    }),
  );

  // ── Sync ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.sync", async () => {
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
      // Sync is interactive (may have conflicts), so run in terminal
      cli.syncInteractive(mode.value);
    }),
  );

  // ── Push ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.push", async () => {
      await withProgress("Pushing branch...", () => cli.push());
      await refreshAll();
      vscode.window.showInformationMessage("Branch pushed.");
    }),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.pushStack", async () => {
      await withProgress("Pushing stack...", () => cli.pushStack());
      await refreshAll();
      vscode.window.showInformationMessage("Stack pushed.");
    }),
  );

  // ── PR Create ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.prCreate", async () => {
      const title = await vscode.window.showInputBox({
        prompt: "PR title",
        placeHolder: "Add feature X",
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

      await withProgress("Creating PR...", () =>
        cli.prCreate(title, draftPick?.value ?? false),
      );
      await refreshAll();
      vscode.window.showInformationMessage("PR created.");
    }),
  );

  // ── PR Update ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.prUpdate", async () => {
      await withProgress("Updating PR...", () => cli.prUpdate());
      await refreshAll();
      vscode.window.showInformationMessage("PR updated.");
    }),
  );

  // ── PR Merge ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.prMerge", async () => {
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

      await withProgress("Merging PR...", () => cli.prMerge(method.value));
      await refreshAll();
      vscode.window.showInformationMessage("PR merged.");
    }),
  );

  // ── PR Draft Toggle ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.prDraft", async () => {
      await withProgress("Toggling draft...", () => cli.prDraft());
      await refreshAll();
    }),
  );

  // ── PR Stack ──
  context.subscriptions.push(
    vscode.commands.registerCommand("ezstack.prStack", async () => {
      await withProgress("Updating stack info in PRs...", () => cli.prStack());
      await refreshAll();
      vscode.window.showInformationMessage("Stack info updated in PRs.");
    }),
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
        await vscode.commands.executeCommand("vscode.openFolder", uri, {
          forceNewWindow: false,
        });
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
          return;
        }
        // Find parent branch's worktree
        const parent = stacks
          .flatMap((s) => s.branches)
          .find((b) => b.name === current.parent);
        if (parent?.worktree_path) {
          const uri = vscode.Uri.file(parent.worktree_path);
          await vscode.commands.executeCommand("vscode.openFolder", uri, {
            forceNewWindow: false,
          });
        }
      } catch {
        // Silently fail — user can use goto instead
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
          return;
        }
        // Find children
        const children = stacks
          .flatMap((s) => s.branches)
          .filter((b) => b.parent === current.name);
        if (children.length === 0) {
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
          await vscode.commands.executeCommand("vscode.openFolder", uri, {
            forceNewWindow: false,
          });
        }
      } catch {
        // Silently fail
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
          // Pick from list
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

        await withProgress("Deleting branch...", () =>
          cli.deleteBranch(branchName!),
        );
        await refreshAll();
        vscode.window.showInformationMessage(`Deleted branch "${branchName}".`);
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

        // Build list of possible parents (all branches + roots, excluding self)
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

        await withProgress("Reparenting...", () =>
          cli.reparent(branchName!, newParent),
        );
        await refreshAll();
        vscode.window.showInformationMessage(
          `Reparented "${branchName}" onto "${newParent}".`,
        );
      },
    ),
  );

  // ── Open PR in Browser ──
  context.subscriptions.push(
    vscode.commands.registerCommand(
      "ezstack.openPR",
      async (node?: BranchNode) => {
        if (node?.branch.pr_url) {
          await vscode.env.openExternal(
            vscode.Uri.parse(node.branch.pr_url),
          );
        }
      },
    ),
  );

  // ── Open Worktree Folder ──
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
          forceNewWindow: false,
        });
      },
    ),
  );
}
