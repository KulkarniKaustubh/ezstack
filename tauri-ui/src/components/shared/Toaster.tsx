import { useEffect } from "react";
import { CheckCircle2, AlertCircle, Info, X } from "lucide-react";
import { useToastStore, type Toast } from "../../store/toast-store";
import { cn } from "../../lib/utils";

const ICONS = {
  info: Info,
  success: CheckCircle2,
  error: AlertCircle,
};

const STYLES = {
  info: "border-info/40 bg-info/10 text-foreground",
  success: "border-success/40 bg-success/10 text-foreground",
  error: "border-destructive/40 bg-destructive/10 text-foreground",
};

const AUTO_DISMISS_MS = 5000;

function ToastItem({ toast }: { toast: Toast }) {
  const dismiss = useToastStore((s) => s.dismiss);
  const Icon = ICONS[toast.variant];

  useEffect(() => {
    // Errors stay on screen until the user dismisses them — they usually
    // contain CLI output the user may need to copy.
    if (toast.variant === "error") return;
    const timer = setTimeout(() => dismiss(toast.id), AUTO_DISMISS_MS);
    return () => clearTimeout(timer);
  }, [toast.id, toast.variant, dismiss]);

  return (
    <div
      className={cn(
        "pointer-events-auto flex items-start gap-2 rounded-md border px-3 py-2 shadow-md min-w-[260px] max-w-sm text-sm",
        STYLES[toast.variant],
      )}
      role={toast.variant === "error" ? "alert" : "status"}
    >
      <Icon className="h-4 w-4 mt-0.5 shrink-0" />
      <div className="flex-1 min-w-0">
        <div className="font-medium truncate">{toast.title}</div>
        {toast.description && (
          <div className="text-xs text-muted-foreground line-clamp-3 break-words">
            {toast.description}
          </div>
        )}
      </div>
      <button
        type="button"
        onClick={() => dismiss(toast.id)}
        className="text-muted-foreground hover:text-foreground shrink-0"
        aria-label="Dismiss notification"
      >
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

export function Toaster() {
  const toasts = useToastStore((s) => s.toasts);
  if (toasts.length === 0) return null;
  return (
    <div
      className="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2"
      aria-live="polite"
      aria-atomic="false"
      aria-label="Notifications"
    >
      {toasts.map((t) => (
        <ToastItem key={t.id} toast={t} />
      ))}
    </div>
  );
}
