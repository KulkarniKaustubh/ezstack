import { describe, it, expect } from "vitest";
import { EzsCli } from "./ezsCli";

// Access private static methods via the class prototype for testing.
// These are pure utility functions with no side effects.
const shellQuote = (EzsCli as unknown as Record<string, (s: string) => string>)
  .shellQuote;
const stripAnsi = (EzsCli as unknown as Record<string, (s: string) => string>)
  .stripAnsi;
const parseJSON = (
  EzsCli as unknown as Record<string, <T>(raw: string, cmd: string) => T>
).parseJSON;

describe("shellQuote", () => {
  it("returns safe strings unquoted", () => {
    expect(shellQuote("hello")).toBe("hello");
    expect(shellQuote("path/to/file.txt")).toBe("path/to/file.txt");
    expect(shellQuote("user@host:repo")).toBe("user@host:repo");
    expect(shellQuote("a=b")).toBe("a=b");
  });

  it("quotes strings with spaces", () => {
    expect(shellQuote("hello world")).toBe("'hello world'");
  });

  it("escapes single quotes inside values", () => {
    expect(shellQuote("it's")).toBe("'it'\\''s'");
  });

  it("quotes strings with special characters", () => {
    expect(shellQuote("$(whoami)")).toBe("'$(whoami)'");
    expect(shellQuote("a;b")).toBe("'a;b'");
  });
});

describe("stripAnsi", () => {
  it("removes ANSI escape codes", () => {
    expect(stripAnsi("\x1b[31mred text\x1b[0m")).toBe("red text");
  });

  it("passes through plain text unchanged", () => {
    expect(stripAnsi("plain text")).toBe("plain text");
  });

  it("removes multiple codes", () => {
    expect(stripAnsi("\x1b[1m\x1b[32mbold green\x1b[0m")).toBe("bold green");
  });
});

describe("parseJSON", () => {
  it("parses valid JSON", () => {
    expect(parseJSON('{"key":"value"}', "test")).toEqual({ key: "value" });
  });

  it("parses arrays", () => {
    expect(parseJSON("[1,2,3]", "test")).toEqual([1, 2, 3]);
  });

  it("throws descriptive error on invalid JSON", () => {
    expect(() => parseJSON("not json", "list --json")).toThrow(
      "ezs list --json returned invalid JSON",
    );
  });
});
