import { useCallback } from "react";
import { useAppStore } from "../store/app-store";
import type { CommandResult } from "../types/ezstack";

export function useOperation() {
  const { setOperationOutput, setOperationLoading } = useAppStore();

  const run = useCallback(
    async (operation: () => Promise<CommandResult>): Promise<CommandResult | null> => {
      setOperationLoading(true);
      setOperationOutput(null);
      try {
        const result = await operation();
        const output = [result.stdout, result.stderr].filter(Boolean).join("\n");
        setOperationOutput(output || "Done.");
        return result;
      } catch (e) {
        setOperationOutput(`Error: ${String(e)}`);
        return null;
      } finally {
        setOperationLoading(false);
      }
    },
    [setOperationOutput, setOperationLoading],
  );

  return { run };
}
