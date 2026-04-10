import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Textarea } from "../ui/textarea";

interface PRCreateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  branchName: string;
  onSubmit: (title: string, body: string, draft: boolean) => void;
  isLoading: boolean;
}

export function PRCreateDialog({ open, onOpenChange, branchName, onSubmit, isLoading }: PRCreateDialogProps) {
  const [title, setTitle] = useState("");
  const [body, setBody] = useState("");
  const [draft, setDraft] = useState(false);

  useEffect(() => {
    if (!open) { setTitle(""); setBody(""); setDraft(false); }
  }, [open]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!title.trim()) return;
    onSubmit(title.trim(), body.trim(), draft);
    setTitle("");
    setBody("");
    setDraft(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>Create Pull Request</DialogTitle>
          <DialogDescription>
            Create a PR for <strong className="font-mono">{branchName}</strong>
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-2">
            <label className="text-sm font-medium">Title</label>
            <Input
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Add awesome feature"
              autoFocus
            />
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium">Description (optional)</label>
            <Textarea
              value={body}
              onChange={(e) => setBody(e.target.value)}
              placeholder="What does this PR do?"
              rows={4}
            />
          </div>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={draft}
              onChange={(e) => setDraft(e.target.checked)}
              className="accent-primary"
            />
            <span className="text-sm">Create as draft</span>
          </label>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!title.trim() || isLoading}>
              Create PR
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
