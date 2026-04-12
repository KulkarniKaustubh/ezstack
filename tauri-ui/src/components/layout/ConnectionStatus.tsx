import { PlugZap, Plug } from "lucide-react";
import type { SshConnection, ConnectionHealth } from "../../types/ezstack";

interface ConnectionStatusProps {
  connection: SshConnection | null;
  health: ConnectionHealth | null;
  onClick: () => void;
}

/**
 * Compact, clickable connection-state pill for the title bar.
 *
 * Disconnected → "Connect" with a plug icon.
 * Connected    → green dot, label, latency, click opens reconnect/diag dialog.
 */
export function ConnectionStatus({ connection, health, onClick }: ConnectionStatusProps) {
  if (!connection) {
    return (
      <button
        type="button"
        onClick={onClick}
        className="flex items-center gap-1.5 h-7 px-2 rounded-md text-xs hover:bg-accent transition-colors"
        title="Connect to a remote machine via SSH"
      >
        <Plug className="h-3.5 w-3.5" />
        Connect
      </button>
    );
  }

  // Tri-state color: ok / degraded / unknown
  const color =
    health == null
      ? "bg-muted-foreground"
      : health.ok
        ? health.latency_ms > 800
          ? "bg-yellow-500"
          : "bg-green-500"
        : "bg-destructive";

  const label =
    connection.label || `${connection.user}@${connection.host}`;

  const tooltip = health
    ? health.ok
      ? `Connected — ${health.latency_ms} ms${
          connection.jump_host ? ` via ${connection.jump_host}` : ""
        }`
      : `Connection problem: ${health.message ?? "unknown"}`
    : `Connected to ${connection.user}@${connection.host}`;

  return (
    <button
      type="button"
      onClick={onClick}
      title={tooltip}
      className="flex items-center gap-1.5 h-7 px-2 rounded-md text-xs border border-transparent hover:border-border hover:bg-accent transition-colors"
    >
      <span className={`h-2 w-2 rounded-full ${color} ${health?.ok ? "animate-pulse" : ""}`} />
      <PlugZap className="h-3.5 w-3.5" />
      <span className="font-medium max-w-[140px] truncate">{label}</span>
      {health?.ok && (
        <span className="text-[10px] text-muted-foreground font-mono">
          {health.latency_ms}ms
        </span>
      )}
    </button>
  );
}
