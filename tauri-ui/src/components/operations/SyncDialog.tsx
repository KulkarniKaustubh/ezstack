import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";

interface SyncDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (scope: "current" | "stack" | "all") => void;
  branchName?: string;
  isLoading: boolean;
}

export function SyncDialog({ open, onOpenChange, onSubmit, branchName, isLoading }: SyncDialogProps) {
  const [scope, setScope] = useState<"current" | "stack" | "all">("current");

  useEffect(() => {
    if (!open) setScope("current");
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>Sync Branches</DialogTitle>
          <DialogDescription>
            Choose the scope of the sync operation.
            {branchName && <> Current branch: <strong>{branchName}</strong></>}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          {(["current", "stack", "all"] as const).map((s) => (
            <label
              key={s}
              className={`flex items-center gap-3 p-3 rounded-lg border cursor-pointer transition-colors ${
                scope === s ? "border-primary bg-accent" : "border-border hover:bg-accent/50"
              }`}
            >
              <input
                type="radio"
                name="scope"
                value={s}
                checked={scope === s}
                onChange={() => setScope(s)}
                className="accent-primary"
              />
              <div>
                <div className="text-sm font-medium capitalize">{s === "current" ? "Current branch" : s === "stack" ? "Current stack" : "All stacks"}</div>
                <div className="text-xs text-muted-foreground">
                  {s === "current" && "Sync only the selected branch"}
                  {s === "stack" && "Sync all branches in the current stack"}
                  {s === "all" && "Sync all branches across all stacks"}
                </div>
              </div>
            </label>
          ))}
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={() => onSubmit(scope)} disabled={isLoading}>
            Sync
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
