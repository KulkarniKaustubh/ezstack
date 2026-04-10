import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";

interface PRMergeDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  branchName: string;
  prNumber: number;
  onSubmit: (method: "squash" | "merge" | "rebase") => void;
  isLoading: boolean;
}

export function PRMergeDialog({ open, onOpenChange, branchName, prNumber, onSubmit, isLoading }: PRMergeDialogProps) {
  const [method, setMethod] = useState<"squash" | "merge" | "rebase">("squash");

  useEffect(() => {
    if (!open) setMethod("squash");
  }, [open]);

  const methods = [
    { value: "squash" as const, label: "Squash and merge", desc: "Combine all commits into one" },
    { value: "merge" as const, label: "Create a merge commit", desc: "Preserve all commits with a merge commit" },
    { value: "rebase" as const, label: "Rebase and merge", desc: "Rebase commits onto base branch" },
  ];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>Merge Pull Request</DialogTitle>
          <DialogDescription>
            Merge <strong className="font-mono">{branchName}</strong> (#{prNumber})
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          {methods.map((m) => (
            <label
              key={m.value}
              className={`flex items-center gap-3 p-3 rounded-lg border cursor-pointer transition-colors ${
                method === m.value ? "border-primary bg-accent" : "border-border hover:bg-accent/50"
              }`}
            >
              <input
                type="radio"
                name="method"
                value={m.value}
                checked={method === m.value}
                onChange={() => setMethod(m.value)}
                className="accent-primary"
              />
              <div>
                <div className="text-sm font-medium">{m.label}</div>
                <div className="text-xs text-muted-foreground">{m.desc}</div>
              </div>
            </label>
          ))}
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={() => onSubmit(method)} disabled={isLoading} className="bg-success text-white hover:bg-success/90">
            Merge
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
