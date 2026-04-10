import { Dialog, DialogContent, DialogHeader, DialogTitle } from "../ui/dialog";
import { Button } from "../ui/button";
import type { RepoConfig } from "../../types/ezstack";
import { APP_VERSION } from "../../version";

interface SettingsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  repos: RepoConfig[];
  selectedRepoPath: string | null;
}

export function SettingsDialog({ open, onOpenChange, repos, selectedRepoPath }: SettingsDialogProps) {
  const currentRepo = repos.find((r) => r.repo_path === selectedRepoPath);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
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
