import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";

interface DeleteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  branchName: string;
  onSubmit: (force: boolean) => void;
  isLoading: boolean;
}

export function DeleteDialog({ open, onOpenChange, branchName, onSubmit, isLoading }: DeleteDialogProps) {
  const [force, setForce] = useState(false);

  useEffect(() => {
    if (!open) setForce(false);
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>Delete Branch</DialogTitle>
          <DialogDescription>
            Are you sure you want to delete <strong className="font-mono">{branchName}</strong>?
            This will remove the branch, its worktree, and tracking data.
          </DialogDescription>
        </DialogHeader>
        <label className="flex items-center gap-2 mt-2">
          <input
            type="checkbox"
            checked={force}
            onChange={(e) => setForce(e.target.checked)}
            className="accent-primary"
          />
          <span className="text-sm">Force delete (even if unmerged)</span>
        </label>
        <div className="flex justify-end gap-2 mt-4">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={() => onSubmit(force)} disabled={isLoading}>
            Delete
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
