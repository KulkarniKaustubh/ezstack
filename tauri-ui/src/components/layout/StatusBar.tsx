import { GitBranch, Folder, Clock, Monitor, Unplug } from "lucide-react";
import { Button } from "../ui/button";
import { Tooltip } from "../ui/tooltip";
import type { SshConnection } from "../../types/ezstack";

interface StatusBarProps {
  repoPath: string | null;
  currentBranch: string | null;
  lastRefresh: Date | null;
  remoteConnection: SshConnection | null;
  onDisconnect?: () => void;
}

export function StatusBar({ repoPath, currentBranch, lastRefresh, remoteConnection, onDisconnect }: StatusBarProps) {
  const formatTime = (d: Date) => {
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  };

  return (
    <div className="flex items-center justify-between h-7 px-3 border-t bg-surface text-xs text-muted-foreground select-none">
      <div className="flex items-center gap-3">
        {remoteConnection && (
          <div className="flex items-center gap-1 text-info">
            <Monitor className="h-3 w-3" />
            <span className="font-mono">
              {remoteConnection.user}@{remoteConnection.host}
            </span>
            {onDisconnect && (
              <Tooltip content="Disconnect from remote">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="h-4 w-4 ml-0.5"
                  onClick={onDisconnect}
                >
                  <Unplug className="h-2.5 w-2.5" />
                </Button>
              </Tooltip>
            )}
          </div>
        )}
        {repoPath && (
          <div className="flex items-center gap-1">
            <Folder className="h-3 w-3" />
            <span className="font-mono truncate max-w-[300px]">{repoPath}</span>
          </div>
        )}
        {currentBranch && (
          <div className="flex items-center gap-1">
            <GitBranch className="h-3 w-3" />
            <span className="font-mono">{currentBranch}</span>
          </div>
        )}
      </div>
      {lastRefresh && (
        <div className="flex items-center gap-1">
          <Clock className="h-3 w-3" />
          <span>{formatTime(lastRefresh)}</span>
        </div>
      )}
    </div>
  );
}
