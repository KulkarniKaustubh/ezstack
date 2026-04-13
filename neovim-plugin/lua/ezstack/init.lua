local M = {}

---@class EzstackConfig
---@field cli_path string Path to ezs binary
---@field auto_refresh boolean Auto-refresh on FugitiveChanged/TermClose
---@field viewer_position string Split command prefix for viewer
---@field viewer_height number Viewer window height in lines
---@field statusline_cache_ttl number Statusline cache TTL in milliseconds
---@field goto_strategy string "tcd"|"cd"|"lcd"
---@field goto_close_buffers boolean Close unmodified buffers from old worktree on goto
---@field goto_open_explorer boolean Open file explorer at new worktree root on goto
---@field default_keymaps boolean Install default `]s`/`[s` stack-navigation keymaps
---@field statusline_format string "stack"|"pr"|"full" — fields included in `M.statusline()`
---@field welcome boolean Show a one-time welcome notification on first setup

---@type EzstackConfig
local defaults = {
  cli_path = "ezs",
  auto_refresh = true,
  viewer_position = "botright",
  viewer_height = 15,
  statusline_cache_ttl = 5000,
  goto_strategy = "tcd",
  goto_close_buffers = false,
  goto_open_explorer = true,
  default_keymaps = false,
  statusline_format = "stack",
  welcome = true,
}

---@type EzstackConfig
M.config = vim.deepcopy(defaults)

---@type boolean
local _setup_done_flag = false

--- Returns true after setup() has been called at least once.
---@return boolean
function M._setup_done()
  return _setup_done_flag
end

--- Setup ezstack.nvim with user options.
---@param opts? table Partial config overrides
function M.setup(opts)
  M.config = vim.tbl_deep_extend("force", defaults, opts or {})
  _setup_done_flag = true

  -- Reset cached binary path in case cli_path changed.
  pcall(function()
    require("ezstack.cli")._reset_binary_cache()
  end)

  -- Register highlight groups
  M._setup_highlights()

  -- The :Ezs user command is registered by plugin/ezstack.lua so it is
  -- available before setup() is called. We still call register() here as
  -- an idempotent fallback for users who load the plugin via dofile() or
  -- without the plugin/ directory on rtp.
  require("ezstack.commands").register()

  vim.api.nvim_create_user_command("EzsActions", function()
    M.actions_menu()
  end, { desc = "ezstack: quick action menu" })

  -- Setup fugitive / EzstackChanged auto-refresh integration
  if M.config.auto_refresh then
    require("ezstack.fugitive").setup()
  end

  if M.config.default_keymaps then
    M._install_default_keymaps()
  end

  if M.config.welcome then
    M._first_run_welcome()
  end

  vim.api.nvim_exec_autocmds("User", { pattern = "EzstackSetup", modeline = false })

  -- Check if ezs is available (async, non-blocking)
  require("ezstack.cli").is_available(function(available)
    if not available then
      vim.notify(
        'ezstack: "ezs" CLI not found. Install it or set cli_path in setup().',
        vim.log.levels.WARN
      )
    end
  end)
end

--- Path to the per-user marker file that records the first-run welcome.
--- Lives under `stdpath("state")` so we never touch `~/.ezstack` (that
--- directory belongs to the `ezs` CLI and must not be modified by the
--- plugin). Returns nil if the state dir is not resolvable or writable.
---@return string|nil
function M._welcome_marker_path()
  local ok, state = pcall(vim.fn.stdpath, "state")
  if not ok or type(state) ~= "string" or state == "" then
    return nil
  end
  return state .. "/ezstack/welcomed"
end

--- Show a one-time welcome notification the first time `setup()` is called
--- on this machine. Idempotent: relies on a marker file under `stdpath("state")`.
--- Never writes to `~/.ezstack`. Failure to create the marker silently skips
--- the notification so read-only environments don't spam on every launch.
function M._first_run_welcome()
  local marker = M._welcome_marker_path()
  if not marker then
    return
  end
  if vim.fn.filereadable(marker) == 1 then
    return
  end
  local dir = vim.fs.dirname(marker)
  if vim.fn.isdirectory(dir) == 0 then
    local mk_ok = pcall(vim.fn.mkdir, dir, "p")
    if not mk_ok then
      return
    end
  end
  local write_ok = pcall(function()
    local f = assert(io.open(marker, "w"))
    f:write("welcomed\n")
    f:close()
  end)
  if not write_ok then
    return
  end
  vim.schedule(function()
    vim.notify(
      "Welcome to ezstack.nvim!\n"
        .. "  :Ezs            open the stack viewer\n"
        .. "  :Ezs graph      ASCII tree of all stacks\n"
        .. "  :Ezs new        create a stacked branch\n"
        .. "  :EzsActions     quick action menu\n"
        .. "See :help ezstack for the full command reference.",
      vim.log.levels.INFO,
      { title = "ezstack.nvim" }
    )
  end)
end

--- Install the opt-in default keymaps. Uses `]s`/`[s` (Vim's "next/prev
--- spell error" motions, which are inert when `spell` is off) instead of
--- `gn`/`gp` — those are built-in motions we must not shadow.
---
--- Mappings are only installed when no user mapping already exists for the
--- key, so users who bind `]s` to spellcheck or another plugin are not
--- silently clobbered.
function M._install_default_keymaps()
  local function maybe_map(lhs, rhs, desc)
    if vim.fn.maparg(lhs, "n") ~= "" then
      return -- user already mapped it; do not override
    end
    vim.keymap.set("n", lhs, rhs, { desc = desc, silent = true })
  end
  maybe_map("]s", "<cmd>Ezs down<cr>", "ezstack: down the stack (toward children)")
  maybe_map("[s", "<cmd>Ezs up<cr>", "ezstack: up the stack (toward parent)")
end

--- Define highlight groups with sensible defaults.
function M._setup_highlights()
  local hl = vim.api.nvim_set_hl
  hl(0, "EzstackStack", { link = "Title", default = true })
  hl(0, "EzstackStackHash", { link = "Comment", default = true })
  hl(0, "EzstackSeparator", { link = "Comment", default = true })
  hl(0, "EzstackBranch", { link = "Normal", default = true })
  hl(0, "EzstackBranchCurrent", { link = "String", default = true })
  hl(0, "EzstackBranchMerged", { link = "Comment", default = true })
  hl(0, "EzstackPointer", { link = "String", default = true })
  hl(0, "EzstackConnector", { link = "Comment", default = true })
  hl(0, "EzstackPR", { link = "Identifier", default = true })
  hl(0, "EzstackPRDraft", { link = "Comment", default = true })
  hl(0, "EzstackPRMerged", { link = "DiagnosticOk", default = true })
  hl(0, "EzstackPRClosed", { link = "DiagnosticError", default = true })
  hl(0, "EzstackCIPass", { link = "DiagnosticOk", default = true })
  hl(0, "EzstackCIFail", { link = "DiagnosticError", default = true })
  hl(0, "EzstackCIPending", { link = "DiagnosticWarn", default = true })
  hl(0, "EzstackParent", { link = "Comment", default = true })
  hl(0, "EzstackRoot", { link = "Comment", default = true })
  hl(0, "EzstackNoPR", { link = "Comment", default = true })
  hl(0, "EzstackAdditions", { link = "DiagnosticOk", default = true })
  hl(0, "EzstackDeletions", { link = "DiagnosticError", default = true })
end

--- Format a single branch+stack pair for the statusline.
--- Exposed for unit tests; use `M.statusline()` from user code.
---@param branch table Branch JSON (name, pr_number, pr_state)
---@param stack table Stack JSON (name, hash)
---@param format "stack"|"pr"|"full"
---@return string
function M._format_statusline(branch, stack, format)
  local function stack_label()
    if stack.name and stack.name ~= "" then
      local hash = stack.hash or ""
      if hash == "" then
        return stack.name
      end
      return stack.name .. " [" .. hash:sub(1, 7) .. "]"
    end
    return "Stack " .. ((stack.hash or ""):sub(1, 7))
  end

  local function pr_label()
    if not branch.pr_number or branch.pr_number <= 0 then
      return ""
    end
    local state = branch.pr_state and branch.pr_state ~= ""
      and (" " .. branch.pr_state) or ""
    return string.format("PR#%d%s", branch.pr_number, state)
  end

  local pieces = { branch.name }
  if format == "stack" then
    table.insert(pieces, stack_label())
  elseif format == "pr" then
    local pr = pr_label()
    if pr ~= "" then
      table.insert(pieces, pr)
    end
  else -- "full"
    table.insert(pieces, stack_label())
    local pr = pr_label()
    if pr ~= "" then
      table.insert(pieces, pr)
    end
  end
  return " " .. table.concat(pieces, " | ")
end

--- Statusline component.
--- Default format ("stack") returns " branch | stack [hash]" — matches the
--- pre-`feat/nvim-bundle` behavior for backwards compatibility. Opt-in
--- "pr" returns " branch | PR#N STATE" and "full" combines both.
--- Returns "" when the current buffer is not in any stack. Cached via
--- `cli.list_stacks_sync` to avoid hammering the CLI.
---@return string
function M.statusline()
  local cli = require("ezstack.cli")
  local stacks = cli.list_stacks_sync()

  if not stacks or #stacks == 0 then
    return ""
  end

  local format = M.config.statusline_format or "stack"
  for _, stack in ipairs(stacks) do
    for _, branch in ipairs(stack.branches or {}) do
      if branch.is_current then
        return M._format_statusline(branch, stack, format)
      end
    end
  end

  return ""
end

--- Action handlers for `:EzsActions`. Each entry is `{label, fn}` where
--- `fn` takes no arguments and runs the action. Exposed as `M._actions` so
--- tests can dispatch by label without going through `vim.ui.select`.
---@return table<string, fun()>
function M._actions()
  local cli = require("ezstack.cli")

  local function notify_cb(label, success)
    return function(err)
      if err then
        vim.notify(label .. " failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify(success, vim.log.levels.INFO)
      end
    end
  end

  return {
    ["sync (current branch)"]      = function() cli.run_in_terminal({ "sync", "-c" }) end,
    ["sync (whole stack)"]         = function() cli.run_in_terminal({ "sync", "-s" }) end,
    ["sync --continue"]            = function()
      vim.notify("Continuing sync...", vim.log.levels.INFO)
      cli.sync_continue(notify_cb("Sync continue", "Sync completed"))
    end,
    ["push branch"]                = function()
      vim.notify("Pushing branch...", vim.log.levels.INFO)
      cli.push({}, notify_cb("Push", "Branch pushed"))
    end,
    ["push stack"]                 = function()
      vim.notify("Pushing stack...", vim.log.levels.INFO)
      cli.push_stack({}, notify_cb("Push stack", "Stack pushed"))
    end,
    ["pr create"]                  = function() vim.cmd("Ezs pr create") end,
    ["pr update"]                  = function()
      vim.notify("Updating PR...", vim.log.levels.INFO)
      cli.pr_update(nil, notify_cb("PR update", "PR updated"))
    end,
    ["pr draft (toggle)"]          = function()
      vim.notify("Toggling draft...", vim.log.levels.INFO)
      cli.pr_draft(nil, notify_cb("PR draft", "Draft status toggled"))
    end,
    ["pr merge"]                   = function() vim.cmd("Ezs pr merge") end,
    ["pr open in browser"]         = function() vim.cmd("Ezs pr open") end,
    ["pr stack (update all PRs)"]  = function()
      vim.notify("Updating stack info in PRs...", vim.log.levels.INFO)
      cli.pr_stack(notify_cb("PR stack", "Stack info updated in PRs"))
    end,
    ["new branch"]                 = function() vim.cmd("Ezs new") end,
    ["delete branch"]              = function() vim.cmd("Ezs delete") end,
    ["goto branch"]                = function() vim.cmd("Ezs goto") end,
    ["graph"]                      = function() vim.cmd("Ezs graph") end,
  }
end

--- Ordered labels for `:EzsActions`. Exposed for tests.
---@return string[]
function M._action_labels()
  return {
    "sync (current branch)",
    "sync (whole stack)",
    "sync --continue",
    "push branch",
    "push stack",
    "pr create",
    "pr update",
    "pr draft (toggle)",
    "pr merge",
    "pr open in browser",
    "pr stack (update all PRs)",
    "new branch",
    "delete branch",
    "goto branch",
    "graph",
  }
end

--- `:EzsActions` — quick action menu via `vim.ui.select`.
function M.actions_menu()
  local actions = M._actions()
  local labels = M._action_labels()
  vim.ui.select(labels, { prompt = "ezstack action:" }, function(choice)
    if not choice then
      return
    end
    local fn = actions[choice]
    if fn then
      fn()
    else
      vim.notify("Unknown ezstack action: " .. choice, vim.log.levels.ERROR)
    end
  end)
end

--- Navigate to a worktree path using the configured goto strategy.
---@param worktree_path string
function M.goto_worktree(worktree_path)
  if not worktree_path or worktree_path == "" then
    vim.notify("No worktree path available", vim.log.levels.WARN)
    return
  end

  local old_cwd = vim.fn.getcwd()

  -- Change directory
  local strategy = M.config.goto_strategy
  if strategy == "tcd" then
    vim.cmd("tcd " .. vim.fn.fnameescape(worktree_path))
  elseif strategy == "lcd" then
    vim.cmd("lcd " .. vim.fn.fnameescape(worktree_path))
  else
    vim.cmd("cd " .. vim.fn.fnameescape(worktree_path))
  end

  -- Optionally close unmodified buffers from old worktree
  if M.config.goto_close_buffers and old_cwd ~= worktree_path then
    for _, bufnr in ipairs(vim.api.nvim_list_bufs()) do
      if vim.api.nvim_buf_is_loaded(bufnr) and not vim.bo[bufnr].modified then
        local bufname = vim.api.nvim_buf_get_name(bufnr)
        if bufname:find(old_cwd, 1, true) == 1 then
          vim.api.nvim_buf_delete(bufnr, { force = false })
        end
      end
    end
  end

  -- Optionally open file explorer
  if M.config.goto_open_explorer then
    -- Try common file explorers
    local ok = pcall(function()
      if pcall(require, "neo-tree") then
        vim.cmd("Neotree dir=" .. vim.fn.fnameescape(worktree_path))
      elseif pcall(require, "nvim-tree") then
        local api = require("nvim-tree.api")
        api.tree.change_root(worktree_path)
        api.tree.open()
      elseif pcall(require, "oil") then
        require("oil").open(worktree_path)
      end
    end)
    -- Silently ignore if no explorer is available (ok unused intentionally)
  end

  -- Invalidate cache and fire autocommand
  require("ezstack.cli").invalidate_cache()
  vim.api.nvim_exec_autocmds("User", { pattern = "EzstackGoto" })

  vim.notify("Switched to " .. worktree_path, vim.log.levels.INFO)
end

return M
