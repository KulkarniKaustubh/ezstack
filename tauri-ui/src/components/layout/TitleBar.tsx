import { Moon, Sun, Monitor, RefreshCw, Settings, Unplug } from "lucide-react";
import { Button } from "../ui/button";
import { Tooltip } from "../ui/tooltip";
import { useTheme } from "../../hooks/use-theme";

interface TitleBarProps {
  onRefresh: () => void;
  onSettings: () => void;
  onConnectRemote: () => void;
  onDisconnectRemote: () => void;
  isLoading: boolean;
  isRemote: boolean;
}

export function TitleBar({
  onRefresh,
  onSettings,
  onConnectRemote,
  onDisconnectRemote,
  isLoading,
  isRemote,
}: TitleBarProps) {
  const { theme, setTheme } = useTheme();

  const nextTheme = theme === "light" ? "dark" : theme === "dark" ? "system" : "light";
  const ThemeIcon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor;

  return (
    <div
      className="flex items-center justify-between h-12 px-4 border-b bg-background/80 backdrop-blur-sm select-none"
      data-tauri-drag-region
    >
      <div className="flex items-center gap-2" data-tauri-drag-region>
        <div className="flex items-center gap-2">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" className="text-primary">
            <path
              d="M12 2L4 6v6c0 5.55 3.84 10.74 8 12 4.16-1.26 8-6.45 8-12V6l-8-4z"
              stroke="currentColor"
              strokeWidth="2"
              fill="none"
            />
            <path d="M8 12h8M12 8v8" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </svg>
          <span className="text-sm font-semibold tracking-tight">ezstack</span>
          <span className="text-[10px] text-muted-foreground font-mono">v2.0.0-beta.4</span>
          {isRemote && (
            <span className="text-[10px] bg-info/15 text-info px-1.5 py-0.5 rounded font-medium">
              REMOTE
            </span>
          )}
        </div>
      </div>

      <div className="flex items-center gap-1">
        {isRemote ? (
          <Tooltip content="Disconnect from remote">
            <Button variant="ghost" size="icon-sm" onClick={onDisconnectRemote}>
              <Unplug className="h-3.5 w-3.5" />
            </Button>
          </Tooltip>
        ) : (
          <Tooltip content="Connect to remote machine">
            <Button variant="ghost" size="icon-sm" onClick={onConnectRemote}>
              <Monitor className="h-3.5 w-3.5" />
            </Button>
          </Tooltip>
        )}
        <Tooltip content={`Switch to ${nextTheme} theme`}>
          <Button variant="ghost" size="icon-sm" onClick={() => setTheme(nextTheme)}>
            <ThemeIcon className="h-3.5 w-3.5" />
          </Button>
        </Tooltip>
        <Tooltip content="Refresh (Cmd+R)">
          <Button variant="ghost" size="icon-sm" onClick={onRefresh} disabled={isLoading}>
            <RefreshCw className={`h-3.5 w-3.5 ${isLoading ? "animate-spin" : ""}`} />
          </Button>
        </Tooltip>
        <Tooltip content="Settings">
          <Button variant="ghost" size="icon-sm" onClick={onSettings}>
            <Settings className="h-3.5 w-3.5" />
          </Button>
        </Tooltip>
      </div>
    </div>
  );
}
