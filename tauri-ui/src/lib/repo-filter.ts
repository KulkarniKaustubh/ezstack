import type { RepoConfig } from "../types/ezstack";

export function repoDisplayName(path: string): string {
  return path.split("/").pop() || path;
}

/**
 * Filter repos by a free-text query (name or full path, case-insensitive).
 * The currently selected repo is always included — even when it doesn't match
 * the filter — so the UI can never enter a state where the selection is
 * invisible and unreachable.
 */
export function filterRepos(
  repos: RepoConfig[],
  query: string,
  selectedRepoPath: string | null,
): RepoConfig[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return repos;
  const matches = (r: RepoConfig) =>
    repoDisplayName(r.repo_path).toLowerCase().includes(needle) ||
    r.repo_path.toLowerCase().includes(needle);
  return repos.filter((r) => matches(r) || r.repo_path === selectedRepoPath);
}
