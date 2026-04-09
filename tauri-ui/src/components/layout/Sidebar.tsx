import { GitBranch, Plus, Layers } from "lucide-react";
import { Button } from "../ui/button";
import { ScrollArea } from "../ui/scroll-area";
import { Tooltip } from "../ui/tooltip";
import { cn } from "../../lib/utils";
import type { StatusStack } from "../../types/ezstack";

interface SidebarProps {
  stacks: StatusStack[];
  selectedStackHash: string | null;
  onSelectStack: (hash: string) => void;
  onNewBranch: () => void;
}

function getStackHealthColor(stack: StatusStack): string {
  const hasMerged = stack.branches.some((b) => b.pr_state === "MERGED");
  const hasFailing = stack.branches.some((b) => b.ci_state === "failure");
  const hasDraft = stack.branches.some((b) => b.pr_state === "DRAFT");
  const hasOpen = stack.branches.some((b) => b.pr_state === "OPEN");

  if (hasFailing) return "bg-destructive";
  if (hasMerged) return "bg-purple-500";
  if (hasOpen) return "bg-success";
  if (hasDraft) return "bg-muted-foreground";
  return "bg-muted-foreground/50";
}

export function Sidebar({ stacks, selectedStackHash, onSelectStack, onNewBranch }: SidebarProps) {
  return (
    <div className="flex flex-col h-full w-60 border-r bg-surface/50">
      <div className="flex items-center justify-between px-3 py-2.5 border-b">
        <div className="flex items-center gap-1.5 text-sm font-medium text-muted-foreground">
          <Layers className="h-3.5 w-3.5" />
          <span>Stacks</span>
          <span className="text-xs text-muted-foreground/60">({stacks.length})</span>
        </div>
        <Tooltip content="New branch (Cmd+N)">
          <Button variant="ghost" size="icon-sm" onClick={onNewBranch}>
            <Plus className="h-3.5 w-3.5" />
          </Button>
        </Tooltip>
      </div>

      <ScrollArea className="flex-1 py-1">
        {stacks.length === 0 ? (
          <div className="px-3 py-8 text-center text-sm text-muted-foreground">
            <GitBranch className="h-8 w-8 mx-auto mb-2 opacity-30" />
            <p>No stacks yet</p>
            <p className="text-xs mt-1">Create a branch to get started</p>
          </div>
        ) : (
          stacks.map((stack) => {
            const displayName = stack.name || stack.hash.slice(0, 7);
            const isSelected = stack.hash === selectedStackHash;
            const currentBranch = stack.branches.find((b) => b.is_current);

            return (
              <button
                key={stack.hash}
                onClick={() => onSelectStack(stack.hash)}
                className={cn(
                  "w-full text-left px-3 py-2 transition-colors",
                  isSelected
                    ? "bg-accent text-accent-foreground"
                    : "hover:bg-accent/50 text-foreground",
                )}
              >
                <div className="flex items-center gap-2">
                  <div className={cn("h-2 w-2 rounded-full shrink-0", getStackHealthColor(stack))} />
                  <div className="min-w-0 flex-1">
                    <div className="text-sm font-medium truncate">{displayName}</div>
                    <div className="flex items-center gap-1.5 text-xs text-muted-foreground mt-0.5">
                      <span className="font-mono">{stack.root}</span>
                      <span>&middot;</span>
                      <span>
                        {stack.branches.length} branch{stack.branches.length !== 1 ? "es" : ""}
                      </span>
                    </div>
                    {currentBranch && (
                      <div className="text-[10px] text-info font-mono mt-0.5 truncate">
                        {currentBranch.name}
                      </div>
                    )}
                  </div>
                </div>
              </button>
            );
          })
        )}
      </ScrollArea>
    </div>
  );
}
