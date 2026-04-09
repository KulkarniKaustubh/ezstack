import { useEffect, useCallback, useRef } from "react";
import { useAppStore } from "../store/app-store";
import { getStacksStatus, getCurrentBranch, getEzstackRepos } from "../commands/ezs";

const POLL_INTERVAL = 30_000;

async function fetchRepoData(repoPath: string) {
  const [stacks, currentBranch] = await Promise.all([
    getStacksStatus(repoPath),
    getCurrentBranch(repoPath),
  ]);
  return { stacks, currentBranch };
}

export function useStacks() {
  const {
    setRepos,
    selectedRepoPath,
    setRepoData,
    setStacks,
    setCurrentBranch,
    setInitialLoading,
    setLoading,
    setError,
    setLastRefresh,
    selectRepo,
    selectStack,
    selectedStackHash,
  } = useAppStore();

  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const selectedStackHashRef = useRef(selectedStackHash);
  selectedStackHashRef.current = selectedStackHash;
  const initializedRef = useRef(false);

  // Initial load: fetch all repos and all their stacks in parallel
  useEffect(() => {
    if (initializedRef.current) return;
    initializedRef.current = true;

    (async () => {
      try {
        const allRepos = await getEzstackRepos();
        setRepos(allRepos);

        if (allRepos.length === 0) {
          setInitialLoading(false);
          return;
        }

        // Fetch stacks for ALL repos in parallel
        const results = await Promise.allSettled(
          allRepos.map(async (repo) => {
            const data = await fetchRepoData(repo.repo_path);
            setRepoData(repo.repo_path, data);
            return { path: repo.repo_path, data };
          }),
        );

        // Auto-select first repo
        const firstSuccess = results.find((r) => r.status === "fulfilled");
        if (firstSuccess && firstSuccess.status === "fulfilled") {
          selectRepo(firstSuccess.value.path);
          if (firstSuccess.value.data.stacks.length > 0) {
            selectStack(firstSuccess.value.data.stacks[0].hash);
          }
        }

        setLastRefresh(new Date());
      } catch (e) {
        setError(String(e));
      } finally {
        setInitialLoading(false);
      }
    })();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Refresh the selected repo (used by polling and manual refresh)
  const refresh = useCallback(async () => {
    if (!selectedRepoPath) return;

    setLoading(true);
    setError(null);
    try {
      const data = await fetchRepoData(selectedRepoPath);
      setRepoData(selectedRepoPath, data);
      setStacks(data.stacks);
      setCurrentBranch(data.currentBranch);
      setLastRefresh(new Date());

      if (!selectedStackHashRef.current && data.stacks.length > 0) {
        selectStack(data.stacks[0].hash);
      }
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }, [selectedRepoPath, setRepoData, setStacks, setCurrentBranch, setLoading, setError, setLastRefresh, selectStack]);

  // Refresh all repos (used by sync-all)
  const refreshAll = useCallback(async () => {
    const allRepos = useAppStore.getState().repos;
    if (allRepos.length === 0) return;

    setLoading(true);
    setError(null);
    try {
      await Promise.allSettled(
        allRepos.map(async (repo) => {
          const data = await fetchRepoData(repo.repo_path);
          setRepoData(repo.repo_path, data);
        }),
      );
      setLastRefresh(new Date());
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }, [setRepoData, setLoading, setError, setLastRefresh]);

  // Polling with focus/blur awareness
  useEffect(() => {
    const startPolling = () => {
      if (intervalRef.current) return;
      intervalRef.current = setInterval(refresh, POLL_INTERVAL);
    };

    const stopPolling = () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
    };

    const handleFocus = () => {
      refresh();
      startPolling();
    };

    const handleBlur = () => {
      stopPolling();
    };

    window.addEventListener("focus", handleFocus);
    window.addEventListener("blur", handleBlur);

    startPolling();

    return () => {
      window.removeEventListener("focus", handleFocus);
      window.removeEventListener("blur", handleBlur);
      stopPolling();
    };
  }, [refresh]);

  return { refresh, refreshAll };
}
