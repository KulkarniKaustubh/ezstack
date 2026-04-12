import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";
import { Textarea } from "../ui/textarea";
import type { StatusStack } from "../../types/ezstack";

interface AgentFeatureDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  stacks: StatusStack[];
  selectedStackHash: string | null;
  onSubmit: (stackHash: string, description: string) => void;
}

export function AgentFeatureDialog({ open, onOpenChange, stacks, selectedStackHash, onSubmit }: AgentFeatureDialogProps) {
  const [description, setDescription] = useState("");
  const [stackHash, setStackHash] = useState(selectedStackHash || "");

  useEffect(() => {
    if (open) {
      setDescription("");
      setStackHash(selectedStackHash || (stacks.length > 0 ? stacks[0].hash : ""));
    }
  }, [open, selectedStackHash, stacks]);

  const canSubmit = description.trim() !== "" && stackHash !== "";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>Build Feature with Agent</DialogTitle>
          <DialogDescription>
            The agent will explore the codebase, plan a series of stacked branches, and implement them after your approval.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {stacks.length > 1 && (
            <div>
              <label className="text-sm font-medium mb-1.5 block">Stack</label>
              <select
                className="w-full rounded-md border bg-background px-3 py-2 text-sm"
                value={stackHash}
                onChange={(e) => setStackHash(e.target.value)}
              >
                {stacks.map((s) => (
                  <option key={s.hash} value={s.hash}>
                    {s.name || s.hash.slice(0, 7)} ({s.branches.length} branch{s.branches.length !== 1 ? "es" : ""})
                  </option>
                ))}
              </select>
            </div>
          )}
          <div>
            <label className="text-sm font-medium mb-1.5 block">Feature Description</label>
            <Textarea
              placeholder="Describe the feature you want to build..."
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={4}
              autoFocus
              onKeyDown={(e) => {
                if (e.key === "Enter" && e.metaKey && canSubmit) {
                  e.preventDefault();
                  onSubmit(stackHash, description.trim());
                }
              }}
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button onClick={() => onSubmit(stackHash, description.trim())} disabled={!canSubmit}>
              Launch Agent
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
