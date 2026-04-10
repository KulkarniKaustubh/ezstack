import { RefreshCw, Upload, GitPullRequest, ArrowRightLeft, Trash2 } from "lucide-react";
import { cn } from "../../lib/utils";
import { BranchStatusBadge } from "../branch/BranchStatusBadge";
import { CIStatusBadge } from "../branch/CIStatusBadge";
import { ReviewBadge } from "../branch/ReviewBadge";
import { ContextMenu, useContextMenu, type ContextMenuItem } from "../ui/context-menu";
import type { StatusBranch } from "../../types/ezstack";

export interface StackNodeActions {
  onSync?: (branchName: string) => void;
  onPush?: (branchName: string) => void;
  onCreatePR?: (branchName: string) => void;
  onUpdatePR?: (branchName: string) => void;
  onReparent?: (branchName: string) => void;
  onDelete?: (branchName: string) => void;
}

interface StackNodeProps {
  branch: StatusBranch;
  isSelected: boolean;
  onClick: () => void;
  actions?: StackNodeActions;
}

export function StackNode({ branch, isSelected, onClick, actions }: StackNodeProps) {
  const { position, onContextMenu, onClose } = useContextMenu();

  const isMerged = branch.is_merged || branch.pr_state === "MERGED" || branch.pr_state === "CLOSED";
  const hasPR = !!branch.pr_number;

  const menuItems: ContextMenuItem[] = [];
  if (actions) {
    if (actions.onSync) {
      menuItems.push({ label: "Sync", icon: <RefreshCw />, onClick: () => actions.onSync!(branch.name) });
    }
    if (actions.onPush) {
      menuItems.push({ label: "Push", icon: <Upload />, onClick: () => actions.onPush!(branch.name) });
    }
    if (!hasPR && !isMerged && actions.onCreatePR) {
      menuItems.push({ label: "Create PR", icon: <GitPullRequest />, onClick: () => actions.onCreatePR!(branch.name) });
    }
    if (hasPR && !isMerged && actions.onUpdatePR) {
      menuItems.push({ label: "Update PR", icon: <Upload />, onClick: () => actions.onUpdatePR!(branch.name) });
    }
    if (actions.onReparent) {
      menuItems.push({ label: "Reparent", icon: <ArrowRightLeft />, onClick: () => actions.onReparent!(branch.name), separator: true });
    }
    if (!isMerged && actions.onDelete) {
      menuItems.push({ label: "Delete", icon: <Trash2 />, onClick: () => actions.onDelete!(branch.name), danger: true, separator: menuItems.length > 0 && !menuItems[menuItems.length - 1].separator });
    }
  }

  return (
    <>
      <button
        onClick={onClick}
        onContextMenu={actions && menuItems.length > 0 ? onContextMenu : undefined}
        className={cn(
          "flex items-center gap-2 px-3 py-2 rounded-lg w-full text-left transition-all",
          "border hover:border-primary/30",
          isSelected
            ? "bg-accent border-primary/40 shadow-sm"
            : "border-transparent hover:bg-accent/50",
          branch.is_current && "ring-1 ring-info/50",
        )}
      >
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span
              className={cn(
                "text-sm font-mono truncate",
                branch.is_current && "font-bold text-info",
                (branch.is_merged || branch.pr_state === "CLOSED") && "line-through text-muted-foreground",
              )}
            >
              {branch.name}
            </span>
            {branch.is_current && (
              <span className="text-[9px] uppercase tracking-wider text-info font-semibold">current</span>
            )}
          </div>
          {branch.pr_number && (
            <span className="text-xs text-muted-foreground font-mono">#{branch.pr_number}</span>
          )}
        </div>
        <div className="flex items-center gap-1.5 shrink-0">
          <ReviewBadge state={branch.review_state} />
          <CIStatusBadge state={branch.ci_state} summary={branch.ci_summary} />
          <BranchStatusBadge branch={branch} />
          {(branch.additions || branch.deletions) ? (
            <span className="text-[10px] font-mono whitespace-nowrap">
              {branch.additions ? <span className="text-success">+{branch.additions}</span> : null}
              {branch.additions && branch.deletions ? " " : null}
              {branch.deletions ? <span className="text-destructive">-{branch.deletions}</span> : null}
            </span>
          ) : null}
        </div>
      </button>
      {menuItems.length > 0 && (
        <ContextMenu items={menuItems} position={position} onClose={onClose} />
      )}
    </>
  );
}
