import { useEffect, useRef, useState, useCallback } from "react";
import * as ezs from "../commands/ezs";
import type { ConnectionHealth } from "../types/ezstack";

const PING_INTERVAL_MS = 30_000;

/**
 * Periodically pings the active SSH connection (if any) to surface latency
 * and detect drops. Pings only run while `enabled` is true and the window
 * has focus.
 */
export function useConnectionHealth(enabled: boolean) {
  const [health, setHealth] = useState<ConnectionHealth | null>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const inFlightRef = useRef(false);

  const ping = useCallback(async () => {
    if (inFlightRef.current) return;
    inFlightRef.current = true;
    try {
      const result = await ezs.pingConnection();
      setHealth(result);
    } catch (e) {
      setHealth({
        ok: false,
        latency_ms: 0,
        message: e instanceof Error ? e.message : String(e),
      });
    } finally {
      inFlightRef.current = false;
    }
  }, []);

  useEffect(() => {
    if (!enabled) {
      setHealth(null);
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
      return;
    }

    ping();

    const start = () => {
      if (intervalRef.current) return;
      intervalRef.current = setInterval(ping, PING_INTERVAL_MS);
    };
    const stop = () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
    };
    const onFocus = () => {
      ping();
      start();
    };
    const onBlur = () => stop();

    start();
    window.addEventListener("focus", onFocus);
    window.addEventListener("blur", onBlur);

    return () => {
      stop();
      window.removeEventListener("focus", onFocus);
      window.removeEventListener("blur", onBlur);
    };
  }, [enabled, ping]);

  return { health, ping };
}
