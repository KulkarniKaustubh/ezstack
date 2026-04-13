local cli = require("ezstack.cli")
local ui = require("ezstack.ui")
local ezstack = require("ezstack")

local M = {}

--- All top-level subcommands. Used for both dispatch and completion.
local SUBCOMMAND_NAMES = {
  "actions", "agent", "amend", "commit", "config", "delete", "diff", "down",
  "goto", "graph", "list", "log", "menu", "new", "pr", "push", "rename",
  "reparent", "stack", "status", "sync", "unstack", "up",
}

--- Subcommand dispatch table.
---@type table<string, fun(args: string[], raw_args: string)>
local subcommands = {}

-- ── Helpers ──

--- Notify on async result with a friendly label.
---@param label string Action label, e.g. "Push"
---@param success_msg string|nil
local function notify_cb(label, success_msg)
  return function(err)
    if err then
      vim.notify(label .. " failed: " .. err, vim.log.levels.ERROR)
    elseif success_msg then
      vim.notify(success_msg, vim.log.levels.INFO)
    end
  end
end

--- Collect every branch name from the stack list (including roots).
---@param stacks table[]
---@return string[]
local function all_branch_names(stacks)
  local seen, out = {}, {}
  for _, s in ipairs(stacks) do
    if s.root and not seen[s.root] then
      seen[s.root] = true
      table.insert(out, s.root)
    end
    for _, b in ipairs(s.branches or {}) do
      if not seen[b.name] then
        seen[b.name] = true
        table.insert(out, b.name)
      end
    end
  end
  return out
end

--- Build a branch -> worktree map.
local function branch_worktree_map(stacks)
  local map = {}
  for _, s in ipairs(stacks) do
    for _, b in ipairs(s.branches or {}) do
      if b.worktree_path and b.worktree_path ~= "" then
        map[b.name] = b.worktree_path
      end
    end
  end
  return map
end

-- ── Subcommands ──

--- `:Ezs` or `:Ezs list` — open the stack viewer.
subcommands["list"] = function()
  ui.open(false)
end

--- `:Ezs status` — open the stack viewer with PR/CI info.
subcommands["status"] = function()
  ui.open(true)
end

--- `:Ezs new <name> [parent]`
subcommands["new"] = function(args)
  local name = args[1]
  if not name then
    vim.ui.input({ prompt = "New branch name: " }, function(input)
      if not input or input == "" then
        return
      end
      M._new_with_parent_pick(input)
    end)
    return
  end
  local parent = args[2]
  if parent then
    cli.new_branch(name, parent, notify_cb("Create branch", 'Created branch "' .. name .. '"'))
  else
    M._new_with_parent_pick(name)
  end
end

function M._new_with_parent_pick(name)
  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("ezstack: list stacks failed: " .. tostring(err), vim.log.levels.ERROR)
      return
    end
    local candidates = all_branch_names(stacks)
    if #candidates == 0 then
      cli.new_branch(name, nil, notify_cb("Create branch", 'Created branch "' .. name .. '"'))
      return
    end
    vim.ui.select(candidates, { prompt = "Parent branch:" }, function(parent)
      if not parent then
        return
      end
      cli.new_branch(name, parent, notify_cb("Create branch", 'Created branch "' .. name .. '"'))
    end)
  end, { force = true, all = true })
end

--- `:Ezs sync [-c|-s|-a|--continue|--dry-run|--merge|--rebase]`
subcommands["sync"] = function(args)
  -- `--continue` is a headless completion, no need for terminal.
  if args[1] == "--continue" then
    vim.notify("Continuing sync...", vim.log.levels.INFO)
    cli.sync_continue(notify_cb("Sync continue", "Sync completed"))
    return
  end

  -- `--dry-run` returns JSON; show it in a scratch buffer.
  if args[1] == "--dry-run" then
    cli.sync_dry_run({ all = vim.tbl_contains(args, "--all") or vim.tbl_contains(args, "-a") }, function(err, plan)
      if err then
        vim.notify("Sync dry-run failed: " .. err, vim.log.levels.ERROR)
        return
      end
      M._show_scratch("ezstack://sync-dry-run", "json",
        vim.split(vim.json.encode(plan or {}), "\n"))
    end)
    return
  end

  -- All other modes are interactive; run in a terminal so the user can
  -- resolve conflicts and respond to menus.
  local cli_args = { "sync" }
  vim.list_extend(cli_args, args)
  cli.run_in_terminal(cli_args)
end

--- `:Ezs push [-s|--stack] [-f|--force]`
subcommands["push"] = function(args)
  local is_stack = false
  local is_force = false
  for _, a in ipairs(args) do
    if a == "-s" or a == "--stack" then
      is_stack = true
    elseif a == "-f" or a == "--force" then
      is_force = true
    end
  end
  if is_stack then
    vim.notify(is_force and "Force-pushing stack..." or "Pushing stack...", vim.log.levels.INFO)
    cli.push_stack({ force = is_force }, notify_cb("Push stack", "Stack pushed"))
  else
    vim.notify(is_force and "Force-pushing branch..." or "Pushing branch...", vim.log.levels.INFO)
    cli.push({ force = is_force }, notify_cb("Push", "Branch pushed"))
  end
end

--- `:Ezs pr <subcommand> [args]`
subcommands["pr"] = function(args)
  local sub = args[1]
  local rest = vim.list_slice(args, 2)

  if sub == "create" then
    local title = table.concat(rest, " ")
    if title == "" then
      vim.ui.input({ prompt = "PR title: " }, function(input)
        if not input or input == "" then
          return
        end
        vim.ui.select({ "Ready for review", "Draft" }, { prompt = "PR type:" }, function(choice)
          if not choice then
            return
          end
          cli.pr_create(input, { draft = choice == "Draft" },
            notify_cb("PR create", "PR created"))
        end)
      end)
      return
    end
    cli.pr_create(title, {}, notify_cb("PR create", "PR created"))

  elseif sub == "update" then
    vim.notify("Updating PR...", vim.log.levels.INFO)
    cli.pr_update(rest[1], notify_cb("PR update", "PR updated"))

  elseif sub == "merge" then
    vim.ui.select({ "Squash and merge", "Create merge commit", "Rebase and merge" }, {
      prompt = "Merge method:",
    }, function(choice)
      if not choice then
        return
      end
      local method_map = {
        ["Squash and merge"] = "squash",
        ["Create merge commit"] = "merge",
        ["Rebase and merge"] = "rebase",
      }
      cli.pr_merge(method_map[choice], rest[1], notify_cb("PR merge", "PR merged"))
    end)

  elseif sub == "draft" then
    vim.notify("Toggling draft...", vim.log.levels.INFO)
    cli.pr_draft(rest[1], notify_cb("PR draft", "Draft status toggled"))

  elseif sub == "stack" then
    vim.notify("Updating stack info in PRs...", vim.log.levels.INFO)
    cli.pr_stack(notify_cb("PR stack", "Stack info updated in PRs"))

  elseif sub == "open" then
    -- Open the cursor's PR (or the current branch's PR) in the browser.
    cli.list_stacks(function(err, stacks)
      if err then
        vim.notify("Failed to list stacks: " .. err, vim.log.levels.ERROR)
        return
      end
      for _, s in ipairs(stacks) do
        for _, b in ipairs(s.branches or {}) do
          if b.is_current and b.pr_url and b.pr_url ~= "" then
            vim.ui.open(b.pr_url)
            return
          end
        end
      end
      vim.notify("No PR for the current branch", vim.log.levels.INFO)
    end, { force = true })

  else
    vim.notify("Unknown pr subcommand: " .. (sub or ""), vim.log.levels.ERROR)
  end
end

--- `:Ezs delete [branch]`
subcommands["delete"] = function(args)
  local branch = args[1]
  if not branch then
    cli.list_stacks(function(err, stacks)
      if err then
        vim.notify("Failed to list stacks: " .. err, vim.log.levels.ERROR)
        return
      end
      local candidates = {}
      for _, s in ipairs(stacks) do
        for _, b in ipairs(s.branches or {}) do
          table.insert(candidates, b.name)
        end
      end
      if #candidates == 0 then
        vim.notify("No branches to delete", vim.log.levels.INFO)
        return
      end
      vim.ui.select(candidates, { prompt = "Branch to delete:" }, function(choice)
        if not choice then
          return
        end
        M._confirm_delete(choice)
      end)
    end, { force = true })
    return
  end
  M._confirm_delete(branch)
end

function M._confirm_delete(name)
  vim.ui.select({ "Yes", "No" }, {
    prompt = 'Delete branch "' .. name .. '" and its worktree?',
  }, function(choice)
    if choice ~= "Yes" then
      return
    end
    cli.delete_branch(name, notify_cb("Delete", 'Deleted branch "' .. name .. '"'))
  end)
end

--- `:Ezs reparent [branch] [parent]`
subcommands["reparent"] = function(args)
  local branch_name = args[1]
  local new_parent = args[2]

  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("Failed to list stacks: " .. err, vim.log.levels.ERROR)
      return
    end
    local all_branches = all_branch_names(stacks)

    local function do_reparent(bn, np)
      cli.reparent(bn, np, notify_cb("Reparent",
        string.format('Reparented "%s" onto "%s"', bn, np)))
    end

    if branch_name and new_parent then
      do_reparent(branch_name, new_parent)
      return
    end

    local branch_candidates = {}
    for _, s in ipairs(stacks) do
      for _, b in ipairs(s.branches or {}) do
        table.insert(branch_candidates, b.name)
      end
    end

    if not branch_name then
      vim.ui.select(branch_candidates, { prompt = "Branch to reparent:" }, function(choice)
        if not choice then
          return
        end
        local parent_candidates = vim.tbl_filter(function(n)
          return n ~= choice
        end, all_branches)
        vim.ui.select(parent_candidates, { prompt = "New parent:" }, function(parent)
          if not parent then
            return
          end
          do_reparent(choice, parent)
        end)
      end)
    else
      local parent_candidates = vim.tbl_filter(function(n)
        return n ~= branch_name
      end, all_branches)
      vim.ui.select(parent_candidates, { prompt = "New parent:" }, function(parent)
        if not parent then
          return
        end
        do_reparent(branch_name, parent)
      end)
    end
  end, { force = true, all = true })
end

--- `:Ezs rename [hash] [name]`
subcommands["rename"] = function(args)
  local hash = args[1]
  local name = args[2]

  if hash and name then
    cli.rename_stack(hash, name, notify_cb("Rename", 'Renamed stack to "' .. name .. '"'))
    return
  end

  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("Failed to list stacks: " .. err, vim.log.levels.ERROR)
      return
    end
    if #stacks == 0 then
      vim.notify("No stacks found", vim.log.levels.INFO)
      return
    end

    local items = {}
    for _, s in ipairs(stacks) do
      local label = s.name and (s.name .. " [" .. s.hash .. "]") or s.hash
      table.insert(items, { label = label, hash = s.hash, name = s.name })
    end

    local function prompt_for_name(stack_hash, current_name)
      vim.ui.input({
        prompt = "New name (empty to clear): ",
        default = current_name or "",
      }, function(new_name)
        if new_name == nil then
          return
        end
        cli.rename_stack(stack_hash, new_name, function(rename_err)
          if rename_err then
            vim.notify("Rename failed: " .. rename_err, vim.log.levels.ERROR)
          else
            local msg = new_name ~= "" and ('Renamed stack to "' .. new_name .. '"') or "Cleared stack name"
            vim.notify(msg, vim.log.levels.INFO)
          end
        end)
      end)
    end

    if not hash then
      vim.ui.select(
        vim.tbl_map(function(item) return item.label end, items),
        { prompt = "Stack to rename:" },
        function(_, idx)
          if not idx then
            return
          end
          local sel = items[idx]
          prompt_for_name(sel.hash, sel.name)
        end
      )
    else
      -- Resolve hash prefix
      for _, s in ipairs(items) do
        if s.hash == hash or s.hash:sub(1, #hash) == hash then
          prompt_for_name(s.hash, s.name)
          return
        end
      end
      vim.notify("Stack not found: " .. hash, vim.log.levels.ERROR)
    end
  end, { force = true, all = true })
end

--- `:Ezs goto [branch]`
subcommands["goto"] = function(args)
  local target = args[1]

  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("Failed to list stacks: " .. err, vim.log.levels.ERROR)
      return
    end

    local branch_map = branch_worktree_map(stacks)
    local branch_names = {}
    for _, s in ipairs(stacks) do
      for _, b in ipairs(s.branches or {}) do
        if branch_map[b.name] then
          local label = b.name
          if b.is_current then
            label = label .. " (current)"
          end
          table.insert(branch_names, label)
        end
      end
    end

    if #branch_names == 0 then
      vim.notify("No branches with worktrees found", vim.log.levels.INFO)
      return
    end

    if target then
      local wt = branch_map[target]
      if wt then
        ezstack.goto_worktree(wt)
      else
        vim.notify('Branch "' .. target .. '" not found or has no worktree', vim.log.levels.ERROR)
      end
      return
    end

    local ok, telescope = pcall(require, "ezstack.telescope")
    if ok and telescope.available() then
      telescope.branches()
    else
      vim.ui.select(branch_names, { prompt = "Go to branch:" }, function(choice)
        if not choice then
          return
        end
        local name = choice:gsub(" %(current%)$", "")
        local wt = branch_map[name]
        if wt then
          ezstack.goto_worktree(wt)
        end
      end)
    end
  end, { force = true, all = true })
end

--- `:Ezs diff [branch]` — show diff vs parent (or named branch) in a split.
subcommands["diff"] = function(args)
  local target = args[1]
  local function show(base)
    ui.show_diff(base)
  end
  if target then
    show(target)
    return
  end
  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("Failed to list stacks: " .. err, vim.log.levels.ERROR)
      return
    end
    local parent
    for _, s in ipairs(stacks) do
      for _, b in ipairs(s.branches or {}) do
        if b.is_current then
          parent = b.parent
          break
        end
      end
      if parent then break end
    end
    if not parent or parent == "" then
      vim.notify("No parent branch found for current branch", vim.log.levels.WARN)
      return
    end
    show(parent)
  end, { force = true, all = true })
end

--- `:Ezs graph` — render the stack as an ASCII tree.
subcommands["graph"] = function()
  ui.show_graph()
end

--- `:Ezs commit [args]`
subcommands["commit"] = function(args)
  local cli_args = { "commit" }
  vim.list_extend(cli_args, args)
  cli.run_in_terminal(cli_args)
end

--- `:Ezs amend` — amend last commit then run `ezs sync` to update children.
subcommands["amend"] = function()
  local result = vim.system(
    { "git", "commit", "--amend", "--no-edit" },
    { text = true, cwd = vim.fn.getcwd() }
  ):wait()
  if result.code ~= 0 then
    local err = vim.trim(result.stderr or "")
    vim.notify("git amend failed: " .. (err ~= "" and err or "code " .. result.code), vim.log.levels.ERROR)
    return
  end
  vim.notify("Amended; syncing children...", vim.log.levels.INFO)
  cli.run_in_terminal({ "sync" })
end

--- `:Ezs stack [branch] [parent]`
subcommands["stack"] = function(args)
  if args[1] == "rename" then
    subcommands["rename"](vim.list_slice(args, 2))
    return
  end
  local cli_args = { "stack" }
  vim.list_extend(cli_args, args)
  cli.run_in_terminal(cli_args)
end

--- `:Ezs unstack [branch]`
subcommands["unstack"] = function(args)
  local branch = args[1]
  local function do_unstack(name)
    cli.exec_yes({ "unstack", name }, function(err)
      if err then
        vim.notify("Unstack failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify('Unstacked "' .. name .. '"', vim.log.levels.INFO)
        cli.invalidate_cache()
      end
    end)
  end
  if branch then
    do_unstack(branch)
    return
  end
  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("Failed to list stacks: " .. err, vim.log.levels.ERROR)
      return
    end
    local candidates = {}
    for _, s in ipairs(stacks) do
      for _, b in ipairs(s.branches or {}) do
        table.insert(candidates, b.name)
      end
    end
    if #candidates == 0 then
      vim.notify("No branches to unstack", vim.log.levels.INFO)
      return
    end
    vim.ui.select(candidates, { prompt = "Branch to unstack:" }, function(choice)
      if choice then
        do_unstack(choice)
      end
    end)
  end, { force = true, all = true })
end

--- `:Ezs up` — navigate to parent branch.
subcommands["up"] = function()
  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("ezstack: " .. err, vim.log.levels.ERROR)
      return
    end
    for _, s in ipairs(stacks) do
      for _, b in ipairs(s.branches or {}) do
        if b.is_current then
          for _, p in ipairs(s.branches) do
            if p.name == b.parent and p.worktree_path and p.worktree_path ~= "" then
              ezstack.goto_worktree(p.worktree_path)
              return
            end
          end
          vim.notify(
            string.format('Parent "%s" is the stack root or has no worktree', b.parent),
            vim.log.levels.INFO
          )
          return
        end
      end
    end
    vim.notify("No current branch found in any stack", vim.log.levels.INFO)
  end, { force = true, all = true })
end

--- `:Ezs down` — navigate to a child branch.
subcommands["down"] = function()
  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("ezstack: " .. err, vim.log.levels.ERROR)
      return
    end
    for _, s in ipairs(stacks) do
      for _, b in ipairs(s.branches or {}) do
        if b.is_current then
          local children = {}
          for _, c in ipairs(s.branches) do
            if c.parent == b.name and c.worktree_path and c.worktree_path ~= "" then
              table.insert(children, c)
            end
          end
          if #children == 0 then
            vim.notify("No children with worktrees", vim.log.levels.INFO)
            return
          end
          if #children == 1 then
            ezstack.goto_worktree(children[1].worktree_path)
            return
          end
          vim.ui.select(
            vim.tbl_map(function(c) return c.name end, children),
            { prompt = "Select child branch:" },
            function(choice)
              if not choice then
                return
              end
              for _, c in ipairs(children) do
                if c.name == choice then
                  ezstack.goto_worktree(c.worktree_path)
                  return
                end
              end
            end
          )
          return
        end
      end
    end
  end, { force = true, all = true })
end

--- `:Ezs log [branch]` — show commits in branch since parent.
subcommands["log"] = function(args)
  cli.log(args[1], function(err, out)
    if err then
      vim.notify("Log failed: " .. err, vim.log.levels.ERROR)
      return
    end
    M._show_scratch("ezstack://log", "git", vim.split(out or "", "\n"))
  end)
end

--- `:Ezs config [show]` — show ezs config.
subcommands["config"] = function(args)
  if args[1] == nil or args[1] == "show" then
    cli.config_show(function(err, out)
      if err then
        vim.notify("Config failed: " .. err, vim.log.levels.ERROR)
        return
      end
      M._show_scratch("ezstack://config", "yaml", vim.split(out or "", "\n"))
    end)
    return
  end
  -- Pass-through for `config set ...`
  local cli_args = { "config" }
  vim.list_extend(cli_args, args)
  cli.run_in_terminal(cli_args)
end

--- `:Ezs menu` — open the interactive ezs menu in a terminal.
subcommands["menu"] = function()
  cli.run_in_terminal({ "menu" })
end

--- `:Ezs agent ...` — agent subcommand.
--- The raw command line is passed in so we can preserve quoted arguments
--- like `:Ezs agent feature "Add login flow"`.
subcommands["agent"] = function(args, raw)
  if args[1] == "prompt" then
    M._agent_prompt(vim.list_slice(args, 2))
    return
  end

  -- For `feature`, recover the raw description string after the keyword
  -- so quoting/spaces are preserved.
  if args[1] == "feature" and raw then
    -- Find "feature" in raw and take everything after it.
    local _, e = raw:find("feature")
    local description = raw:sub((e or 0) + 1):gsub("^%s+", ""):gsub("^[\"']", ""):gsub("[\"']$", "")
    if description == "" then
      vim.ui.input({ prompt = "Feature description: " }, function(input)
        if not input or input == "" then
          return
        end
        cli.run_in_terminal({ "agent", "feature", input })
      end)
      return
    end
    cli.run_in_terminal({ "agent", "feature", description })
    return
  end

  local cli_args = { "agent" }
  vim.list_extend(cli_args, args)
  cli.run_in_terminal(cli_args)
end

-- ── Agent prompt management (3-layer system: shipped/custom/repo) ──

function M._agent_prompt(args)
  local action = args[1]
  local arg2 = args[2]
  local arg3 = args[3]

  if action == "edit" then
    local is_repo = arg2 == "repo"
    local which = is_repo and arg3 or arg2

    local home = vim.env.HOME or ""
    local files = {}
    local base = is_repo and (vim.fn.getcwd() .. "/.ezstack") or (home .. "/.ezstack")

    if which == "feature" then
      files = { base .. "/agent-feature-prompt.md" }
    elseif which == "work" then
      files = { base .. "/agent-work-prompt.md" }
    else
      files = {
        base .. "/agent-work-prompt.md",
        base .. "/agent-feature-prompt.md",
      }
    end

    local missing_types = {}
    for _, f in ipairs(files) do
      if vim.fn.filereadable(f) ~= 1 then
        if f:find("work") then
          missing_types["work"] = true
        elseif f:find("feature") then
          missing_types["feature"] = true
        end
      end
    end

    local function open_files()
      for i, f in ipairs(files) do
        if i == 1 then
          vim.cmd("edit " .. vim.fn.fnameescape(f))
        else
          vim.cmd("vsplit " .. vim.fn.fnameescape(f))
        end
      end
      local label = is_repo and "repo-specific " or "custom "
      vim.notify("Editing " .. label .. "agent prompt" .. (#files > 1 and "s" or ""), vim.log.levels.INFO)
    end

    if next(missing_types) then
      local types_to_reset = vim.tbl_keys(missing_types)
      local idx = 0
      local function reset_next()
        idx = idx + 1
        if idx > #types_to_reset then
          open_files()
          return
        end
        local reset_args = { "agent", "prompt", "--reset" }
        if is_repo then
          table.insert(reset_args, "--repo")
        end
        table.insert(reset_args, types_to_reset[idx])
        cli.exec(reset_args, function(err)
          if err then
            vim.notify("Failed to create prompt files: " .. err, vim.log.levels.ERROR)
            return
          end
          reset_next()
        end)
      end
      reset_next()
    else
      open_files()
    end
    return
  end

  if action == "reset" then
    local is_repo = arg2 == "repo"
    local which = is_repo and arg3 or arg2

    local function do_reset(type_name, callback)
      local reset_args = { "agent", "prompt", "--reset" }
      if is_repo then
        table.insert(reset_args, "--repo")
      end
      table.insert(reset_args, type_name)
      cli.exec(reset_args, callback)
    end

    local label = is_repo and "repo-specific " or "custom "

    if which then
      do_reset(which, function(err)
        if err then
          vim.notify("Failed to reset prompts: " .. err, vim.log.levels.ERROR)
        else
          vim.notify("Reset " .. label .. which .. " agent prompt to default", vim.log.levels.INFO)
        end
      end)
    else
      do_reset("work", function(err1)
        if err1 then
          vim.notify("Failed to reset work prompt: " .. err1, vim.log.levels.ERROR)
          return
        end
        do_reset("feature", function(err2)
          if err2 then
            vim.notify("Failed to reset feature prompt: " .. err2, vim.log.levels.ERROR)
          else
            vim.notify("Reset " .. label .. "work and feature agent prompts to default", vim.log.levels.INFO)
          end
        end)
      end)
    end
    return
  end

  -- View prompts
  local layer = nil
  local which = nil

  if action == "shipped" or action == "custom" or action == "repo" then
    layer = action
    which = arg2
  elseif action == "work" or action == "feature" then
    layer = "shipped"
    which = action
  else
    layer = "shipped"
  end

  local cli_flag = "--" .. layer
  local types_to_fetch = which and { which } or { "work", "feature" }
  local results = {}
  local remaining = #types_to_fetch

  for _, type_name in ipairs(types_to_fetch) do
    cli.exec({ "agent", "prompt", cli_flag, type_name }, function(err, stdout)
      if err then
        results[type_name] = "--- " .. type_name .. " ---\nError: " .. err
      else
        results[type_name] = "--- " .. layer .. " " .. type_name .. " prompt ---\n" .. (stdout or "")
      end
      remaining = remaining - 1
      if remaining == 0 then
        local parts = {}
        for _, tn in ipairs(types_to_fetch) do
          table.insert(parts, results[tn])
        end
        local content = table.concat(parts, "\n\n")
        content = content:gsub("\027%[[0-9;]*m", "")
        local lines = vim.split(content, "\n")
        M._show_scratch("ezstack://agent-prompts", "markdown", lines)
      end
    end)
  end
end

-- ── Scratch buffer helper ──

--- Open a scratch buffer with the given content.
---@param name string Buffer name
---@param ft string File type
---@param lines string[] Lines to display
function M._show_scratch(name, ft, lines)
  local bufnr = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_buf_set_lines(bufnr, 0, -1, false, lines)
  vim.bo[bufnr].modifiable = false
  vim.bo[bufnr].buftype = "nofile"
  vim.bo[bufnr].bufhidden = "wipe"
  vim.bo[bufnr].filetype = ft
  pcall(vim.api.nvim_buf_set_name, bufnr, name)

  vim.cmd("botright split")
  vim.api.nvim_win_set_buf(0, bufnr)

  vim.keymap.set("n", "q", "<cmd>close<cr>", { buffer = bufnr, silent = true, desc = "Close" })
end

-- ── Dispatcher and completion ──

--- Parse a `:Ezs ...` command line into args, preserving the raw remainder.
---@param raw string The text after `:Ezs `
---@return string[] args
local function parse_args(raw)
  local args = {}
  for word in raw:gmatch("%S+") do
    table.insert(args, word)
  end
  return args
end

--- Dispatch a parsed `:Ezs` command. Called by the user-command handler.
---@param opts table The opts table from nvim_create_user_command.
function M.dispatch(opts)
  local raw = opts.args or ""
  local args = parse_args(raw)
  local sub = args[1] or "list"
  local rest = vim.list_slice(args, 2)

  -- Pre-strip the subcommand from raw so callers that need raw_args see
  -- only the remainder. Account for any leading whitespace.
  local raw_rest = raw:gsub("^%s*" .. vim.pesc(sub), "", 1):gsub("^%s+", "")

  local handler = subcommands[sub]
  if handler then
    handler(rest, raw_rest)
  else
    vim.notify("Unknown ezstack command: " .. sub, vim.log.levels.ERROR)
    vim.notify("Available: " .. table.concat(SUBCOMMAND_NAMES, ", "), vim.log.levels.INFO)
  end
end

--- Completion function for the `:Ezs` user command.
---@param arglead string
---@param cmdline string
---@return string[]
function M.complete(arglead, cmdline, _cursorpos)
  local parts = parse_args(cmdline)
  if cmdline:match("%s$") then
    table.insert(parts, "")
  end

  -- Subcommand position
  if #parts <= 2 then
    return vim.tbl_filter(function(s)
      return s:find(arglead, 1, true) == 1
    end, SUBCOMMAND_NAMES)
  end

  -- :Ezs agent <tab>
  if parts[2] == "agent" and #parts <= 3 then
    return vim.tbl_filter(function(s)
      return s:find(arglead, 1, true) == 1
    end, { "feature", "prompt" })
  end

  -- :Ezs agent prompt <tab>
  if parts[2] == "agent" and parts[3] == "prompt" and #parts <= 4 then
    return vim.tbl_filter(function(s)
      return s:find(arglead, 1, true) == 1
    end, { "edit", "reset", "shipped", "custom", "repo", "work", "feature" })
  end

  if parts[2] == "agent" and parts[3] == "prompt" and #parts <= 5 then
    local p4 = parts[4]
    if p4 == "edit" or p4 == "reset" then
      return vim.tbl_filter(function(s)
        return s:find(arglead, 1, true) == 1
      end, { "repo", "work", "feature" })
    elseif p4 == "shipped" or p4 == "custom" or p4 == "repo" then
      return vim.tbl_filter(function(s)
        return s:find(arglead, 1, true) == 1
      end, { "work", "feature" })
    end
  end

  if parts[2] == "agent" and parts[3] == "prompt"
    and (parts[4] == "edit" or parts[4] == "reset")
    and parts[5] == "repo" and #parts <= 6 then
    return vim.tbl_filter(function(s)
      return s:find(arglead, 1, true) == 1
    end, { "work", "feature" })
  end

  -- :Ezs pr <tab>
  if parts[2] == "pr" and #parts <= 3 then
    return vim.tbl_filter(function(s)
      return s:find(arglead, 1, true) == 1
    end, { "create", "update", "merge", "draft", "stack", "open" })
  end

  -- :Ezs sync <tab>
  if parts[2] == "sync" and #parts <= 3 then
    return vim.tbl_filter(function(s)
      return s:find(arglead, 1, true) == 1
    end, { "-s", "-c", "-a", "-p", "-C", "--continue", "--dry-run", "--merge", "--rebase" })
  end

  -- :Ezs push <tab>
  if parts[2] == "push" and #parts <= 3 then
    return vim.tbl_filter(function(s)
      return s:find(arglead, 1, true) == 1
    end, { "-s", "-f" })
  end

  -- :Ezs goto/delete/reparent/log <branch>
  if parts[2] == "goto" or parts[2] == "delete" or parts[2] == "reparent"
    or parts[2] == "log" or parts[2] == "unstack" then
    if #parts <= 3 then
      local stacks = require("ezstack.cli").list_stacks_sync()
      local names = all_branch_names(stacks)
      return vim.tbl_filter(function(s)
        return s:find(arglead, 1, true) == 1
      end, names)
    end
  end

  -- :Ezs config <tab>
  if parts[2] == "config" and #parts <= 3 then
    return vim.tbl_filter(function(s)
      return s:find(arglead, 1, true) == 1
    end, { "show", "set" })
  end

  return {}
end

--- Register the `:Ezs` command. Idempotent — overrides any existing
--- registration so calling setup() multiple times is safe.
function M.register()
  vim.api.nvim_create_user_command("Ezs", function(opts)
    M.dispatch(opts)
  end, {
    nargs = "*",
    desc = "ezstack — manage stacked PRs",
    complete = M.complete,
  })
end

return M
