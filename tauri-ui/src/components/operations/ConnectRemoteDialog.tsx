import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

interface ConnectRemoteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (host: string, user: string, port: number, keyPath: string, repoPath: string) => void;
  isLoading: boolean;
  error: string | null;
}

export function ConnectRemoteDialog({
  open,
  onOpenChange,
  onSubmit,
  isLoading,
  error,
}: ConnectRemoteDialogProps) {
  const [host, setHost] = useState("");
  const [user, setUser] = useState("");
  const [port, setPort] = useState("22");
  const [keyPath, setKeyPath] = useState("");
  const [repoPath, setRepoPath] = useState("");

  useEffect(() => {
    if (!open) {
      setHost("");
      setUser("");
      setPort("22");
      setKeyPath("");
      setRepoPath("");
    }
  }, [open]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!host.trim() || !user.trim() || !repoPath.trim()) return;
    onSubmit(host.trim(), user.trim(), parseInt(port) || 22, keyPath.trim(), repoPath.trim());
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>Connect to Remote</DialogTitle>
          <DialogDescription>
            Connect to a remote machine via SSH to manage stacks on a server or dev box.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="grid grid-cols-[1fr_80px] gap-2">
            <div className="space-y-2">
              <label className="text-sm font-medium">Host</label>
              <Input
                value={host}
                onChange={(e) => setHost(e.target.value)}
                placeholder="192.168.1.100 or dev.example.com"
                autoFocus
              />
            </div>
            <div className="space-y-2">
              <label className="text-sm font-medium">Port</label>
              <Input
                value={port}
                onChange={(e) => setPort(e.target.value)}
                placeholder="22"
                type="number"
                min={1}
                max={65535}
              />
            </div>
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium">Username</label>
            <Input
              value={user}
              onChange={(e) => setUser(e.target.value)}
              placeholder="ubuntu"
            />
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium">
              SSH Key Path <span className="text-muted-foreground font-normal">(optional)</span>
            </label>
            <Input
              value={keyPath}
              onChange={(e) => setKeyPath(e.target.value)}
              placeholder="~/.ssh/id_rsa"
            />
            <p className="text-xs text-muted-foreground">
              Leave empty to use your default SSH keys.
            </p>
          </div>
          <div className="space-y-2">
            <label className="text-sm font-medium">Repository Path</label>
            <Input
              value={repoPath}
              onChange={(e) => setRepoPath(e.target.value)}
              placeholder="/home/user/projects/my-repo"
            />
          </div>
          {error && (
            <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={!host.trim() || !user.trim() || !repoPath.trim() || isLoading}
            >
              {isLoading ? "Connecting..." : "Connect"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
