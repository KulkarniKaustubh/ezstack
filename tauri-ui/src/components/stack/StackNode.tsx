import { useState, type DragEvent } from "react";
import { RefreshCw, Upload, GitPullRequest, ArrowRightLeft, Trash2, Bot } from "lucide-react";
import { cn } from "../../lib/utils";
import { BranchStatusBadge } from "../branch/BranchStatusBadge";
import { CIStatusBadge } from "../branch/CIStatusBadge";
import { ReviewBadge } from "../branch/ReviewBadge";
import { ContextMenu, useContextMenu, type ContextMenuItem } from "../ui/context-menu";
import type { StatusBranch } from "../../types/ezstack";

const DND_MIME = "application/x-ezstack-branch";

// Module-level ref for the branch currently being dragged. Needed because
// the HTML Drag-and-Drop spec blanks dataTransfer.getData() during dragover
// for security, so dropzones can't read the payload until drop fires. We
// fall back to this ref to decide whether to show the highlight.
let currentDragSource: string | null = null;

export interface StackNodeActions {
  onSync?: (branchName: string) => void;
  onPush?: (branchName: string) => void;
  onCreatePR?: (branchName: string) => void;
  onUpdatePR?: (branchName: string) => void;
  onReparent?: (branchName: string) => void;
  onReparentTo?: (branchName: string, newParent: string) => void;
  /**
   * Optional predicate consulted during dragover to decide whether the drop
   * would be valid. Invalid drops still receive drop events (so callers can
   * surface feedback) but the visual highlight is suppressed.
   */
  canReparentTo?: (branchName: string, newParent: string) => boolean;
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

  const canReceiveDrop = !!actions?.onReparentTo;

  const handleDragStart = (e: DragEvent<HTMLButtonElement>) => {
    e.dataTransfer.setData(DND_MIME, branch.name);
    e.dataTransfer.effectAllowed = "move";
    currentDragSource = branch.name;
  };

  const handleDragEnd = () => {
    currentDragSource = null;
  };

  const handleDragOver = (e: DragEvent<HTMLButtonElement>) => {
    if (!canReceiveDrop) return;
    if (!e.dataTransfer.types.includes(DND_MIME)) return;
    // If we know the source branch (same-window drag), pre-validate so
    // invalid targets never light up — the user gets immediate feedback
    // instead of a rejection toast on drop.
    if (
      currentDragSource &&
      actions?.canReparentTo &&
      !actions.canReparentTo(currentDragSource, branch.name)
    ) {
      e.dataTransfer.dropEffect = "none";
      if (dragOver) setDragOver(false);
      return;
    }
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    if (!dragOver) setDragOver(true);
  };

  // dragleave fires when the cursor crosses into a child element; ignore
  // those so the highlight doesn't flicker as the user moves over badges.
  const handleDragLeave = (e: DragEvent<HTMLButtonElement>) => {
    const related = e.relatedTarget as Node | null;
    if (related && e.currentTarget.contains(related)) return;
    setDragOver(false);
  };

  const handleDrop = (e: DragEvent<HTMLButtonElement>) => {
    setDragOver(false);
    if (!canReceiveDrop) return;
    const dragged = e.dataTransfer.getData(DND_MIME);
    if (!dragged) return;
    e.preventDefault();
    // The dragover handler suppresses highlight on invalid drops, but the
    // drop event can still fire (e.g. if canReparentTo was absent). The
    // caller is still responsible for final validation and user feedback.
    actions!.onReparentTo!(dragged, branch.name);
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
        draggable={canReceiveDrop}
        onDragStart={handleDragStart}
        onDragEnd={handleDragEnd}
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
