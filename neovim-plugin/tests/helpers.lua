-- Test helpers shared across ezstack.nvim specs.
--
-- Every spec starts from a clean module state so config changes and
-- dispatch-table edits don't leak between tests.

local M = {}

--- Reload the plugin modules and return the main table. Call this in
--- `before_each` so each test sees a pristine `ezstack.config`.
---@return table
function M.reload()
  package.loaded["ezstack"] = nil
  package.loaded["ezstack.cli"] = nil
  package.loaded["ezstack.commands"] = nil
  package.loaded["ezstack.ui"] = nil
  package.loaded["ezstack.fugitive"] = nil
  package.loaded["ezstack.telescope"] = nil
  return require("ezstack")
end

--- Build a fake stack entry suitable for feeding into `ezstack.statusline`
--- or `ui._render_graph_lines`.
---@param opts table
---@return table
function M.fake_stack(opts)
  return vim.tbl_deep_extend("force", {
    name = "my-feat",
    hash = "a1b2c3d4e5f6",
    root = "main",
    branches = {},
  }, opts or {})
end

--- Stub `cli.list_stacks_sync` to return the given stacks for the duration
--- of one test. Returns a restore function.
---@param stacks table[]
---@return fun()
function M.stub_list_stacks_sync(stacks)
  local cli = require("ezstack.cli")
  local orig = cli.list_stacks_sync
  cli.list_stacks_sync = function() return stacks end
  return function() cli.list_stacks_sync = orig end
end

return M
