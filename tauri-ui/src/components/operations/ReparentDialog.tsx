import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";
import type { StatusStack } from "../../types/ezstack";

interface ReparentDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  branchName: string;
  stacks: StatusStack[];
  onSubmit: (newParent: string) => void;
  isLoading: boolean;
}

export function ReparentDialog({ open, onOpenChange, branchName, stacks, onSubmit, isLoading }: ReparentDialogProps) {
  const [newParent, setNewParent] = useState("");

  useEffect(() => {
    if (!open) setNewParent("");
  }, [open]);

  const allBranches = stacks.flatMap((s) => [s.root, ...s.branches.map((b) => b.name)]);
  const candidates = [...new Set(allBranches)].filter((b) => b !== branchName);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>Reparent Branch</DialogTitle>
          <DialogDescription>
            Change the parent of <strong className="font-mono">{branchName}</strong>
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <label className="text-sm font-medium">New parent</label>
          <select
            value={newParent}
            onChange={(e) => setNewParent(e.target.value)}
            className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            <option value="">Select a branch...</option>
            {candidates.map((b) => (
              <option key={b} value={b}>
                {b}
              </option>
            ))}
          </select>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={() => onSubmit(newParent)} disabled={!newParent || isLoading}>
            Reparent
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
