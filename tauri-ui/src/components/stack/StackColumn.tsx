import { Plus, Pencil, RefreshCcw, Bot } from "lucide-react";
import { Button } from "../ui/button";
import { Tooltip } from "../ui/tooltip";
import { StackNode, type StackNodeActions } from "./StackNode";
import { ContextMenu, useContextMenu } from "../ui/context-menu";
import { cn } from "../../lib/utils";
import type { StatusStack, TreeNode } from "../../types/ezstack";
import { buildTree } from "../../types/ezstack";

interface StackColumnProps {
  stack: StatusStack;
  selectedBranch: string | null;
  onSelectBranch: (stackHash: string, branchName: string) => void;
  onRename: (stackHash: string) => void;
  onAddBranch: (stackHash: string) => void;
  onSyncStack?: (stackHash: string) => void;
  onOpenAgent?: (stackHash: string) => void;
  actions?: StackNodeActions;
}

function getStackHealthColor(stack: StatusStack): string {
  const hasFailing = stack.branches.some((b) => b.ci_state === "failure");
  const hasMerged = stack.branches.some((b) => b.pr_state === "MERGED");
  const hasOpen = stack.branches.some((b) => b.pr_state === "OPEN");

  if (hasFailing) return "bg-destructive";
  if (hasMerged) return "bg-purple-500";
  if (hasOpen) return "bg-success";
  return "bg-muted-foreground/50";
}

function TreeNodeView({
  node,
  selectedBranch,
  onSelectBranch,
  stackHash,
  isLast,
  depth,
  actions,
}: {
  node: TreeNode;
  selectedBranch: string | null;
  onSelectBranch: (stackHash: string, branchName: string) => void;
  stackHash: string;
  isLast: boolean;
  depth: number;
  actions?: StackNodeActions;
}) {
  return (
    <div className="relative">
      <div className="flex">
        {depth > 0 && (
          <div className="relative w-5 shrink-0">
            <div className="absolute left-2.5 top-0 w-px h-4 bg-border" />
            <div className="absolute left-2.5 top-4 w-2.5 h-px bg-border" />
            {!isLast && <div className="absolute left-2.5 top-4 w-px h-full bg-border" />}
          </div>
        )}
        <div className="flex-1 min-w-0">
          <StackNode
            branch={node.branch}
            isSelected={node.branch.name === selectedBranch}
            onClick={() => onSelectBranch(stackHash, node.branch.name)}
            actions={actions}
          />
        </div>
      </div>
      {node.children.length > 0 && (
        <div className={depth > 0 ? "ml-5" : "ml-0"}>
          {node.children.map((child, i) => (
            <TreeNodeView
              key={child.branch.name}
              node={child}
              selectedBranch={selectedBranch}
              onSelectBranch={onSelectBranch}
              stackHash={stackHash}
              isLast={i === node.children.length - 1}
              depth={depth + 1}
              actions={actions}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export function StackColumn({ stack, selectedBranch, onSelectBranch, onRename, onAddBranch, onSyncStack, onOpenAgent, actions }: StackColumnProps) {
  const tree = buildTree(stack);
  const displayName = stack.name || stack.hash.slice(0, 7);
  const { position, onContextMenu, onClose } = useContextMenu();

  const headerMenuItems = [
    ...(onSyncStack ? [{ label: "Sync All in Stack", icon: <RefreshCcw />, onClick: () => onSyncStack(stack.hash) }] : []),
    ...(onOpenAgent ? [{ label: "Open Agent", icon: <Bot />, onClick: () => onOpenAgent(stack.hash) }] : []),
    { label: "Rename Stack", icon: <Pencil />, onClick: () => onRename(stack.hash) },
    { label: "Add Branch", icon: <Plus />, onClick: () => onAddBranch(stack.hash) },
  ];

  return (
    <div className="flex flex-col w-72 shrink-0 rounded-lg border bg-card">
      {/* Stack header */}
      <div className="flex items-center gap-2 px-3 py-2.5 border-b" onContextMenu={onContextMenu}>
        <div className={cn("h-2 w-2 rounded-full shrink-0", getStackHealthColor(stack))} />
        <div className="min-w-0 flex-1">
          <div className="text-sm font-semibold truncate">{displayName}</div>
          <div className="text-[10px] text-muted-foreground font-mono">
            {stack.root} &middot; {stack.branches.length} branch{stack.branches.length !== 1 ? "es" : ""}
          </div>
        </div>
        <div className="flex items-center gap-0.5 shrink-0">
          <Tooltip content="Rename stack">
            <Button variant="ghost" size="icon-sm" onClick={() => onRename(stack.hash)}>
              <Pencil className="h-3 w-3" />
            </Button>
          </Tooltip>
          <Tooltip content="Add branch to stack">
            <Button variant="ghost" size="icon-sm" onClick={() => onAddBranch(stack.hash)}>
              <Plus className="h-3 w-3" />
            </Button>
          </Tooltip>
        </div>
      </div>

      {/* Tree */}
      <div className="flex-1 overflow-y-auto p-2">
        {tree.length === 0 ? (
          <div className="text-center text-xs text-muted-foreground py-6">No branches</div>
        ) : (
          <div className="space-y-0.5">
            {/* Root indicator */}
            <div className="flex items-center gap-1.5 px-2 py-1 text-[10px] text-muted-foreground">
              <div className="h-1.5 w-1.5 rounded-full bg-muted-foreground/40" />
              <span className="font-mono">{stack.root}</span>
            </div>
            {tree.map((node, i) => (
              <TreeNodeView
                key={node.branch.name}
                node={node}
                selectedBranch={selectedBranch}
                onSelectBranch={onSelectBranch}
                stackHash={stack.hash}
                isLast={i === tree.length - 1}
                depth={1}
                actions={actions}
              />
            ))}
          </div>
        )}
      </div>

      <ContextMenu items={headerMenuItems} position={position} onClose={onClose} />
    </div>
  );
}
