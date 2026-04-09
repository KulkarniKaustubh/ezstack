import { GitBranch, Folder, Clock } from "lucide-react";

interface StatusBarProps {
  repoPath: string | null;
  currentBranch: string | null;
  lastRefresh: Date | null;
}

export function StatusBar({ repoPath, currentBranch, lastRefresh }: StatusBarProps) {
  const formatTime = (d: Date) => {
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
  };

  return (
    <div className="flex items-center justify-between h-7 px-3 border-t bg-surface text-xs text-muted-foreground select-none">
      <div className="flex items-center gap-3">
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
