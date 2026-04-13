import { create } from "zustand";

export type ToastVariant = "info" | "success" | "error";

export interface Toast {
  id: number;
  title: string;
  description?: string;
  variant: ToastVariant;
}

interface ToastState {
  toasts: Toast[];
  push: (toast: Omit<Toast, "id">) => number;
  dismiss: (id: number) => void;
  clear: () => void;
}

// Cap the number of concurrently visible toasts. Older entries are evicted
// FIFO so a burst of operations can't grow the array without bound.
export const MAX_TOASTS = 5;

let nextId = 1;

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  push: (toast) => {
    const id = nextId++;
    set((s) => {
      const next = [...s.toasts, { id, ...toast }];
      return { toasts: next.length > MAX_TOASTS ? next.slice(next.length - MAX_TOASTS) : next };
    });
    return id;
  },
  dismiss: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
  clear: () => set({ toasts: [] }),
}));

export function notify(toast: Omit<Toast, "id">) {
  return useToastStore.getState().push(toast);
}

// Test hook: reset module state between tests.
export function __resetToastStoreForTests() {
  nextId = 1;
  useToastStore.getState().clear();
}
