import { Folder, ChevronRight } from "lucide-react";
import { cn } from "../../lib/utils";
import type { RepoConfig } from "../../types/ezstack";

interface SidebarProps {
  repos: RepoConfig[];
  selectedRepoPath: string | null;
  onSelectRepo: (path: string) => void;
  width?: number;
}

function repoDisplayName(path: string): string {
  return path.split("/").pop() || path;
}

export function Sidebar({
  repos,
  selectedRepoPath,
  onSelectRepo,
  width,
}: SidebarProps) {
  return (
    <div className="flex flex-col h-full border-r bg-surface/50" style={width ? { width } : { width: 192 }}>
      <div className="flex items-center gap-1.5 px-3 py-2.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/60 border-b">
        <Folder className="h-3 w-3" />
        Repositories
      </div>
      <div className="flex-1 overflow-y-auto py-1 px-1">
        {repos.map((repo) => {
          const isSelected = repo.repo_path === selectedRepoPath;
          return (
            <button
              key={repo.repo_path}
              onClick={() => onSelectRepo(repo.repo_path)}
              className={cn(
                "w-full text-left px-2.5 py-2 rounded-md text-sm transition-colors",
                isSelected
                  ? "bg-primary text-primary-foreground"
                  : "hover:bg-accent/50 text-foreground",
              )}
            >
              <div className="flex items-center gap-2">
                <ChevronRight
                  className={cn(
                    "h-3 w-3 shrink-0 transition-transform",
                    isSelected && "rotate-90",
                  )}
                />
                <span className="font-medium truncate">{repoDisplayName(repo.repo_path)}</span>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}
