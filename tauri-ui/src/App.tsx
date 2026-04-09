import { useEffect, useState, useCallback } from "react";
import { useAppStore } from "./store/app-store";
import { useStacks } from "./hooks/use-stacks";
import { useOperation } from "./hooks/use-operation";
import { TitleBar } from "./components/layout/TitleBar";
import { Sidebar } from "./components/layout/Sidebar";
import { StatusBar } from "./components/layout/StatusBar";
import { StackGraph } from "./components/stack/StackGraph";
import { BranchDetail } from "./components/branch/BranchDetail";
import { EmptyState } from "./components/shared/EmptyState";
import { OperationOutput } from "./components/shared/OperationOutput";
import { NewBranchDialog } from "./components/operations/NewBranchDialog";
import { SyncDialog } from "./components/operations/SyncDialog";
import { DeleteDialog } from "./components/operations/DeleteDialog";
import { PRCreateDialog } from "./components/operations/PRCreateDialog";
import { PRMergeDialog } from "./components/operations/PRMergeDialog";
import { ReparentDialog } from "./components/operations/ReparentDialog";
import * as ezs from "./commands/ezs";

type DialogState =
  | { type: "none" }
  | { type: "new-branch" }
  | { type: "sync"; branch?: string }
  | { type: "delete"; branch: string }
  | { type: "pr-create"; branch: string }
  | { type: "pr-merge"; branch: string; prNumber: number }
  | { type: "reparent"; branch: string }
  | { type: "settings" };

export default function App() {
  const {
    repos,
    setRepos,
    selectedRepoPath,
    selectRepo,
    stacks,
    selectedStackHash,
    selectedBranchName,
    currentBranch,
    isLoading,
    error,
    lastRefresh,
    operationOutput,
    operationLoading,
    selectStack,
    selectBranch,
    setOperationOutput,
  } = useAppStore();

  const { refresh } = useStacks();
  const { run } = useOperation();
  const [dialog, setDialog] = useState<DialogState>({ type: "none" });

  // Load all ezstack repos on mount
  useEffect(() => {
    ezs.getEzstackRepos().then((r) => {
      setRepos(r);
      // Auto-select first repo if we have repos
      if (r.length > 0 && !selectedRepoPath) {
        selectRepo(r[0].repo_path);
      }
    }).catch(() => {});
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Keyboard shortcuts
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.metaKey && e.key === "r") {
        e.preventDefault();
        refresh();
      }
      if (e.metaKey && e.key === "n") {
        e.preventDefault();
        setDialog({ type: "new-branch" });
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [refresh, setDialog]);

  const selectedStack = stacks.find((s) => s.hash === selectedStackHash);
  const selectedBranch = selectedStack?.branches.find((b) => b.name === selectedBranchName);

  const runAndRefresh = useCallback(
    async (op: () => Promise<ezs.CommandResult>) => {
      await run(op);
      refresh();
    },
    [run, refresh],
  );

  // Build action handlers for the selected branch
  const branchActions = selectedBranch && selectedRepoPath
    ? {
        onSync: () => setDialog({ type: "sync", branch: selectedBranch.name }),
        onPush: () => runAndRefresh(() => ezs.pushBranch(selectedRepoPath)),
        onCreatePR: () => setDialog({ type: "pr-create", branch: selectedBranch.name }),
        onUpdatePR: () => runAndRefresh(() => ezs.prUpdate(selectedRepoPath, selectedBranch.name)),
        onMergePR: () =>
          selectedBranch.pr_number
            ? setDialog({ type: "pr-merge", branch: selectedBranch.name, prNumber: selectedBranch.pr_number })
            : undefined,
        onToggleDraft: () => runAndRefresh(() => ezs.prToggleDraft(selectedRepoPath, selectedBranch.name)),
        onDelete: () => setDialog({ type: "delete", branch: selectedBranch.name }),
        onReparent: () => setDialog({ type: "reparent", branch: selectedBranch.name }),
        onUpdateStack: () => runAndRefresh(() => ezs.prUpdateStack(selectedRepoPath)),
      }
    : null;

  return (
    <div className="flex flex-col h-screen">
      <TitleBar
        onRefresh={refresh}
        onSettings={() => setDialog({ type: "settings" })}
        isLoading={isLoading}
      />

      <div className="flex flex-1 min-h-0">
        {repos.length === 0 && !selectedRepoPath ? (
          <EmptyState type="no-repo" />
        ) : (
          <>
            <Sidebar
              repos={repos}
              selectedRepoPath={selectedRepoPath}
              onSelectRepo={selectRepo}
              stacks={stacks}
              selectedStackHash={selectedStackHash}
              onSelectStack={selectStack}
              onNewBranch={() => setDialog({ type: "new-branch" })}
            />

            <div className="flex flex-1 min-w-0">
              {/* Center: Stack Graph */}
              <div className="flex-1 min-w-0 flex flex-col">
                {error && (
                  <div className="px-4 py-2 bg-destructive/10 text-destructive text-sm border-b">
                    {error}
                  </div>
                )}
                {selectedStack ? (
                  <StackGraph
                    stack={selectedStack}
                    selectedBranch={selectedBranchName}
                    onSelectBranch={selectBranch}
                  />
                ) : (
                  <EmptyState
                    type={stacks.length === 0 ? "no-stacks" : "no-selection"}
                    onNewBranch={() => setDialog({ type: "new-branch" })}
                  />
                )}

                {/* Operation output panel */}
                {operationOutput && (
                  <OperationOutput
                    output={operationOutput}
                    isLoading={operationLoading}
                    onClose={() => setOperationOutput(null)}
                  />
                )}
              </div>

              {/* Right: Branch Detail */}
              {selectedBranch && branchActions && (
                <BranchDetail
                  branch={selectedBranch}
                  onClose={() => selectBranch(null)}
                  isLoading={operationLoading}
                  {...branchActions}
                />
              )}
            </div>
          </>
        )}
      </div>

      <StatusBar repoPath={selectedRepoPath} currentBranch={currentBranch} lastRefresh={lastRefresh} />

      {/* Dialogs */}
      {selectedRepoPath && (
        <>
          <NewBranchDialog
            open={dialog.type === "new-branch"}
            onOpenChange={(o) => !o && setDialog({ type: "none" })}
            stacks={stacks}
            isLoading={operationLoading}
            onSubmit={async (name, parent) => {
              await runAndRefresh(() => ezs.createBranch(selectedRepoPath, name, parent));
              setDialog({ type: "none" });
            }}
          />

          <SyncDialog
            open={dialog.type === "sync"}
            onOpenChange={(o) => !o && setDialog({ type: "none" })}
            branchName={dialog.type === "sync" ? dialog.branch : undefined}
            isLoading={operationLoading}
            onSubmit={async (scope) => {
              await runAndRefresh(() => ezs.syncBranch(selectedRepoPath, scope));
              setDialog({ type: "none" });
            }}
          />

          {dialog.type === "delete" && (
            <DeleteDialog
              open
              onOpenChange={(o) => !o && setDialog({ type: "none" })}
              branchName={dialog.branch}
              isLoading={operationLoading}
              onSubmit={async (force) => {
                await runAndRefresh(() => ezs.deleteBranch(selectedRepoPath, dialog.branch, force));
                selectBranch(null);
                setDialog({ type: "none" });
              }}
            />
          )}

          {dialog.type === "pr-create" && (
            <PRCreateDialog
              open
              onOpenChange={(o) => !o && setDialog({ type: "none" })}
              branchName={dialog.branch}
              isLoading={operationLoading}
              onSubmit={async (title, body, draft) => {
                await runAndRefresh(() => ezs.prCreate(selectedRepoPath, title, body || undefined, draft, dialog.branch));
                setDialog({ type: "none" });
              }}
            />
          )}

          {dialog.type === "pr-merge" && (
            <PRMergeDialog
              open
              onOpenChange={(o) => !o && setDialog({ type: "none" })}
              branchName={dialog.branch}
              prNumber={dialog.prNumber}
              isLoading={operationLoading}
              onSubmit={async (method) => {
                await runAndRefresh(() => ezs.prMerge(selectedRepoPath, method, dialog.branch));
                setDialog({ type: "none" });
              }}
            />
          )}

          {dialog.type === "reparent" && (
            <ReparentDialog
              open
              onOpenChange={(o) => !o && setDialog({ type: "none" })}
              branchName={dialog.branch}
              stacks={stacks}
              isLoading={operationLoading}
              onSubmit={async (newParent) => {
                await runAndRefresh(() => ezs.reparentBranch(selectedRepoPath, dialog.branch, newParent));
                setDialog({ type: "none" });
              }}
            />
          )}
        </>
      )}
    </div>
  );
}
