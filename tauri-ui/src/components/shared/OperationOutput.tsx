import { X } from "lucide-react";
import { Button } from "../ui/button";

interface OperationOutputProps {
  output: string;
  isLoading: boolean;
  onClose: () => void;
}

export function OperationOutput({ output, isLoading, onClose }: OperationOutputProps) {
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
