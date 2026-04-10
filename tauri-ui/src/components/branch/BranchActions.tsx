import {
  RefreshCw,
  Upload,
  GitPullRequest,
  GitMerge,
  FileEdit,
  Trash2,
  ArrowRightLeft,
  Layers,
} from "lucide-react";
import { Button } from "../ui/button";
import { Tooltip } from "../ui/tooltip";
import type { StatusBranch } from "../../types/ezstack";

interface BranchActionsProps {
  branch: StatusBranch;
  onSync: () => void;
  onPush: () => void;
  onPushStack: () => void;
  onCreatePR: () => void;
  onUpdatePR: () => void;
  onMergePR: () => void;
  onToggleDraft: () => void;
  onDelete: () => void;
  onReparent: () => void;
  onUpdateStack: () => void;
  isLoading: boolean;
}

export function BranchActions({
  branch,
  onSync,
  onPush,
  onPushStack,
  onCreatePR,
  onUpdatePR,
  onMergePR,
  onToggleDraft,
  onDelete,
  onReparent,
  onUpdateStack,
  isLoading,
}: BranchActionsProps) {
  const hasPR = !!branch.pr_number;
  const isMerged = branch.is_merged || branch.pr_state === "MERGED";

  return (
    <div className="space-y-2">
      <div className="text-xs font-medium text-muted-foreground uppercase tracking-wider">Actions</div>
      <div className="grid grid-cols-2 gap-1.5">
        <Tooltip content="Sync this branch">
          <Button variant="outline" size="sm" onClick={onSync} disabled={isLoading} className="w-full justify-start">
            <RefreshCw className="h-3.5 w-3.5" />
            Sync
          </Button>
        </Tooltip>

        <Tooltip content="Push to remote">
          <Button variant="outline" size="sm" onClick={onPush} disabled={isLoading} className="w-full justify-start">
            <Upload className="h-3.5 w-3.5" />
            Push
          </Button>
        </Tooltip>

        <Tooltip content="Push all branches in stack">
          <Button variant="outline" size="sm" onClick={onPushStack} disabled={isLoading} className="w-full justify-start">
            <Upload className="h-3.5 w-3.5" />
            Push Stack
          </Button>
        </Tooltip>

        {!hasPR && !isMerged && (
          <Tooltip content="Create a pull request">
            <Button variant="outline" size="sm" onClick={onCreatePR} disabled={isLoading} className="w-full justify-start col-span-2">
              <GitPullRequest className="h-3.5 w-3.5" />
              Create PR
            </Button>
          </Tooltip>
        )}

        {hasPR && !isMerged && (
          <>
            <Tooltip content="Push and update PR">
              <Button variant="outline" size="sm" onClick={onUpdatePR} disabled={isLoading} className="w-full justify-start">
                <Upload className="h-3.5 w-3.5" />
                Update PR
              </Button>
            </Tooltip>

            <Tooltip content={branch.pr_state === "DRAFT" ? "Mark as ready" : "Convert to draft"}>
              <Button variant="outline" size="sm" onClick={onToggleDraft} disabled={isLoading} className="w-full justify-start">
                <FileEdit className="h-3.5 w-3.5" />
                {branch.pr_state === "DRAFT" ? "Ready" : "Draft"}
              </Button>
            </Tooltip>

            <Tooltip content="Merge this PR">
              <Button
                variant="outline"
                size="sm"
                onClick={onMergePR}
                disabled={isLoading}
                className="w-full justify-start text-success hover:text-success"
              >
                <GitMerge className="h-3.5 w-3.5" />
                Merge PR
              </Button>
            </Tooltip>
          </>
        )}

        <Tooltip content="Update stack descriptions">
          <Button variant="outline" size="sm" onClick={onUpdateStack} disabled={isLoading} className="w-full justify-start">
            <Layers className="h-3.5 w-3.5" />
            Stack PRs
          </Button>
        </Tooltip>

        <Tooltip content="Change parent branch">
          <Button variant="outline" size="sm" onClick={onReparent} disabled={isLoading} className="w-full justify-start">
            <ArrowRightLeft className="h-3.5 w-3.5" />
            Reparent
          </Button>
        </Tooltip>

        {!isMerged && (
          <Tooltip content="Delete this branch">
            <Button
              variant="outline"
              size="sm"
              onClick={onDelete}
              disabled={isLoading}
              className="w-full justify-start text-destructive hover:text-destructive"
            >
              <Trash2 className="h-3.5 w-3.5" />
              Delete
            </Button>
          </Tooltip>
        )}
      </div>
    </div>
  );
}
