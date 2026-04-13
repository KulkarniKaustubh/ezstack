import { Folder, ChevronRight, Search } from "lucide-react";
import { useState } from "react";
import { cn } from "../../lib/utils";
import { Input } from "../ui/input";
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
  const [filter, setFilter] = useState("");
  const needle = filter.trim().toLowerCase();
  const visibleRepos = needle
    ? repos.filter(
        (r) =>
          repoDisplayName(r.repo_path).toLowerCase().includes(needle) ||
          r.repo_path.toLowerCase().includes(needle),
      )
    : repos;

  return (
    <div className="flex flex-col h-full border-r bg-surface/50" style={width ? { width } : { width: 192 }}>
      <div className="flex items-center gap-1.5 px-3 py-2.5 text-[10px] font-medium uppercase tracking-wider text-muted-foreground/60 border-b">
        <Folder className="h-3 w-3" />
        Repositories
      </div>
      <div className="px-2 py-2 border-b">
        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3 w-3 text-muted-foreground" />
          <Input
            type="text"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter…"
            className="h-7 pl-7 text-xs"
          />
        </div>
      </div>
      <div className="flex-1 overflow-y-auto py-1 px-1">
        {visibleRepos.length === 0 && (
          <div className="px-2.5 py-2 text-xs text-muted-foreground">No matches</div>
        )}
        {visibleRepos.map((repo) => {
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
