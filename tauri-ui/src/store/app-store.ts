import { create } from "zustand";
import type { StatusStack, RepoConfig } from "../types/ezstack";

interface AppState {
  repos: RepoConfig[];
  selectedRepoPath: string | null;
  stacks: StatusStack[];
  selectedStackHash: string | null;
  selectedBranchName: string | null;
  currentBranch: string | null;
  isLoading: boolean;
  error: string | null;
  lastRefresh: Date | null;
  operationOutput: string | null;
  operationLoading: boolean;

  setRepos: (repos: RepoConfig[]) => void;
  selectRepo: (path: string | null) => void;
  setStacks: (stacks: StatusStack[]) => void;
  selectStack: (hash: string | null) => void;
  selectBranch: (name: string | null) => void;
  setCurrentBranch: (branch: string) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  setLastRefresh: (date: Date) => void;
  setOperationOutput: (output: string | null) => void;
  setOperationLoading: (loading: boolean) => void;
}

export const useAppStore = create<AppState>((set) => ({
  repos: [],
  selectedRepoPath: null,
  stacks: [],
  selectedStackHash: null,
  selectedBranchName: null,
  currentBranch: null,
  isLoading: false,
  error: null,
  lastRefresh: null,
  operationOutput: null,
  operationLoading: false,

  setRepos: (repos) => set({ repos }),
  selectRepo: (path) =>
    set({
      selectedRepoPath: path,
      stacks: [],
      selectedStackHash: null,
      selectedBranchName: null,
      currentBranch: null,
      error: null,
      lastRefresh: null,
    }),
  setStacks: (stacks) => set({ stacks }),
  selectStack: (hash) => set({ selectedStackHash: hash, selectedBranchName: null }),
  selectBranch: (name) => set({ selectedBranchName: name }),
  setCurrentBranch: (branch) => set({ currentBranch: branch }),
  setLoading: (loading) => set({ isLoading: loading }),
  setError: (error) => set({ error }),
  setLastRefresh: (date) => set({ lastRefresh: date }),
  setOperationOutput: (output) => set({ operationOutput: output }),
  setOperationLoading: (loading) => set({ operationLoading: loading }),
}));
