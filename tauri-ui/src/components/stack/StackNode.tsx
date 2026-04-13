import { useState, type DragEvent } from "react";
import { RefreshCw, Upload, GitPullRequest, ArrowRightLeft, Trash2, Bot } from "lucide-react";
import { cn } from "../../lib/utils";
import { BranchStatusBadge } from "../branch/BranchStatusBadge";
import { CIStatusBadge } from "../branch/CIStatusBadge";
import { ReviewBadge } from "../branch/ReviewBadge";
import { ContextMenu, useContextMenu, type ContextMenuItem } from "../ui/context-menu";
import type { StatusBranch } from "../../types/ezstack";

const DND_MIME = "application/x-ezstack-branch";

export interface StackNodeActions {
  onSync?: (branchName: string) => void;
  onPush?: (branchName: string) => void;
  onCreatePR?: (branchName: string) => void;
  onUpdatePR?: (branchName: string) => void;
  onReparent?: (branchName: string) => void;
  onReparentTo?: (branchName: string, newParent: string) => void;
  onOpenAgent?: (branchName: string) => void;
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
  const [dragOver, setDragOver] = useState(false);

  const handleDragStart = (e: DragEvent<HTMLButtonElement>) => {
    e.dataTransfer.setData(DND_MIME, branch.name);
    e.dataTransfer.effectAllowed = "move";
  };

  const handleDragOver = (e: DragEvent<HTMLButtonElement>) => {
    if (!actions?.onReparentTo) return;
    if (!e.dataTransfer.types.includes(DND_MIME)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    if (!dragOver) setDragOver(true);
  };

  const handleDragLeave = () => setDragOver(false);

  const handleDrop = (e: DragEvent<HTMLButtonElement>) => {
    setDragOver(false);
    if (!actions?.onReparentTo) return;
    const dragged = e.dataTransfer.getData(DND_MIME);
    if (!dragged || dragged === branch.name) return;
    e.preventDefault();
    actions.onReparentTo(dragged, branch.name);
  };

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
    if (actions.onOpenAgent) {
      menuItems.push({ label: "Open Agent", icon: <Bot />, onClick: () => actions.onOpenAgent!(branch.name) });
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
        draggable
        onDragStart={handleDragStart}
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
        className={cn(
          "flex items-center gap-2 px-3 py-2 rounded-lg w-full text-left transition-all",
          "border hover:border-primary/30",
          isSelected
            ? "bg-accent border-primary/40 shadow-sm"
            : "border-transparent hover:bg-accent/50",
          branch.is_current && "ring-1 ring-info/50",
          dragOver && "border-primary bg-primary/10",
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
