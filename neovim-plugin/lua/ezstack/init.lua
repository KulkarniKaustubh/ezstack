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
    vim.keymap.set("n", "gn", "<cmd>Ezs up<cr>", { desc = "ezstack: up the stack" })
    vim.keymap.set("n", "gp", "<cmd>Ezs down<cr>", { desc = "ezstack: down the stack" })
  end

  M._first_run_welcome()

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

function M._first_run_welcome()
  local home = vim.env.HOME or ""
  if home == "" then
    return
  end
  local dir = home .. "/.ezstack"
  local marker = dir .. "/.nvim_welcomed"
  if vim.fn.filereadable(marker) == 1 then
    return
  end
  if vim.fn.isdirectory(dir) == 0 then
    pcall(vim.fn.mkdir, dir, "p")
  end
  local ok = pcall(function()
    local f = assert(io.open(marker, "w"))
    f:write("welcomed\n")
    f:close()
  end)
  if not ok then
    return
  end
  vim.schedule(function()
    vim.notify(
      "Welcome to ezstack.nvim!\n"
        .. "  :Ezs list      open the stack viewer\n"
        .. "  :Ezs graph     ASCII tree of all stacks\n"
        .. "  :Ezs new       create a stacked branch\n"
        .. "  :EzsActions    quick action menu\n"
        .. "Run :help ezstack for more.",
      vim.log.levels.INFO
    )
  end)
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

--- Statusline component.
--- Returns a string like " branch-name | stack-name [hash]" or "".
--- Cached to avoid repeated CLI calls.
---@return string
function M.statusline()
  local cli = require("ezstack.cli")
  local stacks = cli.list_stacks_sync()

  if #stacks == 0 then
    return ""
  end

  for _, stack in ipairs(stacks) do
    for _, branch in ipairs(stack.branches or {}) do
      if branch.is_current then
        local pr = ""
        if branch.pr_number and branch.pr_number > 0 then
          local state = branch.pr_state and branch.pr_state ~= "" and (" " .. branch.pr_state) or ""
          pr = string.format(" PR#%d%s", branch.pr_number, state)
        end
        return string.format(" %s%s", branch.name, pr)
      end
    end
  end

  return ""
end

--- `:EzsActions` — quick action menu (sync/push/pr) via vim.ui.select.
function M.actions_menu()
  local items = { "sync", "push", "push --stack", "pr create", "pr update", "pr open" }
  vim.ui.select(items, { prompt = "ezstack action:" }, function(choice)
    if not choice then
      return
    end
    local cli = require("ezstack.cli")
    if choice == "sync" then
      cli.run_in_terminal({ "sync" })
    elseif choice == "push" then
      vim.notify("Pushing branch...", vim.log.levels.INFO)
      cli.push({}, function(err)
        if err then
          vim.notify("Push failed: " .. err, vim.log.levels.ERROR)
        else
          vim.notify("Branch pushed", vim.log.levels.INFO)
        end
      end)
    elseif choice == "push --stack" then
      vim.notify("Pushing stack...", vim.log.levels.INFO)
      cli.push_stack({}, function(err)
        if err then
          vim.notify("Push failed: " .. err, vim.log.levels.ERROR)
        else
          vim.notify("Stack pushed", vim.log.levels.INFO)
        end
      end)
    elseif choice == "pr create" then
      vim.cmd("Ezs pr create")
    elseif choice == "pr update" then
      cli.pr_update(nil, function(err)
        if err then
          vim.notify("PR update failed: " .. err, vim.log.levels.ERROR)
        else
          vim.notify("PR updated", vim.log.levels.INFO)
        end
      end)
    elseif choice == "pr open" then
      vim.cmd("Ezs pr open")
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
