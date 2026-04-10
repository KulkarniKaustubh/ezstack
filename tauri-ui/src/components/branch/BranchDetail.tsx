import { ExternalLink, GitBranch, Folder, X } from "lucide-react";
import { open } from "@tauri-apps/plugin-shell";
import { Button } from "../ui/button";
import { Badge } from "../ui/badge";
import { ScrollArea } from "../ui/scroll-area";
import { Card, CardContent } from "../ui/card";
import { BranchStatusBadge } from "./BranchStatusBadge";
import { CIStatusBadge } from "./CIStatusBadge";
import { ReviewBadge } from "./ReviewBadge";
import { BranchActions } from "./BranchActions";
import type { StatusBranch } from "../../types/ezstack";

interface BranchDetailProps {
  branch: StatusBranch;
  onClose: () => void;
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
  width?: number;
}

export function BranchDetail({
  branch,
  onClose,
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
  width,
}: BranchDetailProps) {
  return (
    <div className="flex flex-col h-full border-l bg-surface/30" style={width ? { width } : { width: 320 }}>
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b">
        <div className="flex items-center gap-2 min-w-0">
          <GitBranch className="h-4 w-4 text-muted-foreground shrink-0" />
          <span className="text-sm font-semibold font-mono truncate">{branch.name}</span>
        </div>
        <Button variant="ghost" size="icon-sm" onClick={onClose}>
          <X className="h-3.5 w-3.5" />
        </Button>
      </div>

      <ScrollArea className="flex-1 p-4 space-y-4">
        {/* Status overview */}
        <Card>
          <CardContent className="p-3 space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-xs text-muted-foreground">PR Status</span>
              <div className="flex items-center gap-2">
                <BranchStatusBadge branch={branch} />
                {branch.pr_number && (
                  <button
                    onClick={() => branch.pr_url && open(branch.pr_url)}
                    className="flex items-center gap-1 text-xs text-info hover:underline"
                  >
                    #{branch.pr_number}
                    <ExternalLink className="h-3 w-3" />
                  </button>
                )}
              </div>
            </div>

            {branch.ci_state && (
              <div className="flex items-center justify-between">
                <span className="text-xs text-muted-foreground">CI</span>
                <div className="flex items-center gap-2">
                  <CIStatusBadge state={branch.ci_state} summary={branch.ci_summary} />
                  {branch.ci_summary && (
                    <span className="text-xs text-muted-foreground">{branch.ci_summary}</span>
                  )}
                </div>
              </div>
            )}

            {branch.review_state && (
              <div className="flex items-center justify-between">
                <span className="text-xs text-muted-foreground">Review</span>
                <div className="flex items-center gap-2">
                  <ReviewBadge state={branch.review_state} />
                  <span className="text-xs">{formatReviewState(branch.review_state)}</span>
                </div>
              </div>
            )}

            {branch.mergeable && (
              <div className="flex items-center justify-between">
                <span className="text-xs text-muted-foreground">Merge</span>
                <Badge
                  variant={
                    branch.mergeable === "MERGEABLE"
                      ? "success"
                      : branch.mergeable === "CONFLICTING"
                        ? "destructive"
                        : "muted"
                  }
                >
                  {branch.mergeable.toLowerCase()}
                </Badge>
              </div>
            )}
          </CardContent>
        </Card>

        {/* Details */}
        <Card>
          <CardContent className="p-3 space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-xs text-muted-foreground">Parent</span>
              <span className="text-xs font-mono">{branch.parent}</span>
            </div>
            {branch.worktree_path && (
              <div className="flex items-center justify-between">
                <span className="text-xs text-muted-foreground">Worktree</span>
                <div className="flex items-center gap-1 text-xs font-mono truncate max-w-[180px]">
                  <Folder className="h-3 w-3 shrink-0" />
                  {branch.worktree_path.split("/").pop()}
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        {/* Actions */}
        <BranchActions
          branch={branch}
          onSync={onSync}
          onPush={onPush}
          onPushStack={onPushStack}
          onCreatePR={onCreatePR}
          onUpdatePR={onUpdatePR}
          onMergePR={onMergePR}
          onToggleDraft={onToggleDraft}
          onDelete={onDelete}
          onReparent={onReparent}
          onUpdateStack={onUpdateStack}
          isLoading={isLoading}
        />
      </ScrollArea>
    </div>
  );
}

function formatReviewState(state: string): string {
  switch (state) {
    case "APPROVED":
      return "Approved";
    case "CHANGES_REQUESTED":
      return "Changes requested";
    case "REVIEW_REQUIRED":
      return "Review required";
    default:
      return state;
  }
}
