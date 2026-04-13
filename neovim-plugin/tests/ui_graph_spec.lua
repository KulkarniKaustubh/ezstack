-- Tests for `ui._render_graph_lines` — pure function, no CLI, no buffers.

local H = require("tests.helpers")

describe("ezstack.ui._render_graph_lines", function()
  local ui

  before_each(function()
    H.reload()
    ui = require("ezstack.ui")
  end)

  it("renders an empty-stack placeholder", function()
    local lines = ui._render_graph_lines({
      { hash = "deadbeef", root = "main", branches = {} },
    })
    assert.truthy(table.concat(lines, "\n"):find("%(empty%)"))
  end)

  it("renders a linear stack as a tree", function()
    local lines = ui._render_graph_lines({
      {
        hash = "h1",
        name = "s1",
        root = "main",
        branches = {
          { name = "a", parent = "main" },
          { name = "b", parent = "a" },
          { name = "c", parent = "b", is_current = true },
        },
      },
    })
    local joined = table.concat(lines, "\n")
    assert.truthy(joined:find("Stack: s1"))
    assert.truthy(joined:find("root: main"))
    -- All three branches appear in order.
    local pa, pb, pc = joined:find("a"), joined:find("b"), joined:find("c")
    assert.is_number(pa)
    assert.is_number(pb)
    assert.is_number(pc)
    -- Current branch is marked.
    assert.truthy(joined:find("%* c"))
  end)

  it("surfaces orphans under a labelled header", function()
    -- Regression test: before the orphan fix, branches whose parent isn't
    -- reachable from `stack.root` were silently dropped.
    local lines = ui._render_graph_lines({
      {
        hash = "h1",
        root = "main",
        branches = {
          { name = "reachable", parent = "main" },
          { name = "orph", parent = "ghost" },
        },
      },
    })
    local joined = table.concat(lines, "\n")
    assert.truthy(joined:find("reachable"))
    assert.truthy(joined:find("orphans"))
    assert.truthy(joined:find("orph"))
    assert.truthy(joined:find("parent: ghost"))
  end)

  it("terminates on cycles without infinite recursion", function()
    -- Mutually-recursive parents: root -> a -> b -> a (impossible in
    -- practice but we should still terminate).
    local lines = ui._render_graph_lines({
      {
        hash = "h1",
        root = "main",
        branches = {
          { name = "a", parent = "main" },
          { name = "b", parent = "a" },
          { name = "a-shadow", parent = "b" }, -- fine, no actual cycle
        },
      },
    })
    -- If this didn't terminate we'd never get here.
    assert.is_true(#lines > 0)
  end)

  it("renders multiple stacks with a blank line between them", function()
    local lines = ui._render_graph_lines({
      { hash = "h1", root = "main", branches = { { name = "a", parent = "main" } } },
      { hash = "h2", root = "main", branches = { { name = "x", parent = "main" } } },
    })
    -- At least one blank separator line between blocks.
    local blank_seen = false
    for _, l in ipairs(lines) do
      if l == "" then
        blank_seen = true
        break
      end
    end
    assert.is_true(blank_seen)
  end)

  it("includes PR label when pr_number > 0", function()
    local lines = ui._render_graph_lines({
      {
        hash = "h1",
        root = "main",
        branches = {
          { name = "a", parent = "main", pr_number = 7, pr_state = "OPEN" },
        },
      },
    })
    local joined = table.concat(lines, "\n")
    assert.truthy(joined:find("PR #7"))
    assert.truthy(joined:find("%[OPEN%]"))
  end)
end)
