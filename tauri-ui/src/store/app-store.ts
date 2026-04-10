import { create } from "zustand";
import type { StatusStack, RepoConfig } from "../types/ezstack";

interface RepoData {
  stacks: StatusStack[];
  currentBranch: string | null;
}

interface AppState {
  repos: RepoConfig[];
  repoDataCache: Record<string, RepoData>;
  selectedRepoPath: string | null;
  stacks: StatusStack[];
  selectedStackHash: string | null;
  selectedBranchName: string | null;
  focusedBranchIndex: number;
  currentBranch: string | null;
  initialLoading: boolean;
  isLoading: boolean;
  error: string | null;
  lastRefresh: Date | null;
  operationOutput: string | null;
  operationLoading: boolean;

  setRepos: (repos: RepoConfig[]) => void;
  setRepoData: (repoPath: string, data: RepoData) => void;
  selectRepo: (path: string | null) => void;
  setStacks: (stacks: StatusStack[]) => void;
  selectStack: (hash: string | null) => void;
  selectBranch: (name: string | null) => void;
  setFocusedBranchIndex: (index: number) => void;
  setCurrentBranch: (branch: string) => void;
  setInitialLoading: (loading: boolean) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  setLastRefresh: (date: Date) => void;
  setOperationOutput: (output: string | null) => void;
  setOperationLoading: (loading: boolean) => void;
}

export const useAppStore = create<AppState>((set, get) => ({
  repos: [],
  repoDataCache: {},
  selectedRepoPath: null,
  stacks: [],
  selectedStackHash: null,
  selectedBranchName: null,
  focusedBranchIndex: 0,
  currentBranch: null,
  initialLoading: true,
  isLoading: false,
  error: null,
  lastRefresh: null,
  operationOutput: null,
  operationLoading: false,

  setRepos: (repos) => set({ repos }),
  setRepoData: (repoPath, data) => {
    const cache = { ...get().repoDataCache, [repoPath]: data };
    const updates: Partial<AppState> = { repoDataCache: cache };
    // If this is the currently selected repo, update the active view too
    if (get().selectedRepoPath === repoPath) {
      updates.stacks = data.stacks;
      updates.currentBranch = data.currentBranch;
    }
    set(updates);
  },
  selectRepo: (path) => {
    const cached = path ? get().repoDataCache[path] : undefined;
    set({
      selectedRepoPath: path,
      stacks: cached?.stacks ?? [],
      currentBranch: cached?.currentBranch ?? null,
      selectedStackHash: null,
      selectedBranchName: null,
      error: null,
      lastRefresh: null,
    });
  },
  setStacks: (stacks) => set({ stacks }),
  selectStack: (hash) => set({ selectedStackHash: hash, focusedBranchIndex: 0 }),
  selectBranch: (name) => set({ selectedBranchName: name }),
  setFocusedBranchIndex: (index) => set({ focusedBranchIndex: index }),
  setCurrentBranch: (branch) => set({ currentBranch: branch }),
  setInitialLoading: (loading) => set({ initialLoading: loading }),
  setLoading: (loading) => set({ isLoading: loading }),
  setError: (error) => set({ error }),
  setLastRefresh: (date) => set({ lastRefresh: date }),
  setOperationOutput: (output) => set({ operationOutput: output }),
  setOperationLoading: (loading) => set({ operationLoading: loading }),
}));
