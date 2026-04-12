--- CLI wrapper for the ezs binary.
--- All async functions use vim.fn.jobstart; sync helpers use vim.fn.system.
local M = {}

--- Cached stack data for statusline (avoids repeated CLI calls).
---@type { data: table[], timestamp: number }|nil
local _cache = nil

--- Resolve the CLI binary path from user config.
---@return string
local function cli_path()
  return require("ezstack").config.cli_path or "ezs"
end

--- Statusline cache TTL from user config.
---@return number milliseconds
local function cache_ttl()
  return require("ezstack").config.statusline_cache_ttl or 5000
end

--- Run a command asynchronously and call back with (err, stdout).
---@param args string[] CLI arguments
---@param callback fun(err: string|nil, stdout: string|nil)
local function run_async(args, callback)
  local cmd = { cli_path() }
  vim.list_extend(cmd, args)

  local stdout_chunks = {}
  local stderr_chunks = {}

  vim.fn.jobstart(cmd, {
    stdout_buffered = true,
    stderr_buffered = true,
    on_stdout = function(_, data)
      if data then
        vim.list_extend(stdout_chunks, data)
      end
    end,
    on_stderr = function(_, data)
      if data then
        vim.list_extend(stderr_chunks, data)
      end
    end,
    on_exit = function(_, code)
      vim.schedule(function()
        if code ~= 0 then
          local err = vim.trim(table.concat(stderr_chunks, "\n"))
          if err == "" then
            err = "ezs exited with code " .. code
          end
          callback(err, nil)
        else
          local out = vim.trim(table.concat(stdout_chunks, "\n"))
          callback(nil, out)
        end
      end)
    end,
  })
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
    local ok, parsed = pcall(vim.json.decode, stdout or "")
    if not ok then
      callback("Failed to parse JSON: " .. tostring(parsed), nil)
      return
    end
    callback(nil, parsed)
  end)
end

--- Check if the ezs CLI is available (async).
---@param callback fun(available: boolean)
function M.is_available(callback)
  run_async({ "--version" }, function(err)
    vim.schedule(function()
      callback(err == nil)
    end)
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
---@return table[]
function M.list_stacks_sync()
  if _cache then
    local age = vim.uv.now() - _cache.timestamp
    if age < cache_ttl() then
      return _cache.data
    end
  end

  local cmd = cli_path() .. " list --json"
  local output = vim.fn.system(cmd)
  if vim.v.shell_error ~= 0 then
    return {}
  end

  local ok, parsed = pcall(vim.json.decode, output)
  if not ok then
    return {}
  end

  _cache = { data = parsed, timestamp = vim.uv.now() }
  return parsed
end

--- Fetch stacks with extended status (PR, CI, review info).
---@param callback fun(err: string|nil, stacks: table[])
function M.status_stacks(callback)
  run_json({ "status", "--json" }, function(err, stacks)
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
---@param args string[] CLI arguments
function M.run_in_terminal(args)
  local cmd = { cli_path() }
  vim.list_extend(cmd, args)
  local cmd_str = table.concat(vim.tbl_map(vim.fn.shellescape, cmd), " ")
  vim.cmd("botright split | terminal " .. cmd_str)
  vim.cmd("startinsert")
end

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
  run_async(args, function(err)
    callback(err)
  end)
end

--- Push current branch.
---@param callback fun(err: string|nil)
function M.push(callback)
  run_async({ "-y", "push" }, function(err)
    callback(err)
  end)
end

--- Push entire stack.
---@param callback fun(err: string|nil)
function M.push_stack(callback)
  run_async({ "-y", "push", "-s" }, function(err)
    callback(err)
  end)
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
  run_async(args, function(err)
    callback(err)
  end)
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
  run_async(args, function(err)
    callback(err)
  end)
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
  run_async(args, function(err)
    callback(err)
  end)
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
  run_async(args, function(err)
    callback(err)
  end)
end

--- Update stack info in all PR descriptions.
---@param callback fun(err: string|nil)
function M.pr_stack(callback)
  run_async({ "-y", "pr", "stack" }, function(err)
    callback(err)
  end)
end

--- Delete a branch and its worktree.
---@param name string Branch name
---@param callback fun(err: string|nil)
function M.delete_branch(name, callback)
  run_async({ "-y", "delete", name }, function(err)
    callback(err)
  end)
end

--- Reparent a branch onto a new parent.
---@param branch string Branch to reparent
---@param new_parent string New parent branch
---@param callback fun(err: string|nil)
function M.reparent(branch, new_parent, callback)
  run_async({ "-y", "reparent", branch, new_parent }, function(err)
    callback(err)
  end)
end

--- Rename a stack.
---@param hash string Stack hash
---@param name string New name (empty string to clear)
---@param callback fun(err: string|nil)
function M.rename_stack(hash, name, callback)
  run_async({ "stack", "rename", hash, name }, function(err)
    callback(err)
  end)
end

return M
