import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import type { StatusStack } from "../../types/ezstack";
import { buildParentPickChoices, ParentPickChoice } from "../../lib/parent-picker";

interface NewBranchDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  stacks: StatusStack[];
  forStack?: StatusStack;
  onSubmit: (name: string, parent?: string) => void;
  isLoading: boolean;
}

interface ScopedBranch {
  branchName: string;
  description: string;
}

export function NewBranchDialog({ open, onOpenChange, stacks, forStack, onSubmit, isLoading }: NewBranchDialogProps) {
  const [name, setName] = useState("");
  const [parent, setParent] = useState("");

  useEffect(() => {
    if (!open) { setName(""); setParent(""); }
  }, [open]);

  // When scoped to a single stack, only that stack's branches are valid parents
  // and the user already knows which stack they're in — flat list is fine.
  // When unscoped, we MUST label each entry to distinguish "stack root → new
  // stack" vs "stack tip → child of existing stack" so users don't silently
  // add a branch to the wrong stack (see v4.5.1 fix).
  const scopedBranches: ScopedBranch[] = forStack
    ? [
        { branchName: forStack.root, description: "stack root" },
        ...forStack.branches.map((b) => ({ branchName: b.name, description: "" })),
      ]
    : [];
  const choices: ParentPickChoice[] = forStack ? [] : buildParentPickChoices(stacks);
  const renderedOptions = forStack ? scopedBranches : choices;

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
              {renderedOptions.map((opt) => (
                <option key={opt.branchName} value={opt.branchName}>
                  {opt.description ? `${opt.branchName}  —  ${opt.description}` : opt.branchName}
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
