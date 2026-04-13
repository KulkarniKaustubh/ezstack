import { useCallback } from "react";
import { useAppStore } from "../store/app-store";
import { notify } from "../store/toast-store";
import type { CommandResult } from "../types/ezstack";

function firstLine(s: string, max = 160): string {
  const line = s.split("\n").find((l) => l.trim().length > 0) ?? "";
  return line.length > max ? line.slice(0, max) + "…" : line;
}

export function useOperation() {
  const { setOperationOutput, setOperationLoading, setOperationSuccess } = useAppStore();

  const run = useCallback(
    async (
      operation: () => Promise<CommandResult>,
      label?: string,
    ): Promise<CommandResult | null> => {
      setOperationLoading(true);
      setOperationOutput(null);
      setOperationSuccess(false);
      try {
        const result = await operation();
        const output = [result.stdout, result.stderr].filter(Boolean).join("\n");
        setOperationOutput(output || "Done.");
        const ok = result.exit_code === 0;
        setOperationSuccess(ok);
        notify({
          variant: ok ? "success" : "error",
          title: ok ? `${label ?? "Operation"} succeeded` : `${label ?? "Operation"} failed`,
          description: firstLine(output) || undefined,
        });
        return result;
      } catch (e) {
        const msg = String(e);
        setOperationOutput(`Error: ${msg}`);
        setOperationSuccess(false);
        notify({
          variant: "error",
          title: `${label ?? "Operation"} failed`,
          description: firstLine(msg),
        });
        return null;
      } finally {
        setOperationLoading(false);
      }
    },
    [setOperationOutput, setOperationLoading, setOperationSuccess],
  );

  return { run };
}
