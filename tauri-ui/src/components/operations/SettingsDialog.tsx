import { useState } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "../ui/dialog";
import { Button } from "../ui/button";
import type { RepoConfig } from "../../types/ezstack";
import { APP_VERSION } from "../../version";
import * as ezs from "../../commands/ezs";

interface SettingsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  repos: RepoConfig[];
  selectedRepoPath: string | null;
}

export function SettingsDialog({ open, onOpenChange, repos, selectedRepoPath }: SettingsDialogProps) {
  const currentRepo = repos.find((r) => r.repo_path === selectedRepoPath);
  const [promptContent, setPromptContent] = useState<string | null>(null);
  const [promptLoading, setPromptLoading] = useState(false);
  const [promptError, setPromptError] = useState<string | null>(null);
  const [resetMessage, setResetMessage] = useState<string | null>(null);

  const handleViewPrompts = async () => {
    if (!selectedRepoPath) return;
    setPromptLoading(true);
    setPromptError(null);
    setResetMessage(null);
    try {
      const content = await ezs.getAgentPrompts(selectedRepoPath);
      setPromptContent(content);
    } catch (e) {
      setPromptError(e instanceof Error ? e.message : String(e));
    } finally {
      setPromptLoading(false);
    }
  };

  const handleEditPrompts = async (which: "work" | "feature" | "both") => {
    if (!selectedRepoPath) return;
    try {
      await ezs.editAgentPrompts(selectedRepoPath, which);
    } catch (e) {
      setPromptError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleResetPrompts = async (which: "work" | "feature" | "both") => {
    if (!selectedRepoPath) return;
    setPromptError(null);
    try {
      await ezs.resetAgentPrompts(selectedRepoPath, which);
      const label = which === "both" ? "all prompts" : `${which} prompt`;
      setResetMessage(`Reset ${label} to default`);
      // Refresh view if prompts are shown
      if (promptContent !== null) {
        const content = await ezs.getAgentPrompts(selectedRepoPath);
        setPromptContent(content);
      }
      setTimeout(() => setResetMessage(null), 3000);
    } catch (e) {
      setPromptError(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => {
      if (!o) {
        setPromptContent(null);
        setPromptError(null);
        setResetMessage(null);
      }
      onOpenChange(o);
    }}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>Settings</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div>
            <div className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
              ezstack Config
            </div>
            <div className="rounded-lg border p-3 space-y-2 text-sm">
              <div className="flex justify-between">
                <span className="text-muted-foreground">Config path</span>
                <span className="font-mono text-xs">~/.ezstack/config.json</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Repositories</span>
                <span>{repos.length}</span>
              </div>
            </div>
          </div>

          {currentRepo && (
            <div>
              <div className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
                Current Repository
              </div>
              <div className="rounded-lg border p-3 space-y-2 text-sm">
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Path</span>
                  <span className="font-mono text-xs truncate max-w-[250px]">{currentRepo.repo_path}</span>
                </div>
                <div className="flex justify-between">
                  <span className="text-muted-foreground">Worktree dir</span>
                  <span className="font-mono text-xs truncate max-w-[250px]">{currentRepo.worktree_base_dir || "—"}</span>
                </div>
                {currentRepo.sync_strategy && (
                  <div className="flex justify-between">
                    <span className="text-muted-foreground">Sync strategy</span>
                    <span>{currentRepo.sync_strategy}</span>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* Agent Prompts */}
          {selectedRepoPath && (
            <div>
              <div className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
                Agent Prompts
              </div>
              <div className="rounded-lg border p-3 space-y-3 text-sm">
                <p className="text-muted-foreground text-xs">
                  Customize the prompts sent to AI agents when using work session or feature builder mode.
                </p>
                <div className="flex flex-wrap gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={handleViewPrompts}
                    disabled={promptLoading}
                  >
                    {promptLoading ? "Loading..." : promptContent !== null ? "Refresh" : "View Prompts"}
                  </Button>
                  <Button variant="outline" size="sm" onClick={() => handleEditPrompts("work")}>
                    Edit Work Prompt
                  </Button>
                  <Button variant="outline" size="sm" onClick={() => handleEditPrompts("feature")}>
                    Edit Feature Prompt
                  </Button>
                  <Button variant="outline" size="sm" onClick={() => handleResetPrompts("both")}>
                    Reset to Defaults
                  </Button>
                </div>
                {promptError && (
                  <p className="text-destructive text-xs">{promptError}</p>
                )}
                {resetMessage && (
                  <p className="text-green-600 dark:text-green-400 text-xs">{resetMessage}</p>
                )}
                {promptContent !== null && (
                  <pre className="mt-2 p-3 rounded bg-muted text-xs font-mono whitespace-pre-wrap max-h-[300px] overflow-y-auto">
                    {promptContent}
                  </pre>
                )}
              </div>
            </div>
          )}

          <div>
            <div className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
              About
            </div>
            <div className="rounded-lg border p-3 space-y-2 text-sm">
              <div className="flex justify-between">
                <span className="text-muted-foreground">Version</span>
                <span className="font-mono">v{APP_VERSION}</span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Framework</span>
                <span>Tauri v2 + React</span>
              </div>
            </div>
          </div>
        </div>

        <div className="flex justify-end mt-4">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
