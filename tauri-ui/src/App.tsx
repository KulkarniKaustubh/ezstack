import { useEffect, useState, useCallback } from "react";
import { useAppStore } from "./store/app-store";
import { useStacks } from "./hooks/use-stacks";
import { useOperation } from "./hooks/use-operation";
import { useResizable } from "./hooks/use-resizable";
import { TitleBar } from "./components/layout/TitleBar";
import { Sidebar } from "./components/layout/Sidebar";
import { StatusBar } from "./components/layout/StatusBar";
import { ResizeHandle } from "./components/ui/resize-handle";
import { StacksBoard } from "./components/stack/StacksBoard";
import { BranchDetail } from "./components/branch/BranchDetail";
import { EmptyState } from "./components/shared/EmptyState";
import { OperationOutput } from "./components/shared/OperationOutput";
import { NewBranchDialog } from "./components/operations/NewBranchDialog";
import { SyncDialog } from "./components/operations/SyncDialog";
import { DeleteDialog } from "./components/operations/DeleteDialog";
import { PRCreateDialog } from "./components/operations/PRCreateDialog";
import { PRMergeDialog } from "./components/operations/PRMergeDialog";
import { ReparentDialog } from "./components/operations/ReparentDialog";
import { RenameStackDialog } from "./components/operations/RenameStackDialog";
import { SettingsDialog } from "./components/operations/SettingsDialog";
import { AgentFeatureDialog } from "./components/operations/AgentFeatureDialog";
import { ConnectRemoteDialog } from "./components/operations/ConnectRemoteDialog";
import type { StackNodeActions } from "./components/stack/StackNode";
import * as ezs from "./commands/ezs";

type DialogState =
  | { type: "none" }
  | { type: "new-branch"; forStackHash?: string }
  | { type: "sync"; branch?: string }
  | { type: "delete"; branch: string }
  | { type: "pr-create"; branch: string }
  | { type: "pr-merge"; branch: string; prNumber: number }
  | { type: "reparent"; branch: string }
  | { type: "rename-stack"; stackHash: string }
  | { type: "settings" }
  | { type: "agent-feature" }
  | { type: "connect-remote" };

export default function App() {
  const {
    repos,
    selectedRepoPath,
    selectRepo,
    stacks,
    selectedStackHash,
    selectedBranchName,
    focusedBranchIndex,
    currentBranch,
    initialLoading,
    isLoading,
    error,
    lastRefresh,
    operationOutput,
    operationLoading,
    operationSuccess,
    selectStack,
    selectBranch,
    setFocusedBranchIndex,
    setOperationOutput,
  } = useAppStore();

  const { refresh } = useStacks();
  const { run } = useOperation();
  const [dialog, setDialog] = useState<DialogState>({ type: "none" });
  const [isConnected, setIsConnected] = useState(false);
  const [connectError, setConnectError] = useState<string | null>(null);

  // Resizable sidebar
  const sidebar = useResizable({
    initialWidth: 192,
    minWidth: 120,
    maxWidth: 360,
    side: "left",
    storageKey: "ezstack-sidebar-width",
  });

  // Resizable detail panel
  const detail = useResizable({
    initialWidth: 320,
    minWidth: 240,
    maxWidth: 500,
    side: "right",
    storageKey: "ezstack-detail-width",
  });

  const selectedStack = stacks.find((s) => s.hash === selectedStackHash);
  const selectedBranch_ = selectedStack?.branches.find((b) => b.name === selectedBranchName);

  // Keyboard shortcuts
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement;
      if (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.tagName === "SELECT") return;

      if (e.metaKey && e.key === "r") {
        e.preventDefault();
        refresh();
        return;
      }
      if (e.metaKey && e.key === "n") {
        e.preventDefault();
        setDialog({ type: "new-branch" });
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        selectBranch(null);
        return;
      }

      // Arrow key navigation within the selected stack's branches
      if (selectedStack && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
        e.preventDefault();
        const branches = selectedStack.branches;
        if (branches.length === 0) return;
        const newIndex = e.key === "ArrowDown"
          ? Math.min(focusedBranchIndex + 1, branches.length - 1)
          : Math.max(focusedBranchIndex - 1, 0);
        setFocusedBranchIndex(newIndex);
        selectBranch(branches[newIndex].name);
        return;
      }

      // Left/Right or [/] to navigate between stacks
      if (stacks.length > 0 && (e.key === "ArrowLeft" || e.key === "ArrowRight" || e.key === "[" || e.key === "]")) {
        e.preventDefault();
        const currentIdx = stacks.findIndex((s) => s.hash === selectedStackHash);
        let newIdx: number;
        if (e.key === "ArrowRight" || e.key === "]") {
          newIdx = currentIdx < stacks.length - 1 ? currentIdx + 1 : 0;
        } else {
          newIdx = currentIdx > 0 ? currentIdx - 1 : stacks.length - 1;
        }
        selectStack(stacks[newIdx].hash);
        return;
      }

      // Enter to select the focused branch
      if (e.key === "Enter" && selectedStack) {
        e.preventDefault();
        const branches = selectedStack.branches;
        if (branches.length > 0 && focusedBranchIndex < branches.length) {
          selectBranch(branches[focusedBranchIndex].name);
        }
        return;
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [refresh, stacks, selectedStack, selectedStackHash, focusedBranchIndex, selectStack, selectBranch, setFocusedBranchIndex]);

  const runAndRefresh = useCallback(
    async (op: () => Promise<ezs.CommandResult>) => {
      await run(op);
      refresh();
    },
    [run, refresh],
  );

  const handleSelectBranch = useCallback(
    (stackHash: string, branchName: string) => {
      selectStack(stackHash);
      selectBranch(branchName);
      // Sync focused index
      const stack = stacks.find((s) => s.hash === stackHash);
      if (stack) {
        const idx = stack.branches.findIndex((b) => b.name === branchName);
        if (idx >= 0) setFocusedBranchIndex(idx);
      }
    },
    [selectStack, selectBranch, setFocusedBranchIndex, stacks],
  );

  // Build action handlers for the selected branch
  const branchActions = selectedBranch_ && selectedRepoPath
    ? {
        onSync: () => setDialog({ type: "sync", branch: selectedBranch_.name }),
        onPush: () => runAndRefresh(() => ezs.pushBranch(selectedRepoPath)),
        onPushStack: () => runAndRefresh(() => ezs.pushBranch(selectedRepoPath, true)),
        onCreatePR: () => setDialog({ type: "pr-create", branch: selectedBranch_.name }),
        onUpdatePR: () => runAndRefresh(() => ezs.prUpdate(selectedRepoPath, selectedBranch_.name)),
        onMergePR: () =>
          selectedBranch_.pr_number
            ? setDialog({ type: "pr-merge", branch: selectedBranch_.name, prNumber: selectedBranch_.pr_number })
            : undefined,
        onToggleDraft: () => runAndRefresh(() => ezs.prToggleDraft(selectedRepoPath, selectedBranch_.name)),
        onDelete: () => setDialog({ type: "delete", branch: selectedBranch_.name }),
        onReparent: () => setDialog({ type: "reparent", branch: selectedBranch_.name }),
        onUpdateStack: () => runAndRefresh(() => ezs.prUpdateStack(selectedRepoPath)),
        onOpenAgent: () => {
          const stack = stacks.find((s) => s.branches.some((b) => b.name === selectedBranchName));
          if (stack && selectedRepoPath) {
            ezs.openAgent(selectedRepoPath, stack.hash, selectedBranchName ?? undefined);
          }
        },
      }
    : null;

  // Context menu actions for stack nodes (passed down to all branches)
  const stackNodeActions: StackNodeActions | undefined = selectedRepoPath
    ? {
        onSync: (branchName) => setDialog({ type: "sync", branch: branchName }),
        onPush: () => runAndRefresh(() => ezs.pushBranch(selectedRepoPath)),
        onCreatePR: (branchName) => setDialog({ type: "pr-create", branch: branchName }),
        onUpdatePR: (branchName) => runAndRefresh(() => ezs.prUpdate(selectedRepoPath, branchName)),
        onOpenAgent: (branchName) => {
          const stack = stacks.find((s) => s.branches.some((b) => b.name === branchName));
          if (stack) ezs.openAgent(selectedRepoPath, stack.hash, branchName);
        },
        onReparent: (branchName) => setDialog({ type: "reparent", branch: branchName }),
        onDelete: (branchName) => setDialog({ type: "delete", branch: branchName }),
      }
    : undefined;

  const handleOpenAgentOnStack = useCallback(
    (stackHash: string) => {
      if (!selectedRepoPath) return;
      ezs.openAgent(selectedRepoPath, stackHash);
    },
    [selectedRepoPath],
  );

  const handleSyncStack = useCallback(
    (_stackHash: string) => {
      if (!selectedRepoPath) return;
      runAndRefresh(() => ezs.syncBranch(selectedRepoPath, "stack"));
    },
    [selectedRepoPath, runAndRefresh],
  );

  const isAnyResizing = sidebar.isResizing || detail.isResizing;

  // Splash screen while loading all repos
  if (initialLoading) {
    return (
      <div className="flex flex-col h-screen bg-background">
        <div
          className="flex items-center h-12 px-4 border-b bg-background/80 backdrop-blur-sm select-none"
          data-tauri-drag-region
        >
          <span className="text-sm font-semibold tracking-tight">ezstack</span>
        </div>
        <div className="flex-1 flex flex-col items-center justify-center gap-4">
          <div className="flex items-center gap-3">
            <div className="h-5 w-5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
            <span className="text-sm text-muted-foreground">Loading repositories...</span>
          </div>
        </div>
      </div>
    );
  }

  // Resolve the stack for "add branch to stack" dialog
  const forStack = dialog.type === "new-branch" && dialog.forStackHash
    ? stacks.find((s) => s.hash === dialog.forStackHash)
    : undefined;

  return (
    <div className={`flex flex-col h-screen ${isAnyResizing ? "select-none" : ""}`}>
      <TitleBar
        onRefresh={refresh}
        onSync={() => setDialog({ type: "sync" })}
        onSettings={() => setDialog({ type: "settings" })}
        isLoading={isLoading}
        isConnected={isConnected}
        onConnectRemote={() => setDialog({ type: "connect-remote" })}
        onDisconnectRemote={async () => {
          await ezs.disconnectRemote();
          setIsConnected(false);
        }}
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
              width={sidebar.width}
            />
            <ResizeHandle onMouseDown={sidebar.handleMouseDown} isResizing={sidebar.isResizing} />

            <div className="flex flex-1 min-w-0">
              {/* Center: All stacks as columns */}
              <div className="flex-1 min-w-0 flex flex-col">
                {error && (
                  <div className="px-4 py-2 bg-destructive/10 text-destructive text-sm border-b">
                    {error}
                  </div>
                )}

                <StacksBoard
                  stacks={stacks}
                  selectedBranch={selectedBranchName}
                  onSelectBranch={handleSelectBranch}
                  onRenameStack={(hash) => setDialog({ type: "rename-stack", stackHash: hash })}
                  onAddBranchToStack={(hash) => setDialog({ type: "new-branch", forStackHash: hash })}
                  onSyncStack={handleSyncStack}
                  onNewBranch={() => setDialog({ type: "new-branch" })}
                  onOpenAgent={handleOpenAgentOnStack}
                  onBuildFeature={() => setDialog({ type: "agent-feature" })}
                  actions={stackNodeActions}
                />

                {/* Operation output panel */}
                {operationOutput && (
                  <OperationOutput
                    output={operationOutput}
                    isLoading={operationLoading}
                    isSuccess={operationSuccess}
                    onClose={() => setOperationOutput(null)}
                  />
                )}
              </div>

              {/* Right: Branch Detail */}
              {selectedBranch_ && branchActions && (
                <>
                  <ResizeHandle onMouseDown={detail.handleMouseDown} isResizing={detail.isResizing} />
                  <BranchDetail
                    branch={selectedBranch_}
                    onClose={() => selectBranch(null)}
                    isLoading={operationLoading}
                    width={detail.width}
                    {...branchActions}
                  />
                </>
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
            forStack={forStack}
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

          <AgentFeatureDialog
            open={dialog.type === "agent-feature"}
            onOpenChange={(o) => !o && setDialog({ type: "none" })}
            stacks={stacks}
            selectedStackHash={selectedStackHash}
            onSubmit={async (stackHash, description) => {
              await ezs.openAgentFeature(selectedRepoPath, stackHash, description);
              setDialog({ type: "none" });
            }}
          />

          {dialog.type === "rename-stack" && (
            <RenameStackDialog
              open
              onOpenChange={(o) => !o && setDialog({ type: "none" })}
              stackHash={dialog.stackHash}
              currentName={stacks.find((s) => s.hash === dialog.stackHash)?.name || ""}
              isLoading={operationLoading}
              onSubmit={async (name) => {
                await runAndRefresh(() => ezs.renameStack(selectedRepoPath, dialog.stackHash, name));
                setDialog({ type: "none" });
              }}
            />
          )}
        </>
      )}

      <ConnectRemoteDialog
        open={dialog.type === "connect-remote"}
        onOpenChange={(o) => {
          if (!o) {
            setDialog({ type: "none" });
            setConnectError(null);
          }
        }}
        isLoading={operationLoading}
        error={connectError}
        onConnect={async (host, user, port, keyPath) => {
          return ezs.connectRemote(host, user, port, keyPath);
        }}
        onSelectRepo={async (host, user, port, keyPath, repoPath) => {
          setConnectError(null);
          try {
            await ezs.selectRemoteRepo(host, user, port, keyPath, repoPath);
            setIsConnected(true);
            setDialog({ type: "none" });
          } catch (e) {
            setConnectError(e instanceof Error ? e.message : String(e));
            throw e;
          }
        }}
      />

      <SettingsDialog
        open={dialog.type === "settings"}
        onOpenChange={(o) => !o && setDialog({ type: "none" })}
        repos={repos}
        selectedRepoPath={selectedRepoPath}
      />
    </div>
  );
}
