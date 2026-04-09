import { ScrollArea } from "../ui/scroll-area";
import { StackNode } from "./StackNode";
import { GitBranch } from "lucide-react";
import type { StatusStack } from "../../types/ezstack";
import { buildTree, type TreeNode } from "../../types/ezstack";

interface StackGraphProps {
  stack: StatusStack;
  selectedBranch: string | null;
  onSelectBranch: (name: string) => void;
}

function TreeNodeView({
  node,
  selectedBranch,
  onSelectBranch,
  isLast,
  depth,
}: {
  node: TreeNode;
  selectedBranch: string | null;
  onSelectBranch: (name: string) => void;
  isLast: boolean;
  depth: number;
}) {
  return (
    <div className="relative">
      <div className="flex">
        {/* Connector lines */}
        {depth > 0 && (
          <div className="relative w-6 shrink-0">
            {/* Vertical line from parent */}
            <div className="absolute left-3 top-0 w-px h-4 bg-border" />
            {/* Horizontal line to node */}
            <div className="absolute left-3 top-4 w-3 h-px bg-border" />
            {/* Continuing vertical line for siblings below */}
            {!isLast && <div className="absolute left-3 top-4 w-px h-full bg-border" />}
          </div>
        )}

        {/* Node content */}
        <div className="flex-1 min-w-0">
          <StackNode
            branch={node.branch}
            isSelected={node.branch.name === selectedBranch}
            onClick={() => onSelectBranch(node.branch.name)}
          />
        </div>
      </div>

      {/* Children */}
      {node.children.length > 0 && (
        <div className={depth > 0 ? "ml-6" : "ml-0"}>
          {node.children.map((child, i) => (
            <TreeNodeView
              key={child.branch.name}
              node={child}
              selectedBranch={selectedBranch}
              onSelectBranch={onSelectBranch}
              isLast={i === node.children.length - 1}
              depth={depth + 1}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export function StackGraph({ stack, selectedBranch, onSelectBranch }: StackGraphProps) {
  const tree = buildTree(stack);
  const displayName = stack.name || stack.hash.slice(0, 7);

  return (
    <div className="flex flex-col h-full">
      {/* Stack header */}
      <div className="flex items-center gap-2 px-4 py-3 border-b">
        <GitBranch className="h-4 w-4 text-muted-foreground" />
        <h2 className="text-sm font-semibold">{displayName}</h2>
        <span className="text-xs text-muted-foreground font-mono">({stack.hash.slice(0, 7)})</span>
        <div className="text-xs text-muted-foreground ml-auto">
          base: <span className="font-mono text-foreground">{stack.root}</span>
        </div>
      </div>

      {/* Tree */}
      <ScrollArea className="flex-1 p-4">
        {tree.length === 0 ? (
          <div className="text-center text-sm text-muted-foreground py-12">
            <p>No branches in this stack</p>
          </div>
        ) : (
          <div className="space-y-0.5">
            {/* Root indicator */}
            <div className="flex items-center gap-2 px-3 py-1.5 text-xs text-muted-foreground">
              <div className="h-1.5 w-1.5 rounded-full bg-muted-foreground/50" />
              <span className="font-mono">{stack.root}</span>
              <span className="text-muted-foreground/50">(base)</span>
            </div>
            {tree.map((node, i) => (
              <TreeNodeView
                key={node.branch.name}
                node={node}
                selectedBranch={selectedBranch}
                onSelectBranch={onSelectBranch}
                isLast={i === tree.length - 1}
                depth={1}
              />
            ))}
          </div>
        )}
      </ScrollArea>
    </div>
  );
}
