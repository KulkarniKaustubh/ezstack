import { create } from "zustand";
import type { StatusStack, SshConnection } from "../types/ezstack";

interface AppState {
  repoPath: string | null;
  stacks: StatusStack[];
  selectedStackHash: string | null;
  selectedBranchName: string | null;
  currentBranch: string | null;
  isLoading: boolean;
  error: string | null;
  lastRefresh: Date | null;
  operationOutput: string | null;
  operationLoading: boolean;
  remoteConnection: SshConnection | null;

  setRepoPath: (path: string) => void;
  setStacks: (stacks: StatusStack[]) => void;
  selectStack: (hash: string | null) => void;
  selectBranch: (name: string | null) => void;
  setCurrentBranch: (branch: string) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  setLastRefresh: (date: Date) => void;
  setOperationOutput: (output: string | null) => void;
  setOperationLoading: (loading: boolean) => void;
  setRemoteConnection: (conn: SshConnection | null) => void;
  clearRepoState: () => void;
}

export const useAppStore = create<AppState>((set) => ({
  repoPath: null,
  stacks: [],
  selectedStackHash: null,
  selectedBranchName: null,
  currentBranch: null,
  isLoading: false,
  error: null,
  lastRefresh: null,
  operationOutput: null,
  operationLoading: false,
  remoteConnection: null,

  setRepoPath: (path) => set({ repoPath: path }),
  setStacks: (stacks) => set({ stacks }),
  selectStack: (hash) => set({ selectedStackHash: hash, selectedBranchName: null }),
  selectBranch: (name) => set({ selectedBranchName: name }),
  setCurrentBranch: (branch) => set({ currentBranch: branch }),
  setLoading: (loading) => set({ isLoading: loading }),
  setError: (error) => set({ error }),
  setLastRefresh: (date) => set({ lastRefresh: date }),
  setOperationOutput: (output) => set({ operationOutput: output }),
  setOperationLoading: (loading) => set({ operationLoading: loading }),
  setRemoteConnection: (conn) => set({ remoteConnection: conn }),
  clearRepoState: () =>
    set({
      repoPath: null,
      stacks: [],
      selectedStackHash: null,
      selectedBranchName: null,
      currentBranch: null,
      error: null,
      lastRefresh: null,
    }),
}));
