import { Plus, Layers, Bot } from "lucide-react";
import { Button } from "../ui/button";
import { StackColumn } from "./StackColumn";
import type { StackNodeActions } from "./StackNode";
import type { StatusStack } from "../../types/ezstack";

interface StacksBoardProps {
  stacks: StatusStack[];
  selectedBranch: string | null;
  onSelectBranch: (stackHash: string, branchName: string) => void;
  onRenameStack: (stackHash: string) => void;
  onAddBranchToStack: (stackHash: string) => void;
  onSyncStack?: (stackHash: string) => void;
  onNewBranch: () => void;
  onOpenAgent?: (stackHash: string) => void;
  onBuildFeature?: () => void;
  actions?: StackNodeActions;
}

export function StacksBoard({
  stacks,
  selectedBranch,
  onSelectBranch,
  onRenameStack,
  onAddBranchToStack,
  onSyncStack,
  onNewBranch,
  onOpenAgent,
  onBuildFeature,
  actions,
}: StacksBoardProps) {
  if (stacks.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-center p-8">
        <Layers className="h-12 w-12 text-muted-foreground/30 mb-4" />
        <h2 className="text-lg font-semibold mb-1">No Stacks</h2>
        <p className="text-sm text-muted-foreground mb-4 max-w-sm">
          Create your first branch to start building a stack.
        </p>
        <Button onClick={onNewBranch} className="gap-1.5">
          <Plus className="h-4 w-4" />
          New Branch
        </Button>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      {/* Board header */}
      <div className="flex items-center justify-between px-4 py-2.5 border-b">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Layers className="h-3.5 w-3.5" />
          <span>{stacks.length} stack{stacks.length !== 1 ? "s" : ""}</span>
        </div>
        <div className="flex items-center gap-2">
          {onBuildFeature && (
            <Button variant="outline" size="sm" onClick={onBuildFeature} className="gap-1.5 text-xs">
              <Bot className="h-3.5 w-3.5" />
              Build Feature
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={onNewBranch} className="gap-1.5 text-xs">
            <Plus className="h-3.5 w-3.5" />
            New Branch
          </Button>
        </div>
      </div>

      {/* Horizontal scrolling board */}
      <div className="flex-1 overflow-x-auto overflow-y-hidden p-4">
        <div className="flex gap-4 h-full items-start">
          {stacks.map((stack) => (
            <StackColumn
              key={stack.hash}
              stack={stack}
              selectedBranch={selectedBranch}
              onSelectBranch={onSelectBranch}
              onRename={onRenameStack}
              onAddBranch={onAddBranchToStack}
              onSyncStack={onSyncStack}
              onOpenAgent={onOpenAgent}
              actions={actions}
            />
          ))}
        </div>
      </div>
    </div>
  );
}
