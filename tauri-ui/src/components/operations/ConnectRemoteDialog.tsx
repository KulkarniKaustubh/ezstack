import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

type Step = "credentials" | "select-repo";

interface ConnectRemoteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConnect: (host: string, user: string, port: number, keyPath: string) => Promise<string[]>;
  onSelectRepo: (host: string, user: string, port: number, keyPath: string, repoPath: string) => Promise<void>;
  isLoading: boolean;
  error: string | null;
}

export function ConnectRemoteDialog({
  open,
  onOpenChange,
  onConnect,
  onSelectRepo,
  isLoading,
  error,
}: ConnectRemoteDialogProps) {
  const [step, setStep] = useState<Step>("credentials");
  const [host, setHost] = useState("");
  const [user, setUser] = useState("");
  const [port, setPort] = useState("22");
  const [keyPath, setKeyPath] = useState("");
  const [repos, setRepos] = useState<string[]>([]);
  const [connecting, setConnecting] = useState(false);
  const [selecting, setSelecting] = useState(false);
  const [connectError, setConnectError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      setStep("credentials");
      setHost("");
      setUser("");
      setPort("22");
      setKeyPath("");
      setRepos([]);
      setConnecting(false);
      setSelecting(false);
      setConnectError(null);
    }
  }, [open]);

  const parsedPort = () => Math.min(Math.max(parseInt(port) || 22, 1), 65535);

  const doSelectRepo = async (repoPath: string) => {
    setSelecting(true);
    setConnectError(null);
    try {
      await onSelectRepo(host.trim(), user.trim(), parsedPort(), keyPath.trim(), repoPath);
    } catch (e) {
      setConnectError(String(e));
    } finally {
      setSelecting(false);
    }
  };

  const handleConnect = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!host.trim() || !user.trim()) return;
    setConnecting(true);
    setConnectError(null);
    try {
      const discoveredRepos = await onConnect(
        host.trim(),
        user.trim(),
        parsedPort(),
        keyPath.trim(),
      );
      setRepos(discoveredRepos);
      if (discoveredRepos.length === 1) {
        // Auto-select if only one repo
        await doSelectRepo(discoveredRepos[0]);
      } else {
        setStep("select-repo");
      }
    } catch (e) {
      setConnectError(String(e));
    } finally {
      setConnecting(false);
    }
  };

  const handleSelectRepo = (repoPath: string) => {
    doSelectRepo(repoPath);
  };

  const displayError = connectError || error;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>
            {step === "credentials" ? "Connect to Remote" : "Select Repository"}
          </DialogTitle>
          <DialogDescription>
            {step === "credentials"
              ? "Connect to a remote machine via SSH to manage stacks on a server or dev box."
              : `Found ${repos.length} repositories on ${user}@${host}. Select one to manage.`}
          </DialogDescription>
        </DialogHeader>

        {step === "credentials" ? (
          <form onSubmit={handleConnect} className="space-y-4">
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
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
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
            {displayError && (
              <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {displayError}
              </div>
            )}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={!host.trim() || !user.trim() || connecting}
              >
                {connecting ? "Connecting..." : "Connect"}
              </Button>
            </div>
          </form>
        ) : (
          <div className="space-y-4">
            <div className="space-y-2 max-h-64 overflow-y-auto">
              {repos.map((repo) => (
                <button
                  key={repo}
                  onClick={() => handleSelectRepo(repo)}
                  disabled={isLoading || selecting}
                  className="w-full text-left px-3 py-2.5 rounded-md border border-border hover:bg-accent hover:text-accent-foreground transition-colors text-sm font-mono disabled:opacity-50"
                >
                  {repo}
                </button>
              ))}
            </div>
            {displayError && (
              <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {displayError}
              </div>
            )}
            <div className="flex justify-between">
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setStep("credentials");
                  setConnectError(null);
                }}
                disabled={isLoading || selecting}
              >
                Back
              </Button>
              <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={selecting}>
                Cancel
              </Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
