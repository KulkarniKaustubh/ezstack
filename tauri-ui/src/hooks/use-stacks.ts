import { useEffect, useCallback, useRef } from "react";
import { useAppStore } from "../store/app-store";
import { getStacksStatus, getCurrentBranch } from "../commands/ezs";

const POLL_INTERVAL = 30_000;

export function useStacks() {
  const {
    repoPath,
    setStacks,
    setCurrentBranch,
    setLoading,
    setError,
    setLastRefresh,
    selectStack,
    selectedStackHash,
  } = useAppStore();

  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const selectedStackHashRef = useRef(selectedStackHash);
  selectedStackHashRef.current = selectedStackHash;

  const refresh = useCallback(async () => {
    if (!repoPath) return;

    setLoading(true);
    setError(null);
    try {
      const [stacks, branch] = await Promise.all([
        getStacksStatus(repoPath),
        getCurrentBranch(repoPath),
      ]);
      setStacks(stacks);
      setCurrentBranch(branch);
      setLastRefresh(new Date());

      // Auto-select first stack if none selected
      if (!selectedStackHashRef.current && stacks.length > 0) {
        selectStack(stacks[0].hash);
      }
    } catch (e) {
      setError(String(e));
    } finally {
      setLoading(false);
    }
  }, [repoPath, setStacks, setCurrentBranch, setLoading, setError, setLastRefresh, selectStack]);

  // Polling with focus/blur awareness
  useEffect(() => {
    const startPolling = () => {
      if (intervalRef.current) return;
      refresh();
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

  return { refresh };
}
