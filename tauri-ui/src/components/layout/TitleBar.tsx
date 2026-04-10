import { Moon, Sun, RefreshCw, Settings, RefreshCcw } from "lucide-react";
import { useState } from "react";
import { Button } from "../ui/button";
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
        <span className="text-sm font-semibold tracking-tight">ezstack</span>
        <span className="text-[10px] text-muted-foreground font-mono">v2.0.0-beta.6</span>
      </div>

      <div className="flex items-center gap-1">
        <Button variant="ghost" size="sm" onClick={onSync} disabled={isLoading} className="gap-1.5 text-xs">
          <RefreshCcw className="h-3.5 w-3.5" />
          Sync
        </Button>
        <Button variant="ghost" size="sm" onClick={toggle} className="gap-1.5 text-xs">
          {theme === "dark" ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
          {theme === "dark" ? "Light" : "Dark"}
        </Button>
        <Button variant="ghost" size="sm" onClick={handleRefresh} disabled={isLoading} className="gap-1.5 text-xs">
          <RefreshCw
            className={`h-3.5 w-3.5 transition-all ${isLoading ? "animate-spin" : ""} ${refreshFlash ? "text-success" : ""}`}
          />
          Refresh
        </Button>
        <Button variant="ghost" size="sm" onClick={onSettings} className="gap-1.5 text-xs">
          <Settings className="h-3.5 w-3.5" />
          Settings
        </Button>
      </div>
    </div>
  );
}
