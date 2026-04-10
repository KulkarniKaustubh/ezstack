import { useEffect } from "react";
import { X } from "lucide-react";
import { Button } from "../ui/button";

interface OperationOutputProps {
  output: string;
  isLoading: boolean;
  isSuccess: boolean;
  onClose: () => void;
}

const AUTO_DISMISS_MS = 5_000;

export function OperationOutput({ output, isLoading, isSuccess, onClose }: OperationOutputProps) {
  // Auto-dismiss after a few seconds on success
  useEffect(() => {
    if (!isLoading && isSuccess) {
      const timer = setTimeout(onClose, AUTO_DISMISS_MS);
      return () => clearTimeout(timer);
    }
  }, [isLoading, isSuccess, onClose]);

  return (
    <div className="border-t bg-surface">
      <div className="flex items-center justify-between px-3 py-1.5 border-b">
        <div className="flex items-center gap-2">
          <span className="text-xs font-medium text-muted-foreground">Output</span>
          {isLoading && (
            <div className="h-1.5 w-1.5 rounded-full bg-warning animate-pulse" />
          )}
        </div>
        <Button variant="ghost" size="icon-sm" onClick={onClose}>
          <X className="h-3 w-3" />
        </Button>
      </div>
      <pre className="p-3 text-xs font-mono text-muted-foreground overflow-auto max-h-40 whitespace-pre-wrap">
        {output}
      </pre>
    </div>
  );
}
