import { describe, it, expect, vi, beforeEach } from "vitest";

// Capture invoke calls. Mock @tauri-apps/api/core's `invoke` so we can
// assert what argv (specifically the `branch` field) the frontend
// shim passes to the Rust handler. The actual Tauri runtime never runs
// during tests; only the wire shape matters here.
const invokeCalls: { command: string; args: Record<string, unknown> }[] = [];

vi.mock("@tauri-apps/api/core", () => ({
  invoke: vi.fn(
    (command: string, args: Record<string, unknown>): Promise<unknown> => {
      invokeCalls.push({ command, args });
      return Promise.resolve({ success: true, output: "" });
    },
  ),
}));

import * as ezs from "./ezs";

beforeEach(() => {
  invokeCalls.length = 0;
});

describe("ezs.pushBranch — branch opt passthrough (regression)", () => {
  // Without the `branch` opt, the Rust handler runs `ezs -y push` and the
  // CLI pushes whatever git HEAD points at — NOT the branch the right-pane
  // button or right-click context menu was operating on. Same shape as the
  // nvim viewer `p` keymap bug fixed in ezstack.nvim#4.

  it("forwards branch through to invoke when callers want a specific branch (the bug regression)", async () => {
    await ezs.pushBranch("/repo", false, false, { branch: "feat/auth" });
    expect(invokeCalls).toHaveLength(1);
    const args = invokeCalls[0].args;
    expect(args.command).toBeUndefined();
    expect(args.repoPath).toBe("/repo");
    expect(args.stack).toBe(false);
    expect(args.force).toBe(false);
    expect(args.branch).toBe("feat/auth");
  });

  it("sends branch=null when no branch is provided (legacy current-HEAD push)", async () => {
    await ezs.pushBranch("/repo");
    expect(invokeCalls).toHaveLength(1);
    expect(invokeCalls[0].args.branch).toBeNull();
  });

  it("sends branch=null when opts is provided without branch field", async () => {
    await ezs.pushBranch("/repo", false, false, { verify: true });
    expect(invokeCalls).toHaveLength(1);
    expect(invokeCalls[0].args.branch).toBeNull();
    expect(invokeCalls[0].args.verify).toBe(true);
  });

  it("preserves stack/force/verify/allRemotes alongside branch", async () => {
    await ezs.pushBranch("/repo", true, true, {
      verify: true,
      allRemotes: true,
      branch: "feat/auth",
    });
    expect(invokeCalls).toHaveLength(1);
    const args = invokeCalls[0].args;
    expect(args.stack).toBe(true);
    expect(args.force).toBe(true);
    expect(args.verify).toBe(true);
    expect(args.allRemotes).toBe(true);
    expect(args.branch).toBe("feat/auth");
  });

  it("invokes the push_branch Tauri command (not push or pushStack)", async () => {
    await ezs.pushBranch("/repo", false, false, { branch: "feat/auth" });
    expect(invokeCalls[0].command).toBe("push_branch");
  });
});
