import { useState, useEffect, useCallback, useRef } from "react";
import { open as openDialog } from "@tauri-apps/plugin-dialog";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "../ui/dialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import {
  Folder,
  FolderOpen,
  Save,
  Trash2,
  Stethoscope,
  CheckCircle2,
  AlertTriangle,
  XCircle,
  Loader2,
  RefreshCw,
  Plug,
  PlugZap,
  Fingerprint,
  Copy,
  Check,
} from "lucide-react";
import * as ezs from "../../commands/ezs";
import type {
  SshConnection,
  ConnectionProfile,
  RemoteRepoSummary,
  DiagnosticStep,
  HostFingerprint,
} from "../../types/ezstack";

type Step = "credentials" | "select-repo" | "connected" | "diagnostics";

interface ConnectRemoteDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Caller-provided connect handler. Returns the discovered repo summaries. */
  onConnect: (
    host: string,
    user: string,
    port: number,
    keyPath: string,
    jumpHost: string,
  ) => Promise<RemoteRepoSummary[]>;
  /** Caller-provided repo selection handler. Returns the resolved absolute path. */
  onSelectRepo: (
    host: string,
    user: string,
    port: number,
    keyPath: string,
    jumpHost: string,
    repoPath: string,
    label: string,
  ) => Promise<void>;
  onDisconnect: () => Promise<void>;
  isLoading: boolean;
  error: string | null;
  currentConnection: SshConnection | null;
}

interface FormState {
  host: string;
  user: string;
  port: string;
  keyPath: string;
  jumpHost: string;
  label: string;
}

const EMPTY_FORM: FormState = {
  host: "",
  user: "",
  port: "22",
  keyPath: "",
  jumpHost: "",
  label: "",
};

function parsePort(port: string): number {
  const n = parseInt(port, 10);
  if (Number.isNaN(n)) return 22;
  return Math.min(Math.max(n, 1), 65535);
}

function profileToForm(p: ConnectionProfile): FormState {
  return {
    host: p.host,
    user: p.user,
    port: String(p.port || 22),
    keyPath: p.key_path || "",
    jumpHost: p.jump_host || "",
    label: p.name || "",
  };
}

function formToProfile(f: FormState, id: string = ""): ConnectionProfile {
  return {
    id,
    name: f.label.trim() || `${f.user.trim()}@${f.host.trim()}`,
    host: f.host.trim(),
    user: f.user.trim(),
    port: parsePort(f.port),
    key_path: f.keyPath.trim(),
    jump_host: f.jumpHost.trim(),
    last_repo_path: "",
  };
}

export function ConnectRemoteDialog({
  open,
  onOpenChange,
  onConnect,
  onSelectRepo,
  onDisconnect,
  isLoading: _isLoading,
  error,
  currentConnection,
}: ConnectRemoteDialogProps) {
  const [step, setStep] = useState<Step>("credentials");
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [repos, setRepos] = useState<RemoteRepoSummary[]>([]);
  const [connecting, setConnecting] = useState(false);
  const [selecting, setSelecting] = useState(false);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [profiles, setProfiles] = useState<ConnectionProfile[]>([]);
  const [activeProfileId, setActiveProfileId] = useState<string>("");
  const [savedFlash, setSavedFlash] = useState(false);
  const [diagnostics, setDiagnostics] = useState<DiagnosticStep[]>([]);
  const [diagnosing, setDiagnosing] = useState(false);
  const [fingerprints, setFingerprints] = useState<HostFingerprint[] | null>(null);
  const [fingerprintLoading, setFingerprintLoading] = useState(false);
  const [diagCopied, setDiagCopied] = useState(false);
  // Track whether we've reset on this open cycle so subsequent prop changes
  // (e.g. parent re-resolving currentConnection) don't blow away the user's
  // half-typed form.
  const openResetRef = useRef(false);
  const isBusy = connecting || selecting || diagnosing;

  // Load profiles whenever the dialog is opened.
  useEffect(() => {
    if (!open) return;
    ezs.listConnectionProfiles().then(setProfiles).catch(() => setProfiles([]));
  }, [open]);

  // Reset state ONCE per open cycle. Pre-fill from active connection if any.
  // We deliberately do NOT depend on `currentConnection` here — it can change
  // mid-edit (parent re-fetches it after a connect succeeds) and would
  // otherwise overwrite the user's typed credentials.
  useEffect(() => {
    if (!open) {
      openResetRef.current = false;
      return;
    }
    if (openResetRef.current) return;
    openResetRef.current = true;
    setConnecting(false);
    setSelecting(false);
    setConnectError(null);
    setDiagnostics([]);
    setDiagnosing(false);
    setFingerprints(null);
    setFingerprintLoading(false);
    if (currentConnection) {
      setForm({
        host: currentConnection.host,
        user: currentConnection.user,
        port: String(currentConnection.port),
        keyPath: currentConnection.key_path,
        jumpHost: currentConnection.jump_host || "",
        label: currentConnection.label || "",
      });
      setStep("connected");
    } else {
      setForm(EMPTY_FORM);
      setRepos([]);
      setActiveProfileId("");
      setStep("credentials");
    }
  }, [open, currentConnection]);

  const updateField = useCallback(<K extends keyof FormState>(key: K, value: FormState[K]) => {
    setForm((f) => ({ ...f, [key]: value }));
  }, []);

  const applyProfile = (profile: ConnectionProfile) => {
    setForm(profileToForm(profile));
    setActiveProfileId(profile.id);
    setConnectError(null);
    setDiagnostics([]);
  };

  const pickKeyFile = async () => {
    try {
      const selected = await openDialog({
        multiple: false,
        directory: false,
        title: "Select SSH private key",
      });
      if (selected && typeof selected === "string") {
        updateField("keyPath", selected);
      }
    } catch (e) {
      setConnectError(e instanceof Error ? e.message : String(e));
    }
  };

  const validate = (): string | null => {
    if (!form.host.trim()) return "Host is required";
    if (!form.user.trim()) return "Username is required";
    const port = parsePort(form.port);
    if (port < 1 || port > 65535) return "Port must be between 1 and 65535";
    return null;
  };

  const doConnect = async () => {
    const v = validate();
    if (v) {
      setConnectError(v);
      return;
    }
    setConnecting(true);
    setConnectError(null);
    try {
      const discovered = await onConnect(
        form.host.trim(),
        form.user.trim(),
        parsePort(form.port),
        form.keyPath.trim(),
        form.jumpHost.trim(),
      );
      setRepos(discovered);
      if (discovered.length === 1 && discovered[0].exists) {
        await doSelectRepo(discovered[0].repo_path);
      } else {
        setStep("select-repo");
      }
    } catch (e) {
      setConnectError(e instanceof Error ? e.message : String(e));
    } finally {
      setConnecting(false);
    }
  };

  const doSelectRepo = async (repoPath: string) => {
    setSelecting(true);
    setConnectError(null);
    try {
      const labelToUse = form.label.trim() || `${form.user.trim()}@${form.host.trim()}`;
      await onSelectRepo(
        form.host.trim(),
        form.user.trim(),
        parsePort(form.port),
        form.keyPath.trim(),
        form.jumpHost.trim(),
        repoPath,
        labelToUse,
      );
      // Persist last-used repo on the active profile if one is selected.
      if (activeProfileId) {
        ezs.updateProfileLastRepo(activeProfileId, repoPath).catch(() => {});
      }
    } catch (e) {
      setConnectError(e instanceof Error ? e.message : String(e));
    } finally {
      setSelecting(false);
    }
  };

  const handleSaveProfile = async () => {
    const v = validate();
    if (v) {
      setConnectError(v);
      return;
    }
    setConnectError(null);
    try {
      const profile = formToProfile(form, activeProfileId);
      const saved = await ezs.saveConnectionProfile(profile);
      const next = await ezs.listConnectionProfiles();
      setProfiles(next);
      setActiveProfileId(saved.id);
      setSavedFlash(true);
      setTimeout(() => setSavedFlash(false), 1500);
    } catch (e) {
      setConnectError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleDeleteProfile = async (id: string) => {
    setConnectError(null);
    try {
      await ezs.deleteConnectionProfile(id);
      const next = await ezs.listConnectionProfiles();
      setProfiles(next);
      if (activeProfileId === id) {
        setActiveProfileId("");
      }
    } catch (e) {
      setConnectError(e instanceof Error ? e.message : String(e));
    }
  };

  const handleRunDiagnostics = async () => {
    const v = validate();
    if (v) {
      setConnectError(v);
      return;
    }
    setDiagnosing(true);
    setConnectError(null);
    setDiagnostics([]);
    setStep("diagnostics");
    try {
      const result = await ezs.diagnoseConnection(
        form.host.trim(),
        form.user.trim(),
        parsePort(form.port),
        form.keyPath.trim(),
        form.jumpHost.trim(),
      );
      setDiagnostics(result);
    } catch (e) {
      setConnectError(e instanceof Error ? e.message : String(e));
    } finally {
      setDiagnosing(false);
    }
  };

  const handleShowFingerprint = async () => {
    if (!form.host.trim()) {
      setConnectError("Host is required to fetch fingerprint");
      return;
    }
    setFingerprintLoading(true);
    setConnectError(null);
    try {
      const fp = await ezs.getHostFingerprint(form.host.trim(), parsePort(form.port));
      setFingerprints(fp);
    } catch (e) {
      setConnectError(e instanceof Error ? e.message : String(e));
    } finally {
      setFingerprintLoading(false);
    }
  };

  const handleCopyDiagnostics = async () => {
    if (diagnostics.length === 0) return;
    const text = diagnostics
      .map((d) => {
        const dur = d.duration_ms != null ? ` (${d.duration_ms}ms)` : "";
        return `[${d.status.toUpperCase()}] ${d.name}${dur}: ${d.message}`;
      })
      .join("\n");
    try {
      await navigator.clipboard.writeText(text);
      setDiagCopied(true);
      setTimeout(() => setDiagCopied(false), 1200);
    } catch (e) {
      setConnectError(`Failed to copy: ${e instanceof Error ? e.message : String(e)}`);
    }
  };

  const handleReconnect = async () => {
    setConnecting(true);
    setConnectError(null);
    try {
      await onDisconnect();
      await doConnect();
    } catch (e) {
      setConnectError(e instanceof Error ? e.message : String(e));
    } finally {
      setConnecting(false);
    }
  };

  const handleDisconnect = async () => {
    await onDisconnect();
    onOpenChange(false);
  };

  const displayError = connectError || error;

  const profilesList = profiles.length > 0 && (
    <div>
      <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground mb-1.5">
        Saved Connections
      </div>
      <div className="space-y-1 max-h-32 overflow-y-auto pr-1">
        {profiles.map((p) => {
          const isActive = p.id === activeProfileId;
          return (
            <div
              key={p.id}
              className={`group flex items-center gap-2 px-2 py-1.5 rounded-md text-xs border ${
                isActive
                  ? "border-primary bg-primary/5"
                  : "border-transparent hover:border-border hover:bg-accent/50"
              }`}
            >
              <button
                type="button"
                onClick={() => applyProfile(p)}
                className="flex-1 text-left truncate"
                disabled={connecting || selecting}
              >
                <span className="font-medium">{p.name}</span>
                <span className="text-muted-foreground ml-1.5">
                  {p.user}@{p.host}
                  {p.port !== 22 ? `:${p.port}` : ""}
                  {p.jump_host ? ` ⇢ ${p.jump_host}` : ""}
                </span>
              </button>
              <button
                type="button"
                onClick={() => handleDeleteProfile(p.id)}
                title="Delete profile"
                className="opacity-0 group-hover:opacity-100 p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-opacity"
                disabled={connecting || selecting}
              >
                <Trash2 className="h-3 w-3" />
              </button>
            </div>
          );
        })}
      </div>
    </div>
  );

  const credentialsForm = (
    <div className="space-y-3">
      <div>
        <label className="text-xs font-medium">Profile name (optional)</label>
        <Input
          value={form.label}
          onChange={(e) => updateField("label", e.target.value)}
          placeholder="e.g. dev-box-eu"
          disabled={connecting}
        />
      </div>
      <div className="grid grid-cols-[1fr_90px] gap-2">
        <div>
          <label className="text-xs font-medium">Host</label>
          <Input
            value={form.host}
            onChange={(e) => updateField("host", e.target.value)}
            placeholder="dev.example.com"
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
            disabled={connecting}
          />
        </div>
        <div>
          <label className="text-xs font-medium">Port</label>
          <Input
            value={form.port}
            onChange={(e) => updateField("port", e.target.value)}
            type="number"
            min={1}
            max={65535}
            disabled={connecting}
          />
        </div>
      </div>
      <div>
        <label className="text-xs font-medium">Username</label>
        <Input
          value={form.user}
          onChange={(e) => updateField("user", e.target.value)}
          placeholder="ubuntu"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          disabled={connecting}
        />
      </div>
      <div>
        <label className="text-xs font-medium">
          SSH Key Path <span className="text-muted-foreground font-normal">(optional)</span>
        </label>
        <div className="flex gap-1.5">
          <Input
            value={form.keyPath}
            onChange={(e) => updateField("keyPath", e.target.value)}
            placeholder="~/.ssh/id_ed25519"
            disabled={connecting}
            className="flex-1 font-mono text-xs"
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={pickKeyFile}
            disabled={connecting}
            title="Browse for key file"
          >
            <FolderOpen className="h-3.5 w-3.5" />
          </Button>
        </div>
        <p className="text-[10px] text-muted-foreground mt-1">
          Leave empty to use ssh-agent / default keys.
        </p>
      </div>
      <div>
        <label className="text-xs font-medium">
          Jump Host (ProxyJump) <span className="text-muted-foreground font-normal">(optional)</span>
        </label>
        <Input
          value={form.jumpHost}
          onChange={(e) => updateField("jumpHost", e.target.value)}
          placeholder="user@bastion.example.com"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
          disabled={connecting}
          className="font-mono text-xs"
        />
        <p className="text-[10px] text-muted-foreground mt-1">
          Equivalent to <code className="font-mono">ssh -J</code>. Use to tunnel through a bastion.
        </p>
      </div>
    </div>
  );

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        onClose={() => onOpenChange(false)}
        className="max-w-xl max-h-[90vh] overflow-y-auto"
      >
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {step === "connected" ? (
              <>
                <PlugZap className="h-4 w-4 text-green-500" /> Remote Connection
              </>
            ) : step === "diagnostics" ? (
              <>
                <Stethoscope className="h-4 w-4" /> Connection Diagnostics
              </>
            ) : step === "credentials" ? (
              <>
                <Plug className="h-4 w-4" /> Connect to Remote
              </>
            ) : (
              <>
                <Folder className="h-4 w-4" /> Select Repository
              </>
            )}
          </DialogTitle>
          <DialogDescription>
            {step === "connected"
              ? `Connected to ${currentConnection?.user}@${currentConnection?.host}${
                  currentConnection?.jump_host ? ` via ${currentConnection.jump_host}` : ""
                }`
              : step === "diagnostics"
                ? "Detailed health check against the configured remote."
                : step === "credentials"
                  ? "Connect to a remote machine via SSH to manage stacks on a server, dev box, or VM."
                  : `Found ${repos.length} repositor${repos.length === 1 ? "y" : "ies"} on ${form.user}@${form.host}.`}
          </DialogDescription>
        </DialogHeader>

        {/* CREDENTIALS STEP */}
        {step === "credentials" && (
          <div className="relative space-y-4">
            {connecting && (
              <div className="absolute inset-0 bg-background/80 flex items-center justify-center rounded-lg z-10">
                <div className="flex flex-col items-center gap-3">
                  <Loader2 className="h-6 w-6 animate-spin text-primary" />
                  <span className="text-sm text-muted-foreground">
                    Connecting to {form.user}@{form.host}
                    {form.jumpHost ? ` via ${form.jumpHost}` : ""}…
                  </span>
                </div>
              </div>
            )}

            {profilesList}

            <form
              onSubmit={(e) => {
                e.preventDefault();
                doConnect();
              }}
              className="space-y-4"
            >
              {credentialsForm}

              {fingerprints && fingerprints.length > 0 && (
                <div className="rounded-md border border-yellow-500/40 bg-yellow-500/5 p-2.5">
                  <div className="flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wider text-yellow-700 dark:text-yellow-400 mb-1.5">
                    <Fingerprint className="h-3 w-3" />
                    Host key fingerprints — verify before connecting
                  </div>
                  <div className="space-y-0.5">
                    {fingerprints.map((fp) => (
                      <div key={fp.fingerprint} className="flex items-center justify-between gap-2 font-mono text-[11px]">
                        <span className="text-muted-foreground shrink-0">
                          {fp.key_type} ({fp.bits})
                        </span>
                        <span className="truncate" title={fp.fingerprint}>
                          {fp.fingerprint}
                        </span>
                      </div>
                    ))}
                  </div>
                  <p className="text-[10px] text-muted-foreground mt-1.5">
                    Compare against the value the host operator published. If they don't match, do not connect.
                  </p>
                </div>
              )}

              {displayError && (
                <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive whitespace-pre-wrap">
                  {displayError}
                </div>
              )}

              <div className="flex items-center justify-between gap-2 pt-1">
                <div className="flex gap-1.5">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={handleSaveProfile}
                    disabled={isBusy}
                    title="Save these credentials as a profile"
                  >
                    <Save className="h-3.5 w-3.5 mr-1" />
                    {savedFlash ? "Saved!" : "Save"}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={handleRunDiagnostics}
                    disabled={isBusy}
                    title="Run multi-step health check"
                  >
                    <Stethoscope className="h-3.5 w-3.5 mr-1" />
                    Diagnose
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={handleShowFingerprint}
                    disabled={isBusy || fingerprintLoading}
                    title="Show this host's SSH public-key fingerprints (verify before trusting)"
                  >
                    <Fingerprint className="h-3.5 w-3.5 mr-1" />
                    {fingerprintLoading ? "…" : "Verify host"}
                  </Button>
                </div>
                <div className="flex gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => onOpenChange(false)}
                    disabled={connecting}
                  >
                    Cancel
                  </Button>
                  <Button type="submit" disabled={connecting}>
                    {connecting ? "Connecting…" : "Connect"}
                  </Button>
                </div>
              </div>
            </form>
          </div>
        )}

        {/* DIAGNOSTICS STEP */}
        {step === "diagnostics" && (
          <div className="space-y-4">
            <div className="space-y-2">
              {diagnosing && (
                <div className="flex items-center gap-2 text-sm text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" /> Running checks…
                </div>
              )}
              {diagnostics.map((d) => (
                <div
                  key={d.name}
                  className="flex items-start gap-2 px-3 py-2 rounded-md border text-sm"
                >
                  <div className="mt-0.5">
                    {d.status === "ok" && (
                      <CheckCircle2 className="h-4 w-4 text-green-500" />
                    )}
                    {d.status === "warn" && (
                      <AlertTriangle className="h-4 w-4 text-yellow-500" />
                    )}
                    {d.status === "fail" && <XCircle className="h-4 w-4 text-destructive" />}
                    {d.status === "skip" && (
                      <div className="h-4 w-4 rounded-full bg-muted" />
                    )}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center justify-between">
                      <span className="font-medium">{d.name}</span>
                      {d.duration_ms != null && (
                        <span className="text-[10px] text-muted-foreground font-mono">
                          {d.duration_ms} ms
                        </span>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground font-mono break-all">
                      {d.message}
                    </div>
                  </div>
                </div>
              ))}
              {!diagnosing && diagnostics.length === 0 && (
                <div className="text-sm text-muted-foreground">No diagnostic results.</div>
              )}
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
                onClick={() => setStep("credentials")}
                disabled={diagnosing}
              >
                Back
              </Button>
              <div className="flex gap-2">
                <Button
                  type="button"
                  variant="outline"
                  onClick={handleCopyDiagnostics}
                  disabled={diagnosing || diagnostics.length === 0}
                  title="Copy diagnostic output to clipboard"
                >
                  {diagCopied ? (
                    <>
                      <Check className="h-3.5 w-3.5 mr-1" /> Copied
                    </>
                  ) : (
                    <>
                      <Copy className="h-3.5 w-3.5 mr-1" /> Copy
                    </>
                  )}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={handleRunDiagnostics}
                  disabled={diagnosing}
                >
                  <RefreshCw className="h-3.5 w-3.5 mr-1" /> Re-run
                </Button>
                <Button type="button" onClick={doConnect} disabled={diagnosing}>
                  Connect
                </Button>
              </div>
            </div>
          </div>
        )}

        {/* SELECT REPO STEP */}
        {step === "select-repo" && (
          <div className="space-y-4">
            <div className="space-y-1.5 max-h-72 overflow-y-auto pr-1">
              {repos.map((repo) => (
                <button
                  key={repo.repo_path}
                  onClick={() => doSelectRepo(repo.repo_path)}
                  disabled={selecting || !repo.exists}
                  className="w-full text-left px-3 py-2.5 rounded-md border border-border hover:bg-accent hover:text-accent-foreground transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                  title={repo.exists ? "Open this repo" : "Path missing on remote"}
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="font-mono text-xs truncate">{repo.repo_path}</div>
                    {repo.exists ? (
                      <span className="text-[10px] text-green-600 dark:text-green-400 shrink-0">
                        present
                      </span>
                    ) : (
                      <span className="text-[10px] text-destructive shrink-0">missing</span>
                    )}
                  </div>
                  {(repo.worktree_base_dir || repo.default_base_branch) && (
                    <div className="mt-1 flex gap-3 text-[10px] text-muted-foreground font-mono">
                      {repo.worktree_base_dir && <span>wt: {repo.worktree_base_dir}</span>}
                      {repo.default_base_branch && <span>base: {repo.default_base_branch}</span>}
                    </div>
                  )}
                </button>
              ))}
            </div>
            {displayError && (
              <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive whitespace-pre-wrap">
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
                disabled={selecting}
              >
                Back
              </Button>
              <div className="flex gap-2">
                <Button
                  type="button"
                  variant="outline"
                  onClick={async () => {
                    setSelecting(true);
                    try {
                      const fresh = await onConnect(
                        form.host.trim(),
                        form.user.trim(),
                        parsePort(form.port),
                        form.keyPath.trim(),
                        form.jumpHost.trim(),
                      );
                      setRepos(fresh);
                    } catch (e) {
                      setConnectError(e instanceof Error ? e.message : String(e));
                    } finally {
                      setSelecting(false);
                    }
                  }}
                  disabled={selecting}
                >
                  <RefreshCw className="h-3.5 w-3.5 mr-1" /> Refresh
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => onOpenChange(false)}
                  disabled={selecting}
                >
                  Cancel
                </Button>
              </div>
            </div>
          </div>
        )}

        {/* CONNECTED STEP */}
        {step === "connected" && (
          <div className="space-y-4 relative">
            {connecting && (
              <div className="absolute inset-0 bg-background/80 flex items-center justify-center rounded-lg z-10">
                <div className="flex flex-col items-center gap-3">
                  <Loader2 className="h-6 w-6 animate-spin text-primary" />
                  <span className="text-sm text-muted-foreground">Reconnecting…</span>
                </div>
              </div>
            )}
            <div className="rounded-md bg-green-500/10 px-3 py-2 text-sm text-green-600 dark:text-green-400 flex items-center gap-2">
              <div className="h-2 w-2 rounded-full bg-green-500 animate-pulse" />
              Connected
            </div>
            {currentConnection?.remote_repo_path && (
              <div className="rounded-md bg-muted px-3 py-2">
                <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
                  Repository
                </div>
                <p className="text-sm font-mono">{currentConnection.remote_repo_path}</p>
              </div>
            )}
            {currentConnection?.jump_host && (
              <div className="rounded-md bg-muted px-3 py-2">
                <div className="text-[10px] uppercase tracking-wider text-muted-foreground">
                  Jump host
                </div>
                <p className="text-sm font-mono">{currentConnection.jump_host}</p>
              </div>
            )}
            {credentialsForm}
            {displayError && (
              <div className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive whitespace-pre-wrap">
                {displayError}
              </div>
            )}
            <div className="flex items-center justify-between gap-2">
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
                  size="sm"
                  onClick={handleRunDiagnostics}
                  disabled={connecting}
                >
                  <Stethoscope className="h-3.5 w-3.5 mr-1" /> Diagnose
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => onOpenChange(false)}
                  disabled={connecting}
                >
                  Close
                </Button>
                <Button type="button" onClick={handleReconnect} disabled={connecting}>
                  {connecting ? "Connecting…" : "Reconnect"}
                </Button>
              </div>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
