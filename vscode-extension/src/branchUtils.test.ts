import { describe, it, expect } from "vitest";
import { shortBranchName, extractTicket } from "./branchUtils";

describe("shortBranchName", () => {
  it("returns last segment of dotted name", () => {
    expect(shortBranchName("stack.feature.login")).toBe("login");
  });

  it("returns full name when no dots", () => {
    expect(shortBranchName("my-branch")).toBe("my-branch");
  });

  it("handles single segment with dot prefix", () => {
    expect(shortBranchName("a.b")).toBe("b");
  });

  it("handles empty string", () => {
    expect(shortBranchName("")).toBe("");
  });
});

describe("extractTicket", () => {
  it("returns undefined when no ticket pattern is configured", () => {
    // Default mock returns empty string for ticketPattern
    expect(extractTicket("PROJ-123-add-feature")).toBeUndefined();
  });
});
