-- CLI wrapper for the ezs binary.
--
-- All async functions use vim.system() (Neovim 0.10+); the synchronous
-- statusline helper uses vim.system():wait() with the list form so paths
-- with spaces are handled safely. We never build a shell string.
local M = {}

--- Cached stack data for statusline (avoids repeated CLI calls).
---@type { data: table[], timestamp: number }|nil
local _cache = nil

--- Cached resolved binary path. Cleared when config.cli_path changes.
---@type string|nil
local _resolved_binary = nil

--- Common install locations to probe when the user has not set cli_path.
local function common_paths()
  local home = vim.env.HOME or ""
  local paths = {
    home .. "/.local/bin/ezs",
    home .. "/go/bin/ezs",
    "/usr/local/bin/ezs",
    "/opt/homebrew/bin/ezs",
  }
  if vim.env.GOBIN and vim.env.GOBIN ~= "" then
    table.insert(paths, 1, vim.env.GOBIN .. "/ezs")
  end
  if vim.env.GOPATH and vim.env.GOPATH ~= "" then
    table.insert(paths, 1, vim.env.GOPATH .. "/bin/ezs")
  end
  return paths
end

--- Resolve the CLI binary path from user config, falling back to PATH lookup
--- and then to common install locations.
---@return string
local function cli_path()
  local configured = require("ezstack").config.cli_path or "ezs"
  if configured ~= "ezs" then
    -- User-specified explicit path; trust it.
    return configured
  end
  if _resolved_binary then
    return _resolved_binary
  end
  -- Try $PATH first.
  local found = vim.fn.exepath("ezs")
  if found and found ~= "" then
    _resolved_binary = found
    return found
  end
  -- Fall back to common install locations.
  for _, p in ipairs(common_paths()) do
    if vim.fn.executable(p) == 1 then
      _resolved_binary = p
      return p
    end
  end
  -- Last resort: return "ezs" so the eventual error message is clear.
  return "ezs"
end

--- Reset the resolved binary cache. Called from setup() if cli_path changes.
function M._reset_binary_cache()
  _resolved_binary = nil
end

--- Statusline cache TTL from user config.
---@return number milliseconds
local function cache_ttl()
  return require("ezstack").config.statusline_cache_ttl or 5000
end

--- Repository working directory used for CLI invocations. Falls back to cwd.
---@return string
local function cwd()
  -- Use the directory of the current buffer if it's a real file, otherwise
  -- the editor's cwd. This makes the plugin work correctly when the user
  -- is editing files inside a worktree.
  local bufname = vim.api.nvim_buf_get_name(0)
  if bufname ~= "" and vim.fn.filereadable(bufname) == 1 then
    return vim.fs.dirname(bufname)
  end
  return vim.fn.getcwd()
end

--- Run a command asynchronously via vim.system and call back with (err, stdout).
---@param args string[] CLI arguments
---@param callback fun(err: string|nil, stdout: string|nil)
local function run_async(args, callback)
  local cmd = { cli_path() }
  vim.list_extend(cmd, args)

  vim.system(cmd, { text = true, cwd = cwd() }, function(obj)
    vim.schedule(function()
      if obj.code ~= 0 then
        local err = vim.trim(obj.stderr or "")
        if err == "" then
          err = "ezs exited with code " .. tostring(obj.code)
        end
        callback(err, nil)
      else
        callback(nil, vim.trim(obj.stdout or ""))
      end
    end)
  end)
end

--- Run a command asynchronously, parse JSON output, call back with (err, data).
---@param args string[] CLI arguments (should include --json)
---@param callback fun(err: string|nil, data: any)
local function run_json(args, callback)
  run_async(args, function(err, stdout)
    if err then
      callback(err, nil)
      return
    end
    if not stdout or stdout == "" then
      callback(nil, {})
      return
    end
    local ok, parsed = pcall(vim.json.decode, stdout)
    if not ok then
      callback("Failed to parse JSON: " .. tostring(parsed), nil)
      return
    end
    callback(nil, parsed)
  end)
end

--- Fire the EzstackChanged user autocommand. Wraps mutating CLI calls so
--- the viewer (and any user hooks) refresh automatically.
local function fire_changed()
  M.invalidate_cache()
  vim.schedule(function()
    pcall(vim.api.nvim_exec_autocmds, "User", { pattern = "EzstackChanged" })
  end)
end

--- Wrap a callback so it fires EzstackChanged on success.
---@param cb fun(err: string|nil, ...): any
---@return fun(err: string|nil, ...): any
local function on_change(cb)
  return function(err, ...)
    if not err then
      fire_changed()
    end
    if cb then
      return cb(err, ...)
    end
  end
end

--- Check if the ezs CLI is available (async).
---@param callback fun(available: boolean)
function M.is_available(callback)
  run_async({ "--version" }, function(err)
    callback(err == nil)
  end)
end

--- Invalidate the cached stack data.
function M.invalidate_cache()
  _cache = nil
end

--- List stacks asynchronously.
---@param callback fun(err: string|nil, stacks: table[])
---@param opts? { force: boolean, all: boolean }
function M.list_stacks(callback, opts)
  opts = opts or {}
  -- Return cache if valid and not forced
  if not opts.force and _cache then
    local age = vim.uv.now() - _cache.timestamp
    if age < cache_ttl() then
      callback(nil, _cache.data)
      return
    end
  end

  local args = { "list", "--json" }
  if opts.all then
    table.insert(args, "--all")
  end

  run_json(args, function(err, stacks)
    if err then
      callback(err, {})
      return
    end
    stacks = stacks or {}
    _cache = { data = stacks, timestamp = vim.uv.now() }
    callback(nil, stacks)
  end)
end

--- List stacks synchronously (blocking). Used by statusline.
--- Uses the list form of vim.system so binary paths with spaces work.
---@return table[]
function M.list_stacks_sync()
  if _cache then
    local age = vim.uv.now() - _cache.timestamp
    if age < cache_ttl() then
      return _cache.data
    end
  end

  local result = vim.system(
    { cli_path(), "list", "--json" },
    { text = true, cwd = cwd(), timeout = 3000 }
  ):wait()

  if result.code ~= 0 or not result.stdout or result.stdout == "" then
    return {}
  end

  local ok, parsed = pcall(vim.json.decode, result.stdout)
  if not ok then
    return {}
  end

  _cache = { data = parsed, timestamp = vim.uv.now() }
  return parsed
end

--- Fetch stacks with extended status (PR, CI, review info).
---@param callback fun(err: string|nil, stacks: table[])
---@param opts? { all: boolean }
function M.status_stacks(callback, opts)
  opts = opts or {}
  local args = { "status", "--json" }
  if opts.all then
    table.insert(args, "--all")
  end
  run_json(args, function(err, stacks)
    if err then
      callback(err, {})
      return
    end
    callback(nil, stacks or {})
  end)
end

--- Execute an arbitrary ezs command asynchronously.
---@param args string[] CLI arguments
---@param callback fun(err: string|nil, stdout: string|nil)
function M.exec(args, callback)
  run_async(args, callback)
end

--- Execute an arbitrary ezs command with -y flag (auto-confirm).
---@param args string[] CLI arguments
---@param callback fun(err: string|nil, stdout: string|nil)
function M.exec_yes(args, callback)
  local full_args = { "-y" }
  vim.list_extend(full_args, args)
  run_async(full_args, callback)
end

--- Run an ezs command in an integrated terminal buffer.
--- Reuses an existing ezstack-terminal window if visible to avoid stacking
--- splits, otherwise opens a new bottom split. Uses jobstart's list form
--- so arguments are passed to execvp directly with no shell quoting issues.
---@param args string[] CLI arguments
function M.run_in_terminal(args)
  local cmd = { cli_path() }
  vim.list_extend(cmd, args)

  -- Reuse a visible ezstack terminal window if one exists.
  for _, win in ipairs(vim.api.nvim_list_wins()) do
    local buf = vim.api.nvim_win_get_buf(win)
    if vim.api.nvim_buf_is_valid(buf)
      and vim.b[buf].ezstack_terminal == 1
      and vim.bo[buf].buftype == "terminal"
    then
      -- Replace the existing terminal buffer.
      vim.api.nvim_set_current_win(win)
      break
    end
  end

  vim.cmd("botright new")
  local bufnr = vim.api.nvim_get_current_buf()
  vim.b[bufnr].ezstack_terminal = 1

  -- termopen accepts a list — no shell escaping needed.
  vim.fn.termopen(cmd, {
    cwd = cwd(),
    on_exit = function(_, code)
      vim.schedule(function()
        fire_changed()
        if code == 0 then
          -- Auto-close successful runs after a brief pause so the user
          -- can read the final output.
          vim.defer_fn(function()
            if vim.api.nvim_buf_is_valid(bufnr) then
              pcall(vim.api.nvim_buf_delete, bufnr, { force = true })
            end
          end, 1500)
        end
      end)
    end,
  })
  vim.cmd("startinsert")
end

-- ── Mutating commands (wrapped with on_change to refresh the UI) ──

--- Create a new branch.
---@param name string Branch name
---@param parent string|nil Parent branch (nil for default)
---@param callback fun(err: string|nil)
function M.new_branch(name, parent, callback)
  local args = { "-y", "new", name }
  if parent and parent ~= "" then
    table.insert(args, "-p")
    table.insert(args, parent)
  end
  run_async(args, on_change(callback))
end

--- Push current branch.
---@param opts? { force: boolean, branch: string|nil }
---@param callback fun(err: string|nil)
function M.push(opts, callback)
  opts = opts or {}
  local args = { "-y", "push" }
  if opts.force then
    table.insert(args, "--force")
  end
  run_async(args, on_change(callback))
end

--- Push entire stack.
---@param opts? { force: boolean }
---@param callback fun(err: string|nil)
function M.push_stack(opts, callback)
  opts = opts or {}
  local args = { "-y", "push", "-s" }
  if opts.force then
    table.insert(args, "--force")
  end
  run_async(args, on_change(callback))
end

--- Create a pull request.
---@param title string PR title
---@param opts { draft: boolean }
---@param callback fun(err: string|nil)
function M.pr_create(title, opts, callback)
  local args = { "-y", "pr", "create", "-t", title }
  if opts and opts.draft then
    table.insert(args, "-d")
  end
  if opts and opts.body and opts.body ~= "" then
    table.insert(args, "-b")
    table.insert(args, opts.body)
  end
  run_async(args, on_change(callback))
end

--- Update a pull request.
---@param branch string|nil Branch name
---@param callback fun(err: string|nil)
function M.pr_update(branch, callback)
  local args = { "-y", "pr", "update" }
  if branch and branch ~= "" then
    table.insert(args, "--branch")
    table.insert(args, branch)
  end
  run_async(args, on_change(callback))
end

--- Merge a pull request.
---@param method string "squash"|"merge"|"rebase"
---@param branch string|nil Branch name
---@param callback fun(err: string|nil)
function M.pr_merge(method, branch, callback)
  local args = { "-y", "pr", "merge", "-m", method or "squash" }
  if branch and branch ~= "" then
    table.insert(args, "--branch")
    table.insert(args, branch)
  end
  run_async(args, on_change(callback))
end

--- Toggle draft status on a PR.
---@param branch string|nil Branch name
---@param callback fun(err: string|nil)
function M.pr_draft(branch, callback)
  local args = { "-y", "pr", "draft" }
  if branch and branch ~= "" then
    table.insert(args, "--branch")
    table.insert(args, branch)
  end
  run_async(args, on_change(callback))
end

--- Update stack info in all PR descriptions.
---@param callback fun(err: string|nil)
function M.pr_stack(callback)
  run_async({ "-y", "pr", "stack" }, on_change(callback))
end

--- Delete a branch and its worktree.
---@param name string Branch name
---@param callback fun(err: string|nil)
function M.delete_branch(name, callback)
  run_async({ "-y", "delete", name }, on_change(callback))
end

--- Reparent a branch onto a new parent.
---@param branch string Branch to reparent
---@param new_parent string New parent branch
---@param callback fun(err: string|nil)
function M.reparent(branch, new_parent, callback)
  run_async({ "-y", "reparent", branch, new_parent }, on_change(callback))
end

--- Rename a stack.
---@param hash string Stack hash
---@param name string New name (empty string to clear)
---@param callback fun(err: string|nil)
function M.rename_stack(hash, name, callback)
  run_async({ "stack", "rename", hash, name }, on_change(callback))
end

--- Sync a single branch (headless, runs in background).
---@param branch string|nil Branch name (nil = current branch)
---@param opts? { merge: boolean, rebase: boolean }
---@param callback fun(err: string|nil)
function M.sync_branch(branch, opts, callback)
  opts = opts or {}
  local args = { "-y", "sync", "-c" }
  if branch and branch ~= "" then
    -- The CLI auto-detects the branch from cwd; --branch isn't a sync flag.
    -- Headless single-branch sync uses -c (current branch only).
  end
  if opts.merge then
    table.insert(args, "--merge")
  elseif opts.rebase then
    table.insert(args, "--rebase")
  end
  run_async(args, on_change(callback))
end

--- Continue an in-progress sync after the user resolved conflicts.
---@param callback fun(err: string|nil)
function M.sync_continue(callback)
  run_async({ "-y", "sync", "--continue" }, on_change(callback))
end

--- Dry-run sync — returns the JSON plan without making changes.
---@param opts? { all: boolean }
---@param callback fun(err: string|nil, plan: table|nil)
function M.sync_dry_run(opts, callback)
  opts = opts or {}
  local args = { "sync", "--dry-run", "--json" }
  if opts.all then
    table.insert(args, "--all")
  end
  run_json(args, callback)
end

--- Get ezs version.
---@param callback fun(err: string|nil, version: string|nil)
function M.version(callback)
  run_async({ "--version" }, callback)
end

--- Show ezs config.
---@param callback fun(err: string|nil, config: string|nil)
function M.config_show(callback)
  run_async({ "config", "show" }, callback)
end

--- Show commit log for a branch (vs its parent).
---@param branch string|nil Branch name (nil = current)
---@param callback fun(err: string|nil, log: string|nil)
function M.log(branch, callback)
  local args = { "log" }
  if branch and branch ~= "" then
    table.insert(args, "--branch")
    table.insert(args, branch)
  end
  run_async(args, callback)
end

return M
