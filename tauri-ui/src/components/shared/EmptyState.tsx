import { Layers, FolderOpen } from "lucide-react";
import { Button } from "../ui/button";

interface EmptyStateProps {
  type: "no-repo" | "no-stacks" | "no-selection";
  onSelectRepo?: () => void;
  onNewBranch?: () => void;
}

export function EmptyState({ type, onSelectRepo, onNewBranch }: EmptyStateProps) {
  if (type === "no-repo") {
    return (
      <div className="flex flex-col items-center justify-center h-full text-center p-8">
        <FolderOpen className="h-12 w-12 text-muted-foreground/30 mb-4" />
        <h2 className="text-lg font-semibold mb-1">Welcome to ezstack</h2>
        <p className="text-sm text-muted-foreground mb-4 max-w-sm">
          Open a git repository to start managing your stacked pull requests.
        </p>
        {onSelectRepo && (
          <Button onClick={onSelectRepo}>Open Repository</Button>
        )}
      </div>
    );
  }

  if (type === "no-stacks") {
    return (
      <div className="flex flex-col items-center justify-center h-full text-center p-8">
        <Layers className="h-12 w-12 text-muted-foreground/30 mb-4" />
        <h2 className="text-lg font-semibold mb-1">No Stacks</h2>
        <p className="text-sm text-muted-foreground mb-4 max-w-sm">
          Create your first branch to start building a stack.
        </p>
        {onNewBranch && (
          <Button onClick={onNewBranch}>Create Branch</Button>
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-col items-center justify-center h-full text-center p-8">
      <Layers className="h-10 w-10 text-muted-foreground/20 mb-3" />
      <p className="text-sm text-muted-foreground">Select a stack to view its branches</p>
    </div>
  );
}
