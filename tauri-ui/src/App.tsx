import { useEffect, useState, useCallback } from "react";
import { open } from "@tauri-apps/plugin-dialog";
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
import { ConnectRemoteDialog } from "./components/operations/ConnectRemoteDialog";
import * as ezs from "./commands/ezs";

type DialogState =
  | { type: "none" }
  | { type: "new-branch" }
  | { type: "sync"; branch?: string }
  | { type: "delete"; branch: string }
  | { type: "pr-create"; branch: string }
  | { type: "pr-merge"; branch: string; prNumber: number }
  | { type: "reparent"; branch: string }
  | { type: "settings" }
  | { type: "connect-remote" };

export default function App() {
  const {
    repoPath,
    setRepoPath,
    stacks,
    selectedStackHash,
    selectedBranchName,
    currentBranch,
    isLoading,
    error,
    lastRefresh,
    operationOutput,
    operationLoading,
    remoteConnection,
    selectStack,
    selectBranch,
    setOperationOutput,
    setRemoteConnection,
    clearRepoState,
  } = useAppStore();

  const { refresh } = useStacks();
  const { run } = useOperation();
  const [dialog, setDialog] = useState<DialogState>({ type: "none" });
  const [connectError, setConnectError] = useState<string | null>(null);
  const [connectLoading, setConnectLoading] = useState(false);

  // Auto-detect repo path on mount (only when local)
  useEffect(() => {
    if (!repoPath && !remoteConnection) {
      ezs.getRepoPath(".").then(setRepoPath).catch(() => {});
    }
  }, [repoPath, remoteConnection, setRepoPath]);

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

  const handleSelectRepo = useCallback(async () => {
    const selected = await open({ directory: true, multiple: false });
    if (selected) {
      try {
        const path = await ezs.getRepoPath(selected as string);
        setRepoPath(path);
      } catch {
        setRepoPath(selected as string);
      }
    }
  }, [setRepoPath]);

  const handleConnectRemote = useCallback(
    async (host: string, user: string, port: number, keyPath: string, remotePath: string) => {
      setConnectError(null);
      setConnectLoading(true);
      try {
        const resolvedPath = await ezs.connectRemote(host, user, port, keyPath, remotePath);
        setRemoteConnection({ host, user, port, key_path: keyPath, remote_repo_path: resolvedPath });
        setRepoPath(resolvedPath);
        setDialog({ type: "none" });
      } catch (e) {
        setConnectError(String(e));
      } finally {
        setConnectLoading(false);
      }
    },
    [setRemoteConnection, setRepoPath],
  );

  const handleDisconnectRemote = useCallback(async () => {
    try {
      await ezs.disconnectRemote();
    } catch {
      // Ignore errors during disconnect
    }
    setRemoteConnection(null);
    clearRepoState();
  }, [setRemoteConnection, clearRepoState]);

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
  const branchActions = selectedBranch && repoPath
    ? {
        onSync: () => setDialog({ type: "sync", branch: selectedBranch.name }),
        onPush: () => runAndRefresh(() => ezs.pushBranch(repoPath!)),
        onCreatePR: () => setDialog({ type: "pr-create", branch: selectedBranch.name }),
        onUpdatePR: () => runAndRefresh(() => ezs.prUpdate(repoPath!, selectedBranch.name)),
        onMergePR: () =>
          selectedBranch.pr_number
            ? setDialog({ type: "pr-merge", branch: selectedBranch.name, prNumber: selectedBranch.pr_number })
            : undefined,
        onToggleDraft: () => runAndRefresh(() => ezs.prToggleDraft(repoPath!, selectedBranch.name)),
        onDelete: () => setDialog({ type: "delete", branch: selectedBranch.name }),
        onReparent: () => setDialog({ type: "reparent", branch: selectedBranch.name }),
        onUpdateStack: () => runAndRefresh(() => ezs.prUpdateStack(repoPath!)),
      }
    : null;

  return (
    <div className="flex flex-col h-screen">
      <TitleBar
        onRefresh={refresh}
        onSettings={() => setDialog({ type: "settings" })}
        onConnectRemote={() => setDialog({ type: "connect-remote" })}
        onDisconnectRemote={handleDisconnectRemote}
        isLoading={isLoading}
        isRemote={remoteConnection !== null}
      />

      <div className="flex flex-1 min-h-0">
        {!repoPath ? (
          <EmptyState
            type="no-repo"
            onSelectRepo={handleSelectRepo}
            onConnectRemote={() => setDialog({ type: "connect-remote" })}
          />
        ) : (
          <>
            <Sidebar
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

      <StatusBar
        repoPath={repoPath}
        currentBranch={currentBranch}
        lastRefresh={lastRefresh}
        remoteConnection={remoteConnection}
        onDisconnect={remoteConnection ? handleDisconnectRemote : undefined}
      />

      {/* Dialogs */}
      <NewBranchDialog
        open={dialog.type === "new-branch"}
        onOpenChange={(open) => !open && setDialog({ type: "none" })}
        stacks={stacks}
        isLoading={operationLoading}
        onSubmit={async (name, parent) => {
          await runAndRefresh(() => ezs.createBranch(repoPath!, name, parent));
          setDialog({ type: "none" });
        }}
      />

      <SyncDialog
        open={dialog.type === "sync"}
        onOpenChange={(open) => !open && setDialog({ type: "none" })}
        branchName={dialog.type === "sync" ? dialog.branch : undefined}
        isLoading={operationLoading}
        onSubmit={async (scope) => {
          await runAndRefresh(() => ezs.syncBranch(repoPath!, scope));
          setDialog({ type: "none" });
        }}
      />

      {dialog.type === "delete" && (
        <DeleteDialog
          open
          onOpenChange={(open) => !open && setDialog({ type: "none" })}
          branchName={dialog.branch}
          isLoading={operationLoading}
          onSubmit={async (force) => {
            await runAndRefresh(() => ezs.deleteBranch(repoPath!, dialog.branch, force));
            selectBranch(null);
            setDialog({ type: "none" });
          }}
        />
      )}

      {dialog.type === "pr-create" && (
        <PRCreateDialog
          open
          onOpenChange={(open) => !open && setDialog({ type: "none" })}
          branchName={dialog.branch}
          isLoading={operationLoading}
          onSubmit={async (title, body, draft) => {
            await runAndRefresh(() => ezs.prCreate(repoPath!, title, body || undefined, draft, dialog.branch));
            setDialog({ type: "none" });
          }}
        />
      )}

      {dialog.type === "pr-merge" && (
        <PRMergeDialog
          open
          onOpenChange={(open) => !open && setDialog({ type: "none" })}
          branchName={dialog.branch}
          prNumber={dialog.prNumber}
          isLoading={operationLoading}
          onSubmit={async (method) => {
            await runAndRefresh(() => ezs.prMerge(repoPath!, method, dialog.branch));
            setDialog({ type: "none" });
          }}
        />
      )}

      {dialog.type === "reparent" && (
        <ReparentDialog
          open
          onOpenChange={(open) => !open && setDialog({ type: "none" })}
          branchName={dialog.branch}
          stacks={stacks}
          isLoading={operationLoading}
          onSubmit={async (newParent) => {
            await runAndRefresh(() => ezs.reparentBranch(repoPath!, dialog.branch, newParent));
            setDialog({ type: "none" });
          }}
        />
      )}

      <ConnectRemoteDialog
        open={dialog.type === "connect-remote"}
        onOpenChange={(open) => {
          if (!open) {
            setDialog({ type: "none" });
            setConnectError(null);
          }
        }}
        onSubmit={handleConnectRemote}
        isLoading={connectLoading}
        error={connectError}
      />
    </div>
  );
}
