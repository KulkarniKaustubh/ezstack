import { Moon, Sun, RefreshCw, Settings, RefreshCcw } from "lucide-react";
import { useState } from "react";
import { Button } from "../ui/button";
import { Tooltip } from "../ui/tooltip";
import { useTheme } from "../../hooks/use-theme";

interface TitleBarProps {
  onRefresh: () => void;
  onSync: () => void;
  onSettings: () => void;
  isLoading: boolean;
}

export function TitleBar({ onRefresh, onSync, onSettings, isLoading }: TitleBarProps) {
  const { theme, toggle } = useTheme();
  const [refreshFlash, setRefreshFlash] = useState(false);

  const handleRefresh = () => {
    onRefresh();
    setRefreshFlash(true);
    setTimeout(() => setRefreshFlash(false), 600);
  };

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
          <span className="text-[10px] text-muted-foreground font-mono">v2.0.0-beta.2</span>
        </div>
      </div>

      <div className="flex items-center gap-1">
        <Tooltip content="Sync all stacks">
          <Button variant="ghost" size="icon-sm" onClick={onSync} disabled={isLoading}>
            <RefreshCcw className="h-3.5 w-3.5" />
          </Button>
        </Tooltip>
        <Tooltip content={`Switch to ${theme === "dark" ? "light" : "dark"} mode`}>
          <Button variant="ghost" size="icon-sm" onClick={toggle}>
            {theme === "dark" ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
          </Button>
        </Tooltip>
        <Tooltip content="Refresh (Cmd+R)">
          <Button variant="ghost" size="icon-sm" onClick={handleRefresh} disabled={isLoading}>
            <RefreshCw
              className={`h-3.5 w-3.5 transition-all ${isLoading ? "animate-spin" : ""} ${refreshFlash ? "text-success" : ""}`}
            />
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
