import { describe, it, expect } from "vitest";
import { filterRepos, repoDisplayName } from "./repo-filter";
import type { RepoConfig } from "../types/ezstack";

const repos: RepoConfig[] = [
  { repo_path: "/Users/me/code/alpha", worktree_base_dir: "" },
  { repo_path: "/Users/me/code/beta", worktree_base_dir: "" },
  { repo_path: "/srv/work/gamma", worktree_base_dir: "" },
];

describe("repoDisplayName", () => {
  it("returns the trailing path segment", () => {
    expect(repoDisplayName("/Users/me/code/alpha")).toBe("alpha");
  });

  it("returns the full string when there is no slash", () => {
    expect(repoDisplayName("bare")).toBe("bare");
  });
});

describe("filterRepos", () => {
  it("returns all repos when the query is empty", () => {
    expect(filterRepos(repos, "", null)).toEqual(repos);
    expect(filterRepos(repos, "   ", null)).toEqual(repos);
  });

  it("matches on display name case-insensitively", () => {
    const result = filterRepos(repos, "ALPHA", null);
    expect(result.map((r) => r.repo_path)).toEqual(["/Users/me/code/alpha"]);
  });

  it("matches on full path", () => {
    const result = filterRepos(repos, "srv", null);
    expect(result.map((r) => r.repo_path)).toEqual(["/srv/work/gamma"]);
  });

  it("always includes the selected repo even when it doesn't match", () => {
    const result = filterRepos(repos, "alpha", "/srv/work/gamma");
    const paths = result.map((r) => r.repo_path);
    expect(paths).toContain("/Users/me/code/alpha");
    expect(paths).toContain("/srv/work/gamma");
    expect(paths).not.toContain("/Users/me/code/beta");
  });

  it("does not duplicate the selected repo when it also matches the query", () => {
    const result = filterRepos(repos, "alpha", "/Users/me/code/alpha");
    expect(result.map((r) => r.repo_path)).toEqual(["/Users/me/code/alpha"]);
  });

  it("returns empty when nothing matches and there is no selection", () => {
    expect(filterRepos(repos, "nothing-matches", null)).toEqual([]);
  });
});
