local M = {}

--- Resolve the ezs binary path.
--- Checks the configured path, then common install locations.
---@return string
local function resolve_binary()
  local config = require("ezstack").config
  local configured = config.cli_path

  -- If user set an explicit path, use it
  if configured ~= "ezs" then
    return configured
  end

  -- Check if ezs is on PATH
  if vim.fn.executable("ezs") == 1 then
    return "ezs"
  end

  -- Check common install locations
  local home = vim.env.HOME or ""
  local candidates = {
    home .. "/.local/bin/ezs",
    home .. "/go/bin/ezs",
    "/usr/local/bin/ezs",
  }
  if vim.env.GOBIN then
    table.insert(candidates, 1, vim.env.GOBIN .. "/ezs")
  end
  if vim.env.GOPATH then
    table.insert(candidates, 2, vim.env.GOPATH .. "/bin/ezs")
  end

  for _, p in ipairs(candidates) do
    if vim.fn.filereadable(p) == 1 then
      return p
    end
  end

  return "ezs" -- fallback
end

local _resolved_path = nil

--- Get the resolved CLI path (cached after first call).
---@return string
local function cli_path()
  if not _resolved_path then
    _resolved_path = resolve_binary()
  end
  return _resolved_path
end

--- Get the working directory for CLI commands.
---@return string
local function get_cwd()
  return vim.fn.getcwd()
end

--- Strip ANSI escape codes from a string.
---@param s string
---@return string
local function strip_ansi(s)
  return s:gsub("\027%[[0-9;]*m", "")
end

--- Run an ezs command asynchronously and invoke callback with result.
---@param args string[] CLI arguments
---@param callback fun(err: string|nil, stdout: string) Called with (nil, stdout) on success or (error_msg, "") on failure
function M.exec(args, callback)
  local cmd = { cli_path() }
  for _, a in ipairs(args) do
    table.insert(cmd, a)
  end

  vim.system(cmd, {
    cwd = get_cwd(),
    timeout = 30000,
  }, function(result)
    vim.schedule(function()
      if result.code ~= 0 then
        local err = strip_ansi(result.stderr or ""):gsub("^%s+", ""):gsub("%s+$", "")
        if err == "" then
          err = "ezs exited with code " .. result.code
        end
        callback(err, "")
      else
        callback(nil, result.stdout or "")
      end
    end)
  end)
end

--- Run an ezs command synchronously and return stdout.
--- Use sparingly — blocks the UI.
---@param args string[] CLI arguments
---@return string|nil stdout
---@return string|nil error
function M.exec_sync(args)
  local cmd = { cli_path() }
  for _, a in ipairs(args) do
    table.insert(cmd, a)
  end

  local result = vim.system(cmd, {
    cwd = get_cwd(),
    timeout = 30000,
  }):wait()

  if result.code ~= 0 then
    local err = strip_ansi(result.stderr or ""):gsub("^%s+", ""):gsub("%s+$", "")
    if err == "" then
      err = "ezs exited with code " .. result.code
    end
    return nil, err
  end
  return result.stdout or "", nil
end

--- Run an ezs command with -y flag (skip confirmations).
---@param args string[] CLI arguments (without -y)
---@param callback fun(err: string|nil, stdout: string)
function M.exec_yes(args, callback)
  local full = { "-y" }
  for _, a in ipairs(args) do
    table.insert(full, a)
  end
  M.exec(full, callback)
end

--- Run an ezs command in a terminal buffer (for interactive use).
---@param args string[] CLI arguments
---@return number bufnr Terminal buffer number
function M.run_in_terminal(args)
  local cmd = { cli_path() }
  for _, a in ipairs(args) do
    table.insert(cmd, a)
  end
  vim.cmd("botright split")
  vim.fn.termopen(cmd, { cwd = get_cwd() })
  local bufnr = vim.api.nvim_get_current_buf()

  -- Auto-refresh on terminal close
  vim.api.nvim_create_autocmd("TermClose", {
    buffer = bufnr,
    once = true,
    callback = function()
      vim.defer_fn(function()
        M.invalidate_cache()
        vim.api.nvim_exec_autocmds("User", { pattern = "EzstackChanged" })
      end, 500)
    end,
  })

  vim.cmd("startinsert")
  return bufnr
end

-- ── JSON query helpers ──

---@type table|nil Cached stack list
local _stacks_cache = nil
---@type number|nil Timestamp of last cache
local _stacks_cache_time = nil

--- Invalidate the stack list cache.
function M.invalidate_cache()
  _stacks_cache = nil
  _stacks_cache_time = nil
end

--- Get the cache TTL in milliseconds.
---@return number
local function cache_ttl()
  return require("ezstack").config.statusline_cache_ttl
end

--- List all stacks (async, with caching).
---@param callback fun(err: string|nil, stacks: table[])
---@param opts? { force?: boolean, all?: boolean }
function M.list_stacks(callback, opts)
  opts = opts or {}
  local now = vim.uv.now()

  if not opts.force and _stacks_cache and _stacks_cache_time and (now - _stacks_cache_time) < cache_ttl() then
    callback(nil, _stacks_cache)
    return
  end

  local args = { "list", "--json" }
  if opts.all ~= false then
    table.insert(args, "--all")
  end

  M.exec(args, function(err, stdout)
    if err then
      callback(err, {})
      return
    end
    local ok, data = pcall(vim.json.decode, stdout)
    if not ok or type(data) ~= "table" then
      callback("failed to parse JSON", {})
      return
    end
    _stacks_cache = data
    _stacks_cache_time = vim.uv.now()
    callback(nil, data)
  end)
end

--- List all stacks synchronously (for statusline).
---@param opts? { force?: boolean }
---@return table[] stacks
function M.list_stacks_sync(opts)
  opts = opts or {}
  local now = vim.uv.now()

  if not opts.force and _stacks_cache and _stacks_cache_time and (now - _stacks_cache_time) < cache_ttl() then
    return _stacks_cache
  end

  local stdout, err = M.exec_sync({ "list", "--json", "--all" })
  if err or not stdout then
    return {}
  end
  local ok, data = pcall(vim.json.decode, stdout)
  if not ok or type(data) ~= "table" then
    return {}
  end
  _stacks_cache = data
  _stacks_cache_time = vim.uv.now()
  return data
end

--- Get status for all stacks (async, includes PR/CI info).
---@param callback fun(err: string|nil, stacks: table[])
function M.status_stacks(callback)
  M.exec({ "status", "--json", "--all" }, function(err, stdout)
    if err then
      callback(err, {})
      return
    end
    local ok, data = pcall(vim.json.decode, stdout)
    if not ok or type(data) ~= "table" then
      callback("failed to parse JSON", {})
      return
    end
    callback(nil, data)
  end)
end

--- Check if ezs is available.
---@param callback fun(available: boolean)
function M.is_available(callback)
  M.exec({ "--version" }, function(err)
    callback(err == nil)
  end)
end

-- ── Mutation helpers ──

--- Create a new branch.
---@param name string
---@param parent? string
---@param callback fun(err: string|nil)
function M.new_branch(name, parent, callback)
  local args = { "new", name }
  if parent then
    table.insert(args, "-p")
    table.insert(args, parent)
  end
  M.exec_yes(args, function(err)
    if not err then
      M.invalidate_cache()
    end
    callback(err)
  end)
end

--- Delete a branch.
---@param name string
---@param callback fun(err: string|nil)
function M.delete_branch(name, callback)
  M.exec_yes({ "delete", name }, function(err)
    if not err then
      M.invalidate_cache()
    end
    callback(err)
  end)
end

--- Reparent a branch.
---@param branch string
---@param new_parent string
---@param callback fun(err: string|nil)
function M.reparent(branch, new_parent, callback)
  M.exec_yes({ "reparent", branch, new_parent }, function(err)
    if not err then
      M.invalidate_cache()
    end
    callback(err)
  end)
end

--- Rename a stack.
---@param hash string
---@param name string
---@param callback fun(err: string|nil)
function M.rename_stack(hash, name, callback)
  M.exec({ "stack", "rename", hash, name }, function(err)
    if not err then
      M.invalidate_cache()
    end
    callback(err)
  end)
end

--- Push current branch.
---@param callback fun(err: string|nil)
function M.push(callback)
  M.exec_yes({ "push" }, function(err)
    callback(err)
  end)
end

--- Push entire stack.
---@param callback fun(err: string|nil)
function M.push_stack(callback)
  M.exec_yes({ "push", "-s" }, function(err)
    callback(err)
  end)
end

--- Create a PR.
---@param title string
---@param opts? { draft?: boolean, branch?: string }
---@param callback fun(err: string|nil)
function M.pr_create(title, opts, callback)
  opts = opts or {}
  local args = { "pr", "create", "-t", title }
  if opts.draft then
    table.insert(args, "-d")
  end
  if opts.branch then
    table.insert(args, "--branch")
    table.insert(args, opts.branch)
  end
  M.exec_yes(args, function(err)
    if not err then
      M.invalidate_cache()
    end
    callback(err)
  end)
end

--- Update a PR.
---@param branch? string
---@param callback fun(err: string|nil)
function M.pr_update(branch, callback)
  local args = { "pr", "update" }
  if branch then
    table.insert(args, "--branch")
    table.insert(args, branch)
  end
  M.exec_yes(args, function(err)
    callback(err)
  end)
end

--- Merge a PR.
---@param method string "squash"|"merge"|"rebase"
---@param branch? string
---@param callback fun(err: string|nil)
function M.pr_merge(method, branch, callback)
  local args = { "pr", "merge", "-m", method }
  if branch then
    table.insert(args, "--branch")
    table.insert(args, branch)
  end
  M.exec_yes(args, function(err)
    if not err then
      M.invalidate_cache()
    end
    callback(err)
  end)
end

--- Toggle PR draft status.
---@param branch? string
---@param callback fun(err: string|nil)
function M.pr_draft(branch, callback)
  local args = { "pr", "draft" }
  if branch then
    table.insert(args, "--branch")
    table.insert(args, branch)
  end
  M.exec_yes(args, function(err)
    if not err then
      M.invalidate_cache()
    end
    callback(err)
  end)
end

--- Update stack info in PRs.
---@param callback fun(err: string|nil)
function M.pr_stack(callback)
  M.exec_yes({ "pr", "stack" }, function(err)
    callback(err)
  end)
end

return M
