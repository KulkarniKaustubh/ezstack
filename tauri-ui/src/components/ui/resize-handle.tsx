import { cn } from "../../lib/utils";

interface ResizeHandleProps {
  onMouseDown: (e: React.MouseEvent) => void;
  isResizing: boolean;
}

export function ResizeHandle({ onMouseDown, isResizing }: ResizeHandleProps) {
  return (
    <div
      className="relative shrink-0 w-0 cursor-col-resize group"
      onMouseDown={onMouseDown}
    >
      {/* Wide invisible hit area */}
      <div className="absolute inset-y-0 -left-2 w-4 z-10" />
      {/* Visible line */}
      <div
        className={cn(
          "absolute inset-y-0 left-0 w-px transition-colors",
          isResizing ? "bg-primary" : "bg-transparent group-hover:bg-primary/40",
        )}
      />
    </div>
  );
}
