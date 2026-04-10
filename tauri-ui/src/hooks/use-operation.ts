import { useCallback } from "react";
import { useAppStore } from "../store/app-store";
import type { CommandResult } from "../types/ezstack";

export function useOperation() {
  const { setOperationOutput, setOperationLoading, setOperationSuccess } = useAppStore();

  const run = useCallback(
    async (operation: () => Promise<CommandResult>): Promise<CommandResult | null> => {
      setOperationLoading(true);
      setOperationOutput(null);
      setOperationSuccess(false);
      try {
        const result = await operation();
        const output = [result.stdout, result.stderr].filter(Boolean).join("\n");
        setOperationOutput(output || "Done.");
        setOperationSuccess(result.exit_code === 0);
        return result;
      } catch (e) {
        setOperationOutput(`Error: ${String(e)}`);
        setOperationSuccess(false);
        return null;
      } finally {
        setOperationLoading(false);
      }
    },
    [setOperationOutput, setOperationLoading, setOperationSuccess],
  );

  return { run };
}
