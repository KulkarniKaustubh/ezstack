import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import type { StatusStack } from "../../types/ezstack";

interface NewBranchDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  stacks: StatusStack[];
  forStack?: StatusStack;
  onSubmit: (name: string, parent?: string) => void;
  isLoading: boolean;
}

export function NewBranchDialog({ open, onOpenChange, stacks, forStack, onSubmit, isLoading }: NewBranchDialogProps) {
  const [name, setName] = useState("");
  const [parent, setParent] = useState("");

  useEffect(() => {
    if (!open) { setName(""); setParent(""); }
  }, [open]);

  // If scoped to a stack, only show that stack's branches as parents
  const sourceStacks = forStack ? [forStack] : stacks;
  const allBranches = sourceStacks.flatMap((s) => [s.root, ...s.branches.map((b) => b.name)]);
  const uniqueBranches = [...new Set(allBranches)];

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    onSubmit(name.trim(), parent || undefined);
    setName("");
    setParent("");
  };

  const title = forStack
    ? `Add Branch to ${forStack.name || forStack.hash.slice(0, 7)}`
    : "Create New Branch";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {forStack
              ? "Create a new branch and add it to this stack."
              : "Create a new branch with an optional parent."}
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <label className="text-sm font-medium">Branch name</label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-feature"
              autoFocus
            />
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium">Parent branch{forStack ? "" : " (optional)"}</label>
            <select
              value={parent}
              onChange={(e) => setParent(e.target.value)}
              className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            >
              {forStack ? (
                <option value="">Select parent...</option>
              ) : (
                <option value="">Default (current branch)</option>
              )}
              {uniqueBranches.map((b) => (
                <option key={b} value={b}>
                  {b}
                </option>
              ))}
            </select>
          </div>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!name.trim() || isLoading || (!!forStack && !parent)}>
              Create
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
