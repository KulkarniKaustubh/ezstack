import { cn } from "../../lib/utils";
import { BranchStatusBadge } from "../branch/BranchStatusBadge";
import { CIStatusBadge } from "../branch/CIStatusBadge";
import { ReviewBadge } from "../branch/ReviewBadge";
import type { StatusBranch } from "../../types/ezstack";

interface StackNodeProps {
  branch: StatusBranch;
  isSelected: boolean;
  onClick: () => void;
}

export function StackNode({ branch, isSelected, onClick }: StackNodeProps) {
  return (
    <button
      onClick={onClick}
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
              branch.is_merged && "line-through text-muted-foreground",
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
      </div>
    </button>
  );
}
