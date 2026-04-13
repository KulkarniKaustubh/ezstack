import { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "../../lib/utils";

export interface ContextMenuItem {
  label: string;
  icon?: React.ReactNode;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
  separator?: boolean;
}

interface ContextMenuProps {
  items: ContextMenuItem[];
  position: { x: number; y: number } | null;
  onClose: () => void;
}

export function ContextMenu({ items, position, onClose }: ContextMenuProps) {
  const menuRef = useRef<HTMLDivElement>(null);
  const [adjusted, setAdjusted] = useState<{ x: number; y: number } | null>(null);

  // Adjust position to stay within viewport
  useEffect(() => {
    if (!position || !menuRef.current) {
      setAdjusted(null);
      return;
    }
    const rect = menuRef.current.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    setAdjusted({
      x: position.x + rect.width > vw ? vw - rect.width - 8 : position.x,
      y: position.y + rect.height > vh ? vh - rect.height - 8 : position.y,
    });
  }, [position]);

  // Close on click outside, Escape, scroll
  useEffect(() => {
    if (!position) return;

    const handleClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        onClose();
      }
    };
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    const handleScroll = () => onClose();

    document.addEventListener("mousedown", handleClick);
    document.addEventListener("keydown", handleKey);
    window.addEventListener("scroll", handleScroll, true);
    return () => {
      document.removeEventListener("mousedown", handleClick);
      document.removeEventListener("keydown", handleKey);
      window.removeEventListener("scroll", handleScroll, true);
    };
  }, [position, onClose]);

  if (!position) return null;

  const pos = adjusted || position;

  return (
    <div
      ref={menuRef}
      className="fixed z-50 min-w-[160px] rounded-lg border bg-popover p-1 shadow-lg animate-in fade-in-0 zoom-in-95"
      style={{ left: pos.x, top: pos.y }}
    >
      {items.map((item, i) => {
        if (item.separator) {
          return <div key={i} className="my-1 h-px bg-border" />;
        }
        return (
          <button
            key={i}
            onClick={() => {
              if (!item.disabled) {
                item.onClick();
                onClose();
              }
            }}
            disabled={item.disabled}
            className={cn(
              "flex items-center gap-2 w-full rounded-md px-2.5 py-1.5 text-xs transition-colors",
              item.disabled
                ? "text-muted-foreground/50 cursor-not-allowed"
                : item.danger
                  ? "text-destructive hover:bg-destructive/10"
                  : "text-foreground hover:bg-accent",
            )}
          >
            {item.icon && <span className="shrink-0 [&>svg]:h-3.5 [&>svg]:w-3.5">{item.icon}</span>}
            {item.label}
          </button>
        );
      })}
    </div>
  );
}

export function useContextMenu() {
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null);

  const onContextMenu = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setPosition({ x: e.clientX, y: e.clientY });
  }, []);

  const onClose = useCallback(() => {
    setPosition(null);
  }, []);

  return { position, onContextMenu, onClose };
}
