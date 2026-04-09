import * as React from "react";
import { cn } from "../../lib/utils";

interface TooltipProps {
  content: string;
  children: React.ReactNode;
  className?: string;
}

function Tooltip({ content, children, className }: TooltipProps) {
  const [show, setShow] = React.useState(false);
  const [pos, setPos] = React.useState({ x: 0, y: 0 });
  const ref = React.useRef<HTMLDivElement>(null);

  return (
    <div
      ref={ref}
      className="relative inline-flex"
      onMouseEnter={(e) => {
        const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
        setPos({ x: rect.left + rect.width / 2, y: rect.top });
        setShow(true);
      }}
      onMouseLeave={() => setShow(false)}
    >
      {children}
      {show && (
        <div
          className={cn(
            "fixed z-50 -translate-x-1/2 -translate-y-full -mt-2 px-2 py-1 text-xs rounded-md bg-popover text-popover-foreground border shadow-md whitespace-nowrap",
            className,
          )}
          style={{ left: pos.x, top: pos.y }}
        >
          {content}
        </div>
      )}
    </div>
  );
}

export { Tooltip };
