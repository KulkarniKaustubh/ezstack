import { describe, it, expect, beforeEach } from "vitest";
import {
  MAX_TOASTS,
  notify,
  useToastStore,
  __resetToastStoreForTests,
} from "./toast-store";

beforeEach(() => {
  __resetToastStoreForTests();
});

describe("toast-store", () => {
  it("push adds a toast with a unique id", () => {
    const id1 = notify({ variant: "info", title: "one" });
    const id2 = notify({ variant: "info", title: "two" });
    expect(id1).not.toBe(id2);
    expect(useToastStore.getState().toasts).toHaveLength(2);
  });

  it("evicts oldest toasts once MAX_TOASTS is exceeded (FIFO)", () => {
    for (let i = 0; i < MAX_TOASTS + 3; i++) {
      notify({ variant: "info", title: `toast-${i}` });
    }
    const { toasts } = useToastStore.getState();
    expect(toasts).toHaveLength(MAX_TOASTS);
    // The first 3 should have been evicted.
    expect(toasts[0].title).toBe(`toast-3`);
    expect(toasts[toasts.length - 1].title).toBe(`toast-${MAX_TOASTS + 2}`);
  });

  it("dismiss removes a specific toast by id", () => {
    const id = notify({ variant: "error", title: "boom" });
    notify({ variant: "info", title: "keep" });
    useToastStore.getState().dismiss(id);
    const titles = useToastStore.getState().toasts.map((t) => t.title);
    expect(titles).toEqual(["keep"]);
  });

  it("clear wipes all toasts", () => {
    notify({ variant: "info", title: "a" });
    notify({ variant: "info", title: "b" });
    useToastStore.getState().clear();
    expect(useToastStore.getState().toasts).toHaveLength(0);
  });
});
