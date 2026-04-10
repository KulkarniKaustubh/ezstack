import { useState, useEffect } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../ui/dialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import type { SshConnection } from "../../types/ezstack";

type Step = "credentials" | "select-repo" | "connected";

interface ConnectRemoteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onConnect: (host: string, user: string, port: number, keyPath: string) => Promise<string[]>;
  onSelectRepo: (host: string, user: string, port: number, keyPath: string, repoPath: string) => Promise<void>;
  onDisconnect: () => Promise<void>;
  isLoading: boolean;
  error: string | null;
  currentConnection: SshConnection | null;
}

export function ConnectRemoteDialog({
  open,
  onOpenChange,
  onConnect,
  onSelectRepo,
  onDisconnect,
  isLoading,
  error,
  currentConnection,
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
    if (open) {
      setConnecting(false);
      setSelecting(false);
      setConnectError(null);
      if (currentConnection) {
        setHost(currentConnection.host);
        setUser(currentConnection.user);
        setPort(String(currentConnection.port));
        setKeyPath(currentConnection.key_path);
        setStep("connected");
      } else {
        setHost("");
        setUser("");
        setPort("22");
        setKeyPath("");
        setRepos([]);
        setStep("credentials");
      }
    }
  }, [open, currentConnection]);

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

  const handleReconnect = async () => {
    if (!host.trim() || !user.trim()) return;
    setConnecting(true);
    setConnectError(null);
    try {
      await onDisconnect();
      const discoveredRepos = await onConnect(
        host.trim(),
        user.trim(),
        parsedPort(),
        keyPath.trim(),
      );
      setRepos(discoveredRepos);
      if (discoveredRepos.length === 1) {
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

  const handleDisconnect = async () => {
    await onDisconnect();
    onOpenChange(false);
  };

  const handleSelectRepo = (repoPath: string) => {
    doSelectRepo(repoPath);
  };

  const displayError = connectError || error;

  const credentialsForm = (
    <>
      <div className="grid grid-cols-[1fr_80px] gap-2">
        <div className="space-y-2">
          <label className="text-sm font-medium">Host</label>
          <Input
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="192.168.1.100 or dev.example.com"
            autoFocus={step === "credentials"}
            disabled={connecting}
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
            disabled={connecting}
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
          disabled={connecting}
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
          disabled={connecting}
        />
        <p className="text-xs text-muted-foreground">
          Leave empty to use your default SSH keys.
        </p>
      </div>
    </>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent onClose={() => onOpenChange(false)}>
        <DialogHeader>
          <DialogTitle>
            {step === "connected"
              ? "Remote Connection"
              : step === "credentials"
                ? "Connect to Remote"
                : "Select Repository"}
          </DialogTitle>
          <DialogDescription>
            {step === "connected"
              ? `Connected to ${currentConnection?.user}@${currentConnection?.host}`
              : step === "credentials"
                ? "Connect to a remote machine via SSH to manage stacks on a server or dev box."
                : `Found ${repos.length} repositories on ${user}@${host}. Select one to manage.`}
          </DialogDescription>
        </DialogHeader>

        {step === "connected" && (
          <div className="space-y-4 relative">
            {connecting && (
              <div className="absolute inset-0 bg-background/80 flex items-center justify-center rounded-lg z-10">
                <div className="flex flex-col items-center gap-3">
                  <div className="h-6 w-6 border-2 border-primary border-t-transparent rounded-full animate-spin" />
                  <span className="text-sm text-muted-foreground">
                    Reconnecting to {user.trim()}@{host.trim()}...
                  </span>
                </div>
              </div>
            )}
            <div className="rounded-md bg-green-500/10 px-3 py-2 text-sm text-green-600 dark:text-green-400 flex items-center gap-2">
              <div className="h-2 w-2 rounded-full bg-green-500" />
              Connected
            </div>
            {currentConnection?.remote_repo_path && (
              <div className="rounded-md bg-muted px-3 py-2">
                <span className="text-xs text-muted-foreground">Repository</span>
                <p className="text-sm font-mono">{currentConnection.remote_repo_path}</p>
              </div>
            )}
            {credentialsForm}
            {displayError && (
              <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {displayError}
              </div>
            )}
            <div className="flex justify-between">
              <Button
                type="button"
                variant="destructive"
                onClick={handleDisconnect}
                disabled={connecting}
              >
                Disconnect
              </Button>
              <div className="flex gap-2">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => onOpenChange(false)}
                  disabled={connecting}
                >
                  Close
                </Button>
                <Button
                  type="button"
                  onClick={handleReconnect}
                  disabled={!host.trim() || !user.trim() || connecting}
                >
                  {connecting ? "Connecting..." : "Reconnect"}
                </Button>
              </div>
            </div>
          </div>
        )}

        {step === "credentials" && (
          <div className="relative">
            {connecting && (
              <div className="absolute inset-0 bg-background/80 flex items-center justify-center rounded-lg z-10">
                <div className="flex flex-col items-center gap-3">
                  <div className="h-6 w-6 border-2 border-primary border-t-transparent rounded-full animate-spin" />
                  <span className="text-sm text-muted-foreground">
                    Connecting to {user.trim()}@{host.trim()}...
                  </span>
                </div>
              </div>
            )}
            <form onSubmit={handleConnect} className="space-y-4">
              {credentialsForm}
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
          </div>
        )}

        {step === "select-repo" && (
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
