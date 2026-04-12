local cli = require("ezstack.cli")
local ezstack = require("ezstack")

local M = {}

-- Buffer-local data stored per viewer buffer
-- Maps bufnr -> { stacks, line_map }
local _buf_data = {}

--- Sort branches topologically (parent before child).
---@param branches table[]
---@param root string
---@return table[]
local function sort_branches(branches, root)
  -- Build children map
  local children = {}
  for _, b in ipairs(branches) do
    local parent = b.parent
    if not children[parent] then
      children[parent] = {}
    end
    table.insert(children[parent], b)
  end

  -- DFS from root
  local sorted = {}
  local function walk(parent_name)
    local kids = children[parent_name]
    if not kids then
      return
    end
    for _, b in ipairs(kids) do
      table.insert(sorted, b)
      walk(b.name)
    end
  end
  walk(root)

  -- If DFS missed any branches (shouldn't happen), append them
  if #sorted < #branches then
    local seen = {}
    for _, b in ipairs(sorted) do
      seen[b.name] = true
    end
    for _, b in ipairs(branches) do
      if not seen[b.name] then
        table.insert(sorted, b)
      end
    end
  end

  return sorted
end

--- Render a single stack into lines and highlight data.
---@param stack table Stack JSON data
---@return string[] lines
---@return table[] highlights Array of {line, col_start, col_end, hl_group}
---@return table[] line_map Array of {type="stack"|"branch"|"separator", stack_hash, branch_name, branch_data}
local function render_stack(stack, line_offset)
  local lines = {}
  local highlights = {}
  local line_map = {}

  -- Stack header
  local display_name = stack.name and (stack.name .. " [" .. stack.hash .. "]") or stack.hash
  local header = " Stack: " .. display_name
  local root_info = "root: " .. stack.root
  -- Pad to align root info
  local padding = math.max(1, 60 - #header - #root_info)
  local header_line = header .. string.rep(" ", padding) .. root_info

  table.insert(lines, header_line)
  table.insert(highlights, { line_offset + #lines - 1, 0, #header, "EzstackStack" })
  table.insert(highlights, { line_offset + #lines - 1, #header_line - #root_info, #header_line, "EzstackRoot" })
  table.insert(line_map, { type = "stack", stack_hash = stack.hash, stack = stack })

  -- Separator
  local sep_line = " " .. string.rep("─", 65)
  table.insert(lines, sep_line)
  table.insert(highlights, { line_offset + #lines - 1, 0, #sep_line, "EzstackSeparator" })
  table.insert(line_map, { type = "separator" })

  -- Branches
  local branches = stack.branches or {}
  if #branches == 0 then
    table.insert(lines, "   (empty)")
    table.insert(highlights, { line_offset + #lines - 1, 0, 12, "EzstackBranchMerged" })
    table.insert(line_map, { type = "empty" })
  else
    local sorted = sort_branches(branches, stack.root)

    for i, branch in ipairs(sorted) do
      local is_last = (i == #sorted)
      local is_current = branch.is_current
      local is_merged = branch.is_merged

      -- Pointer
      local pointer = is_current and " > " or "   "

      -- Connector
      local connector = is_last and "└── " or "├── "

      -- Branch name (truncated)
      local name = branch.name
      if #name > 30 then
        name = name:sub(1, 27) .. "..."
      end
      -- Pad name
      local padded_name = name .. string.rep(" ", math.max(1, 20 - #name))

      -- PR info
      local pr_text
      if branch.pr_number and branch.pr_number > 0 then
        local state = branch.pr_state or ""
        if state ~= "" then
          pr_text = string.format("PR #%d [%s]", branch.pr_number, state)
        else
          pr_text = string.format("PR #%d", branch.pr_number)
        end
      else
        pr_text = "[no PR]"
      end
      local padded_pr = pr_text .. string.rep(" ", math.max(1, 18 - #pr_text))

      -- CI info
      local ci_text = ""
      if branch.ci_summary and branch.ci_summary ~= "" then
        ci_text = "CI: " .. branch.ci_summary
      elseif branch.ci_state and branch.ci_state ~= "" and branch.ci_state ~= "none" then
        ci_text = "CI: " .. branch.ci_state
      end
      local padded_ci = ci_text .. string.rep(" ", math.max(1, 14 - #ci_text))

      -- Diff stats
      local diff_text = ""
      local diff_add_text = ""
      local diff_del_text = ""
      if branch.additions or branch.deletions then
        local adds = branch.additions or 0
        local dels = branch.deletions or 0
        diff_add_text = "+" .. adds
        diff_del_text = "-" .. dels
        diff_text = diff_add_text .. " " .. diff_del_text
      end
      local padded_diff = diff_text ~= "" and (diff_text .. string.rep(" ", math.max(1, 12 - #diff_text))) or ""

      -- Parent info
      local parent_text = branch.parent and branch.parent ~= "" and ("(→ " .. branch.parent .. ")") or ""

      local line = pointer .. connector .. padded_name .. padded_pr .. padded_ci .. padded_diff .. parent_text
      table.insert(lines, line)

      local ln = line_offset + #lines - 1

      -- Highlights
      if is_current then
        table.insert(highlights, { ln, 0, #pointer, "EzstackPointer" })
      end
      table.insert(highlights, { ln, #pointer, #pointer + #connector, "EzstackConnector" })

      local name_start = #pointer + #connector
      local name_end = name_start + #name
      if is_merged then
        table.insert(highlights, { ln, name_start, #line, "EzstackBranchMerged" })
      elseif is_current then
        table.insert(highlights, { ln, name_start, name_end, "EzstackBranchCurrent" })
      else
        table.insert(highlights, { ln, name_start, name_end, "EzstackBranch" })
      end

      -- PR highlight
      local pr_start = name_start + #padded_name
      if branch.pr_number and branch.pr_number > 0 then
        local pr_hl = "EzstackPR"
        local state = (branch.pr_state or ""):upper()
        if state == "DRAFT" then
          pr_hl = "EzstackPRDraft"
        elseif state == "MERGED" then
          pr_hl = "EzstackPRMerged"
        elseif state == "CLOSED" then
          pr_hl = "EzstackPRClosed"
        end
        table.insert(highlights, { ln, pr_start, pr_start + #pr_text, pr_hl })
      else
        table.insert(highlights, { ln, pr_start, pr_start + #pr_text, "EzstackNoPR" })
      end

      -- CI highlight
      if ci_text ~= "" then
        local ci_start = pr_start + #padded_pr
        local ci_hl = "EzstackCIPending"
        local ci_state = (branch.ci_state or ""):lower()
        if ci_state == "success" then
          ci_hl = "EzstackCIPass"
        elseif ci_state == "failure" then
          ci_hl = "EzstackCIFail"
        end
        table.insert(highlights, { ln, ci_start, ci_start + #ci_text, ci_hl })
      end

      -- Diff stats highlight
      if diff_text ~= "" then
        local diff_start = pr_start + #padded_pr + #padded_ci
        table.insert(highlights, { ln, diff_start, diff_start + #diff_add_text, "EzstackAdditions" })
        local del_start = diff_start + #diff_add_text + 1
        table.insert(highlights, { ln, del_start, del_start + #diff_del_text, "EzstackDeletions" })
      end

      -- Parent highlight
      if parent_text ~= "" then
        local parent_start = #line - #parent_text
        table.insert(highlights, { ln, parent_start, #line, "EzstackParent" })
      end

      table.insert(line_map, {
        type = "branch",
        stack_hash = stack.hash,
        branch_name = branch.name,
        branch = branch,
      })
    end
  end

  return lines, highlights, line_map
end

--- Get the namespace for ezstack highlights.
---@return number
local function get_ns()
  return vim.api.nvim_create_namespace("ezstack")
end

--- Open or refresh the stack viewer buffer.
---@param use_status? boolean If true, fetch status (PR/CI info) instead of basic list
function M.open(use_status)
  local fetch = use_status
    and function(cb)
      cli.status_stacks(cb, { all = true })
    end
    or function(cb)
      cli.list_stacks(cb, { force = true, all = true })
    end

  fetch(function(err, stacks)
    if err then
      vim.notify("ezstack: " .. err, vim.log.levels.ERROR)
      return
    end

    if #stacks == 0 then
      vim.notify("No stacks found. Create one with: ezs new <branch-name>", vim.log.levels.INFO)
      return
    end

    -- Find or create the viewer buffer
    local bufnr = M._find_viewer_buf()
    if not bufnr then
      bufnr = M._create_viewer_buf()
    end

    -- Render all stacks
    local all_lines = {}
    local all_highlights = {}
    local all_line_map = {}

    for idx, stack in ipairs(stacks) do
      if idx > 1 then
        table.insert(all_lines, "")
        table.insert(all_line_map, { type = "blank" })
      end

      local lines, highlights, line_map = render_stack(stack, #all_lines)
      for _, l in ipairs(lines) do
        table.insert(all_lines, l)
      end
      for _, h in ipairs(highlights) do
        table.insert(all_highlights, h)
      end
      for _, m in ipairs(line_map) do
        table.insert(all_line_map, m)
      end
    end

    -- Write to buffer (guard against async race if buffer was wiped).
    -- Preserve cursor row across refreshes so the user doesn't lose position.
    if not vim.api.nvim_buf_is_valid(bufnr) then
      bufnr = M._create_viewer_buf()
    end
    local saved_cursor
    for _, win in ipairs(vim.api.nvim_list_wins()) do
      if vim.api.nvim_win_get_buf(win) == bufnr then
        saved_cursor = vim.api.nvim_win_get_cursor(win)
        break
      end
    end

    vim.bo[bufnr].modifiable = true
    vim.api.nvim_buf_set_lines(bufnr, 0, -1, false, all_lines)
    vim.bo[bufnr].modifiable = false

    if saved_cursor then
      for _, win in ipairs(vim.api.nvim_list_wins()) do
        if vim.api.nvim_win_get_buf(win) == bufnr then
          local row = math.min(saved_cursor[1], #all_lines)
          pcall(vim.api.nvim_win_set_cursor, win, { math.max(1, row), saved_cursor[2] })
          break
        end
      end
    end

    -- Apply highlights via extmarks (nvim_buf_add_highlight is deprecated
    -- in nvim 0.11 and slated for removal).
    local ns = get_ns()
    vim.api.nvim_buf_clear_namespace(bufnr, ns, 0, -1)
    for _, h in ipairs(all_highlights) do
      local line, col_start, col_end, hl_group = h[1], h[2], h[3], h[4]
      pcall(vim.api.nvim_buf_set_extmark, bufnr, ns, line, col_start, {
        end_col = col_end,
        hl_group = hl_group,
        strict = false,
      })
    end

    -- Store line map for keymaps
    _buf_data[bufnr] = {
      stacks = stacks,
      line_map = all_line_map,
      use_status = use_status,
    }

    -- Show the buffer if it's not already visible
    M._ensure_visible(bufnr)
  end)
end

--- Get the use_status flag for a viewer buffer.
---@param bufnr number
---@return boolean|nil
function M.get_viewer_use_status(bufnr)
  local data = _buf_data[bufnr]
  if not data then
    return nil
  end
  return data.use_status
end

--- Get the line data at the cursor in a viewer buffer.
---@param bufnr number
---@return table|nil line_data
function M.get_cursor_data(bufnr)
  local data = _buf_data[bufnr]
  if not data then
    return nil
  end
  local line = vim.api.nvim_win_get_cursor(0)[1] -- 1-indexed
  return data.line_map[line]
end

--- Find an existing viewer buffer.
---@return number|nil
function M._find_viewer_buf()
  for _, bufnr in ipairs(vim.api.nvim_list_bufs()) do
    if vim.api.nvim_buf_is_valid(bufnr) and vim.bo[bufnr].filetype == "ezstack" then
      return bufnr
    end
  end
  return nil
end

--- Create a new viewer buffer with keymaps.
---@return number bufnr
function M._create_viewer_buf()
  local config = ezstack.config
  vim.cmd(config.viewer_position .. " " .. config.viewer_height .. "split")
  local bufnr = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_win_set_buf(0, bufnr)

  vim.bo[bufnr].filetype = "ezstack"
  vim.bo[bufnr].buftype = "nofile"
  vim.bo[bufnr].bufhidden = "wipe"
  vim.bo[bufnr].swapfile = false
  vim.bo[bufnr].modifiable = false

  vim.wo[0].number = false
  vim.wo[0].relativenumber = false
  vim.wo[0].signcolumn = "no"
  vim.wo[0].foldcolumn = "0"
  vim.wo[0].wrap = false
  vim.wo[0].cursorline = true

  -- Setup keymaps
  M._setup_keymaps(bufnr)

  -- Cleanup on buffer wipe
  vim.api.nvim_create_autocmd("BufWipeout", {
    buffer = bufnr,
    once = true,
    callback = function()
      _buf_data[bufnr] = nil
    end,
  })

  return bufnr
end

--- Ensure the viewer buffer is visible in a window.
---@param bufnr number
function M._ensure_visible(bufnr)
  for _, win in ipairs(vim.api.nvim_list_wins()) do
    if vim.api.nvim_win_get_buf(win) == bufnr then
      return -- already visible
    end
  end
  -- Open in a split
  local config = ezstack.config
  vim.cmd(config.viewer_position .. " " .. config.viewer_height .. "split")
  vim.api.nvim_win_set_buf(0, bufnr)
end

--- Setup buffer-local keymaps for the viewer.
---@param bufnr number
function M._setup_keymaps(bufnr)
  local map = function(key, fn, desc)
    vim.keymap.set("n", key, fn, { buffer = bufnr, desc = desc, nowait = true })
  end

  -- q — close
  map("q", function()
    local win = vim.api.nvim_get_current_win()
    vim.api.nvim_win_close(win, true)
  end, "Close viewer")

  -- r — refresh
  map("r", function()
    local data = _buf_data[bufnr]
    M.open(data and data.use_status)
  end, "Refresh")

  -- <CR> — goto worktree
  map("<CR>", function()
    local entry = M.get_cursor_data(bufnr)
    if not entry or entry.type ~= "branch" then
      return
    end
    local wt = entry.branch and entry.branch.worktree_path
    if wt and wt ~= "" then
      ezstack.goto_worktree(wt)
    else
      vim.notify("No worktree path for " .. entry.branch_name, vim.log.levels.WARN)
    end
  end, "Go to worktree")

  -- o — open PR in browser
  map("o", function()
    local entry = M.get_cursor_data(bufnr)
    if not entry or entry.type ~= "branch" then
      return
    end
    local url = entry.branch and entry.branch.pr_url
    if url and url ~= "" then
      vim.ui.open(url)
    else
      vim.notify("No PR URL for " .. entry.branch_name, vim.log.levels.INFO)
    end
  end, "Open PR in browser")

  -- R — rename stack
  map("R", function()
    local entry = M.get_cursor_data(bufnr)
    if not entry then
      return
    end
    -- Find the stack hash from the current or nearest stack line
    local stack_hash = entry.stack_hash
    if not stack_hash then
      return
    end
    -- Find the stack to get current name
    local data = _buf_data[bufnr]
    local current_name = ""
    if data then
      for _, s in ipairs(data.stacks) do
        if s.hash == stack_hash then
          current_name = s.name or ""
          break
        end
      end
    end

    vim.ui.input({
      prompt = "Stack name (empty to clear): ",
      default = current_name,
    }, function(name)
      if name == nil then
        return -- cancelled
      end
      cli.rename_stack(stack_hash, name, function(err)
        if err then
          vim.notify("Rename failed: " .. err, vim.log.levels.ERROR)
        else
          local msg = name ~= "" and ('Renamed stack to "' .. name .. '"') or "Cleared stack name"
          vim.notify(msg, vim.log.levels.INFO)
          M.open(data and data.use_status)
        end
      end)
    end)
  end, "Rename stack")

  -- n — new branch
  map("n", function()
    vim.ui.input({ prompt = "New branch name: " }, function(name)
      if not name or name == "" then
        return
      end
      -- Pick parent from all branches
      local data = _buf_data[bufnr]
      if not data then
        return
      end
      local candidates = {}
      for _, s in ipairs(data.stacks) do
        table.insert(candidates, s.root)
        for _, b in ipairs(s.branches or {}) do
          table.insert(candidates, b.name)
        end
      end
      -- Deduplicate
      local seen = {}
      local unique = {}
      for _, c in ipairs(candidates) do
        if not seen[c] then
          seen[c] = true
          table.insert(unique, c)
        end
      end

      vim.ui.select(unique, { prompt = "Parent branch:" }, function(parent)
        if not parent then
          return
        end
        cli.new_branch(name, parent, function(err)
          if err then
            vim.notify("Failed to create branch: " .. err, vim.log.levels.ERROR)
          else
            vim.notify('Created branch "' .. name .. '"', vim.log.levels.INFO)
            M.open(data.use_status)
          end
        end)
      end)
    end)
  end, "New branch")

  -- d — delete branch
  map("d", function()
    local entry = M.get_cursor_data(bufnr)
    if not entry or entry.type ~= "branch" then
      return
    end
    local name = entry.branch_name
    vim.ui.select({ "Yes", "No" }, {
      prompt = 'Delete branch "' .. name .. '" and its worktree?',
    }, function(choice)
      if choice ~= "Yes" then
        return
      end
      cli.delete_branch(name, function(err)
        if err then
          vim.notify("Delete failed: " .. err, vim.log.levels.ERROR)
        else
          vim.notify('Deleted branch "' .. name .. '"', vim.log.levels.INFO)
          local data = _buf_data[bufnr]
          M.open(data and data.use_status)
        end
      end)
    end)
  end, "Delete branch")

  -- p — push branch
  map("p", function()
    local entry = M.get_cursor_data(bufnr)
    if not entry or entry.type ~= "branch" then
      return
    end
    vim.notify("Pushing branch...", vim.log.levels.INFO)
    cli.push({}, function(err)
      if err then
        vim.notify("Push failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("Branch pushed", vim.log.levels.INFO)
      end
    end)
  end, "Push branch")

  -- P — push stack
  map("P", function()
    vim.notify("Pushing stack...", vim.log.levels.INFO)
    cli.push_stack({}, function(err)
      if err then
        vim.notify("Push failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("Stack pushed", vim.log.levels.INFO)
      end
    end)
  end, "Push stack")

  -- s — sync (interactive, opens terminal). The CLI's interactive menu
  -- already handles "current stack" vs "all" vs "current branch", so we
  -- launch it without `-s` and let the user choose.
  map("s", function()
    cli.run_in_terminal({ "sync" })
  end, "Sync stack")

  -- c — sync continue (after resolving conflicts in another window)
  map("c", function()
    vim.notify("Continuing sync...", vim.log.levels.INFO)
    cli.sync_continue(function(err)
      if err then
        vim.notify("Sync continue failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("Sync completed", vim.log.levels.INFO)
      end
    end)
  end, "Sync continue")

  -- u — update PR for branch under cursor
  map("u", function()
    local entry = M.get_cursor_data(bufnr)
    if not entry or entry.type ~= "branch" then
      return
    end
    vim.notify("Updating PR for " .. entry.branch_name .. "...", vim.log.levels.INFO)
    cli.pr_update(entry.branch_name, function(err)
      if err then
        vim.notify("PR update failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("PR updated", vim.log.levels.INFO)
      end
    end)
  end, "Update PR for branch")

  -- D — diff against parent
  map("D", function()
    local entry = M.get_cursor_data(bufnr)
    if not entry or entry.type ~= "branch" then
      return
    end
    cli.run_in_terminal({ "diff" })
  end, "Diff against parent")

  -- a — open agent
  map("a", function()
    local entry = M.get_cursor_data(bufnr)
    if not entry then
      return
    end
    if entry.type == "branch" and entry.branch_name then
      cli.run_in_terminal({ "agent", "-b", entry.branch_name })
    elseif entry.stack_hash then
      cli.run_in_terminal({ "agent", "-s", entry.stack_hash })
    else
      cli.run_in_terminal({ "agent" })
    end
  end, "Open agent")

  -- ? — show help
  map("?", function()
    M._show_help()
  end, "Show keymap help")

  -- A — open agent feature
  map("A", function()
    local entry = M.get_cursor_data(bufnr)
    if not entry or not entry.stack_hash then
      vim.notify("Place cursor on a stack or branch to use agent feature mode", vim.log.levels.WARN)
      return
    end
    vim.ui.input({ prompt = "Feature description: " }, function(description)
      if not description or description == "" then
        return
      end
      cli.run_in_terminal({ "agent", "-s", entry.stack_hash, "feature", description })
    end)
  end, "Build feature with agent")
end

--- Show a help popup with the viewer keymaps.
function M._show_help()
  local lines = {
    " ezstack viewer keymaps",
    " ──────────────────────",
    "  <CR>  Go to worktree",
    "  o     Open PR in browser",
    "  r     Refresh",
    "  R     Rename stack",
    "  n     New branch",
    "  d     Delete branch under cursor",
    "  p     Push branch under cursor",
    "  P     Push entire stack",
    "  s     Sync stack (interactive terminal)",
    "  c     Continue an in-progress sync",
    "  u     Update PR for branch under cursor",
    "  D     Diff against parent branch",
    "  a     Open AI agent (branch- or stack-scoped)",
    "  A     Build a feature with the AI agent",
    "  ?     Show this help",
    "  q     Close viewer",
    "",
    " Press q to close.",
  }
  local bufnr = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_buf_set_lines(bufnr, 0, -1, false, lines)
  vim.bo[bufnr].modifiable = false
  vim.bo[bufnr].buftype = "nofile"
  vim.bo[bufnr].bufhidden = "wipe"

  local width = 40
  local height = #lines
  local ui_w = vim.o.columns
  local ui_h = vim.o.lines
  vim.api.nvim_open_win(bufnr, true, {
    relative = "editor",
    row = math.floor((ui_h - height) / 2),
    col = math.floor((ui_w - width) / 2),
    width = width,
    height = height,
    style = "minimal",
    border = "rounded",
    title = " ezstack help ",
    title_pos = "center",
  })
  vim.keymap.set("n", "q", "<cmd>close<cr>", { buffer = bufnr, silent = true })
  vim.keymap.set("n", "<Esc>", "<cmd>close<cr>", { buffer = bufnr, silent = true })
end

--- Render a stack tree as plain text lines (for Telescope preview).
---@param stack table Stack JSON data
---@param highlight_branch? string Branch name to highlight
---@return string[] lines
function M.render_preview(stack, highlight_branch)
  local lines = {}
  local display_name = stack.name and (stack.name .. " [" .. stack.hash .. "]") or stack.hash
  table.insert(lines, "Stack: " .. display_name .. "  root: " .. stack.root)
  table.insert(lines, string.rep("─", 50))

  local branches = stack.branches or {}
  if #branches == 0 then
    table.insert(lines, "  (empty)")
    return lines
  end

  local sorted = sort_branches(branches, stack.root)
  for i, branch in ipairs(sorted) do
    local is_last = (i == #sorted)
    local marker = ""
    if highlight_branch and branch.name == highlight_branch then
      marker = "> "
    elseif branch.is_current then
      marker = "> "
    else
      marker = "  "
    end
    local connector = is_last and "└── " or "├── "

    local pr_text = ""
    if branch.pr_number and branch.pr_number > 0 then
      pr_text = string.format("  PR #%d", branch.pr_number)
      if branch.pr_state and branch.pr_state ~= "" then
        pr_text = pr_text .. " [" .. branch.pr_state .. "]"
      end
    end

    local diff_text = ""
    if branch.additions or branch.deletions then
      local adds = branch.additions or 0
      local dels = branch.deletions or 0
      diff_text = string.format("  +%d -%d", adds, dels)
    end

    local parent_text = branch.parent and branch.parent ~= "" and ("  (→ " .. branch.parent .. ")") or ""
    table.insert(lines, marker .. connector .. branch.name .. pr_text .. diff_text .. parent_text)
  end

  return lines
end

return M
