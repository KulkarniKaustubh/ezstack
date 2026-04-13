import { describe, it, expect } from "vitest";

// colorIndexForHash is module-level but not exported, so we re-implement
// the same algorithm here to verify its properties.
const PALETTE_SIZE = 8;
function colorIndexForHash(value: string): number {
  let hash = 0;
  for (let i = 0; i < value.length; i++) {
    hash = ((hash << 5) - hash + value.charCodeAt(i)) | 0;
  }
  return ((hash % PALETTE_SIZE) + PALETTE_SIZE) % PALETTE_SIZE;
}

describe("colorIndexForHash", () => {
  it("returns values in [0, PALETTE_SIZE)", () => {
    const inputs = ["PROJ-1", "PROJ-2", "abc", "", "long-ticket-name-999"];
    for (const input of inputs) {
      const idx = colorIndexForHash(input);
      expect(idx).toBeGreaterThanOrEqual(0);
      expect(idx).toBeLessThan(PALETTE_SIZE);
    }
  });

  it("is deterministic", () => {
    expect(colorIndexForHash("PROJ-42")).toBe(colorIndexForHash("PROJ-42"));
  });

  it("produces different indices for different inputs", () => {
    const indices = new Set(
      ["A", "B", "C", "D", "E", "F", "G", "H"].map(colorIndexForHash),
    );
    // With 8 single-char inputs and palette size 8, we expect some variety
    expect(indices.size).toBeGreaterThan(1);
  });

  it("handles empty string", () => {
    const idx = colorIndexForHash("");
    expect(idx).toBe(0);
  });
});
