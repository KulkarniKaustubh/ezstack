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

describe("EzsCli.parseVersionSemver", () => {
  it("parses the standard ezstack version line", () => {
    expect(EzsCli.parseVersionSemver("ezstack version 4.7.6\n")).toEqual({
      major: 4,
      minor: 7,
      patch: 6,
    });
  });

  it("parses a leading 'v' prefix", () => {
    expect(EzsCli.parseVersionSemver("ezstack version v4.7.0\n")).toEqual({
      major: 4,
      minor: 7,
      patch: 0,
    });
  });

  it("anchors on 'version ' so trailing build metadata can't introduce a spurious match", () => {
    // If the regex only matched any numeric triple, the build-metadata
    // hash 2025.05.03 below would race the real triple. The anchor
    // prevents that — we should still get 1.2.3.
    expect(EzsCli.parseVersionSemver("ezstack version 1.2.3-rc.1+2025.05.03")).toEqual({
      major: 1,
      minor: 2,
      patch: 3,
    });
  });

  it("returns null when the version line is missing", () => {
    expect(EzsCli.parseVersionSemver("")).toBeNull();
    expect(EzsCli.parseVersionSemver("garbage 1.2 not enough segments")).toBeNull();
  });

  it("ignores leading non-version lines", () => {
    // Future-proofing: if `ezs --version` ever prepends a deprecation
    // notice, the parser should still find the canonical line.
    const out = ["warning: outdated config schema", "ezstack version 4.7.5"].join("\n");
    expect(EzsCli.parseVersionSemver(out)).toEqual({ major: 4, minor: 7, patch: 5 });
  });

  it("does not pick up a non-version triple like `git 2.43.0`", () => {
    // Without the 'version ' anchor we'd match `2.43.0` from doctor-style
    // output and wrongly claim the CLI is 2.43.0.
    const out = "git: 2.43.0\nfzf: 0.45.0\n"; // no `ezstack version` line
    expect(EzsCli.parseVersionSemver(out)).toBeNull();
  });

  it("parses the ezs-mcp variant emitted as 'ezstack-mcp version X.Y.Z'", () => {
    // The MCP server's --version line was tightened to share the
    // 'version ' anchor with the main CLI — verify the regex matches
    // both binaries identically so a future caller that points the
    // gate at ezs-mcp doesn't silently fall through to "unknown
    // version → skip warning".
    expect(EzsCli.parseVersionSemver("ezstack-mcp version 4.7.6\n")).toEqual({
      major: 4,
      minor: 7,
      patch: 6,
    });
  });

  it("tolerates Windows CRLF line endings", () => {
    // ezs --version on Windows ends lines with \r\n. The regex must
    // tolerate the carriage return — \b matches between `6` and `\r`,
    // so we expect the same triple as the LF case.
    expect(EzsCli.parseVersionSemver("ezstack version 4.7.6\r\n")).toEqual({
      major: 4,
      minor: 7,
      patch: 6,
    });
  });

  it("treats the empty result as 'unknown' rather than throwing", () => {
    // Defensive: if `ezs --version` ever prints to stderr instead of
    // stdout, the captured stdout could be empty. The activation gate
    // must degrade to skipping the warning, not to a thrown exception.
    expect(() => EzsCli.parseVersionSemver("")).not.toThrow();
    expect(EzsCli.parseVersionSemver("")).toBeNull();
  });
});
