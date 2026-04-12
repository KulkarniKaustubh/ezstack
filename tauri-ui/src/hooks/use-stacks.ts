import { useEffect, useCallback, useRef } from "react";
import { useAppStore } from "../store/app-store";
import { getStacksStatus, getCurrentBranch, getEzstackRepos } from "../commands/ezs";

const POLL_INTERVAL_MS = 30_000;
const MAX_POLL_INTERVAL_MS = 5 * 60_000;

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
    setRepoData,
    setStacks,
    setCurrentBranch,
    setInitialLoading,
    setLoading,
    setError,
    setLastRefresh,
    selectRepo,
  } = useAppStore();

  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const initializedRef = useRef(false);
  const failureCountRef = useRef(0);

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

        const results = await Promise.allSettled(
          allRepos.map(async (repo) => {
            const data = await fetchRepoData(repo.repo_path);
            setRepoData(repo.repo_path, data);
            return { path: repo.repo_path, data };
          }),
        );

        const firstSuccess = results.find((r) => r.status === "fulfilled");
        if (firstSuccess && firstSuccess.status === "fulfilled") {
          selectRepo(firstSuccess.value.path);
        } else {
          selectRepo(allRepos[0].repo_path);
        }

        setLastRefresh(new Date());
      } catch (e) {
        setError(String(e));
      } finally {
        setInitialLoading(false);
      }
    })();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Refresh the selected repo (used by polling and manual refresh).
  // Reads selectedRepoPath from the live store rather than capturing it via
  // closure — otherwise calling refresh() right after selectRepo(x) would
  // refresh the *old* path instead of x.
  const refresh = useCallback(async () => {
    const path = useAppStore.getState().selectedRepoPath;
    if (!path) return;

    setLoading(true);
    setError(null);
    try {
      const data = await fetchRepoData(path);
      setRepoData(path, data);
      // Only push into the active view if the user hasn't switched repos mid-flight.
      if (useAppStore.getState().selectedRepoPath === path) {
        setStacks(data.stacks);
        setCurrentBranch(data.currentBranch);
      }
      setLastRefresh(new Date());
      failureCountRef.current = 0;
    } catch (e) {
      failureCountRef.current += 1;
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }, [setRepoData, setStacks, setCurrentBranch, setLoading, setError, setLastRefresh]);

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

  // Polling with exponential backoff on failures + focus/blur awareness.
  useEffect(() => {
    let cancelled = false;

    const computeDelay = () => {
      const failures = failureCountRef.current;
      if (failures === 0) return POLL_INTERVAL_MS;
      // 30s → 60s → 120s → 240s → 300s (cap)
      const exp = POLL_INTERVAL_MS * Math.pow(2, Math.min(failures - 1, 4));
      return Math.min(exp, MAX_POLL_INTERVAL_MS);
    };

    const schedule = () => {
      if (cancelled) return;
      if (timeoutRef.current) clearTimeout(timeoutRef.current);
      const delay = computeDelay();
      timeoutRef.current = setTimeout(async () => {
        if (cancelled) return;
        await refresh();
        schedule();
      }, delay);
    };

    const stop = () => {
      cancelled = true;
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
        timeoutRef.current = null;
      }
    };

    const handleFocus = () => {
      cancelled = false;
      // Reset backoff on regaining focus so we get a fresh attempt.
      failureCountRef.current = 0;
      refresh();
      schedule();
    };

    const handleBlur = () => {
      stop();
    };

    window.addEventListener("focus", handleFocus);
    window.addEventListener("blur", handleBlur);

    schedule();

    return () => {
      window.removeEventListener("focus", handleFocus);
      window.removeEventListener("blur", handleBlur);
      stop();
    };
  }, [refresh]);

  return { refresh, refreshAll };
}
