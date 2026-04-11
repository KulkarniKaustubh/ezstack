local cli = require("ezstack.cli")
local ui = require("ezstack.ui")
local ezstack = require("ezstack")

local M = {}

--- Subcommand dispatch table.
---@type table<string, fun(args: string[])>
local subcommands = {}

--- `:Ezs agent [options]` — launch AI agent with stack context
--- `:Ezs agent feature "description"` — launch agent to build a feature
--- `:Ezs agent prompt [shipped|custom|repo] [work|feature]` — view agent prompts (3 layers)
--- `:Ezs agent prompt edit [repo] [work|feature]` — edit custom (or repo) instructions
--- `:Ezs agent prompt reset [repo] [work|feature]` — reset custom (or repo) instructions
subcommands["agent"] = function(args)
  -- Handle "prompt" subcommand natively for better Neovim integration
  if args[1] == "prompt" then
    M._agent_prompt(vim.list_slice(args, 2))
    return
  end

  local cli_args = { "agent" }
  for _, a in ipairs(args) do
    table.insert(cli_args, a)
  end
  cli.run_in_terminal(cli_args)
end

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
    cli.new_branch(name, parent, function(err)
      if err then
        vim.notify("Failed to create branch: " .. err, vim.log.levels.ERROR)
      else
        vim.notify('Created branch "' .. name .. '"', vim.log.levels.INFO)
      end
    end)
  else
    M._new_with_parent_pick(name)
  end
end

--- Helper: create branch after picking parent.
---@param name string
function M._new_with_parent_pick(name)
  cli.list_stacks(function(err, stacks)
    if err then
      -- No stacks yet, create without parent
      cli.new_branch(name, nil, function(create_err)
        if create_err then
          vim.notify("Failed to create branch: " .. create_err, vim.log.levels.ERROR)
        else
          vim.notify('Created branch "' .. name .. '"', vim.log.levels.INFO)
        end
      end)
      return
    end

    local candidates = {}
    local seen = {}
    for _, s in ipairs(stacks) do
      if not seen[s.root] then
        seen[s.root] = true
        table.insert(candidates, s.root)
      end
      for _, b in ipairs(s.branches or {}) do
        if not seen[b.name] then
          seen[b.name] = true
          table.insert(candidates, b.name)
        end
      end
    end

    if #candidates == 0 then
      cli.new_branch(name, nil, function(create_err)
        if create_err then
          vim.notify("Failed to create branch: " .. create_err, vim.log.levels.ERROR)
        else
          vim.notify('Created branch "' .. name .. '"', vim.log.levels.INFO)
        end
      end)
      return
    end

    vim.ui.select(candidates, { prompt = "Parent branch:" }, function(parent)
      if not parent then
        return
      end
      cli.new_branch(name, parent, function(create_err)
        if create_err then
          vim.notify("Failed to create branch: " .. create_err, vim.log.levels.ERROR)
        else
          vim.notify('Created branch "' .. name .. '"', vim.log.levels.INFO)
        end
      end)
    end)
  end, { force = true })
end

--- `:Ezs sync`
subcommands["sync"] = function()
  cli.run_in_terminal({ "sync", "-s" })
end

--- `:Ezs push [-s]`
subcommands["push"] = function(args)
  if args[1] == "-s" then
    vim.notify("Pushing stack...", vim.log.levels.INFO)
    cli.push_stack(function(err)
      if err then
        vim.notify("Push failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("Stack pushed", vim.log.levels.INFO)
      end
    end)
  else
    vim.notify("Pushing branch...", vim.log.levels.INFO)
    cli.push(function(err)
      if err then
        vim.notify("Push failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("Branch pushed", vim.log.levels.INFO)
      end
    end)
  end
end

--- `:Ezs pr <subcommand> [args]`
subcommands["pr"] = function(args)
  local sub = args[1]
  local rest = {}
  for i = 2, #args do
    table.insert(rest, args[i])
  end

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
          cli.pr_create(input, { draft = choice == "Draft" }, function(err)
            if err then
              vim.notify("PR create failed: " .. err, vim.log.levels.ERROR)
            else
              vim.notify("PR created", vim.log.levels.INFO)
            end
          end)
        end)
      end)
      return
    end
    cli.pr_create(title, {}, function(err)
      if err then
        vim.notify("PR create failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("PR created", vim.log.levels.INFO)
      end
    end)
  elseif sub == "update" then
    vim.notify("Updating PR...", vim.log.levels.INFO)
    cli.pr_update(rest[1], function(err)
      if err then
        vim.notify("PR update failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("PR updated", vim.log.levels.INFO)
      end
    end)
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
      cli.pr_merge(method_map[choice], rest[1], function(err)
        if err then
          vim.notify("PR merge failed: " .. err, vim.log.levels.ERROR)
        else
          vim.notify("PR merged", vim.log.levels.INFO)
        end
      end)
    end)
  elseif sub == "draft" then
    vim.notify("Toggling draft...", vim.log.levels.INFO)
    cli.pr_draft(rest[1], function(err)
      if err then
        vim.notify("PR draft toggle failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("Draft status toggled", vim.log.levels.INFO)
      end
    end)
  elseif sub == "stack" then
    vim.notify("Updating stack info in PRs...", vim.log.levels.INFO)
    cli.pr_stack(function(err)
      if err then
        vim.notify("PR stack update failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify("Stack info updated in PRs", vim.log.levels.INFO)
      end
    end)
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

--- Confirm and delete a branch.
---@param name string
function M._confirm_delete(name)
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
      end
    end)
  end)
end

--- `:Ezs reparent <branch> <parent>`
subcommands["reparent"] = function(args)
  local branch_name = args[1]
  local new_parent = args[2]

  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("Failed to list stacks: " .. err, vim.log.levels.ERROR)
      return
    end

    -- Collect all branch names
    local all_branches = {}
    for _, s in ipairs(stacks) do
      table.insert(all_branches, s.root)
      for _, b in ipairs(s.branches or {}) do
        table.insert(all_branches, b.name)
      end
    end

    local function do_reparent(bn, np)
      cli.reparent(bn, np, function(rp_err)
        if rp_err then
          vim.notify("Reparent failed: " .. rp_err, vim.log.levels.ERROR)
        else
          vim.notify(string.format('Reparented "%s" onto "%s"', bn, np), vim.log.levels.INFO)
        end
      end)
    end

    if branch_name and new_parent then
      do_reparent(branch_name, new_parent)
      return
    end

    -- Interactive: pick branch first
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
  end, { force = true })
end

--- `:Ezs rename [hash] [name]`
subcommands["rename"] = function(args)
  local hash = args[1]
  local name = args[2]

  if hash and name then
    cli.rename_stack(hash, name, function(err)
      if err then
        vim.notify("Rename failed: " .. err, vim.log.levels.ERROR)
      else
        vim.notify('Renamed stack to "' .. name .. '"', vim.log.levels.INFO)
      end
    end)
    return
  end

  -- Interactive: pick stack
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

    if not hash then
      vim.ui.select(
        vim.tbl_map(function(item) return item.label end, items),
        { prompt = "Stack to rename:" },
        function(_, idx)
          if not idx then
            return
          end
          local selected = items[idx]
          vim.ui.input({
            prompt = "New name (empty to clear): ",
            default = selected.name or "",
          }, function(new_name)
            if new_name == nil then
              return
            end
            cli.rename_stack(selected.hash, new_name, function(rename_err)
              if rename_err then
                vim.notify("Rename failed: " .. rename_err, vim.log.levels.ERROR)
              else
                local msg = new_name ~= "" and ('Renamed stack to "' .. new_name .. '"') or "Cleared stack name"
                vim.notify(msg, vim.log.levels.INFO)
              end
            end)
          end)
        end
      )
    else
      -- Hash provided, prompt for name
      local current_name = ""
      for _, s in ipairs(stacks) do
        if s.hash == hash or s.hash:sub(1, #hash) == hash then
          current_name = s.name or ""
          hash = s.hash -- resolve prefix
          break
        end
      end
      vim.ui.input({
        prompt = "New name (empty to clear): ",
        default = current_name,
      }, function(new_name)
        if new_name == nil then
          return
        end
        cli.rename_stack(hash, new_name, function(rename_err)
          if rename_err then
            vim.notify("Rename failed: " .. rename_err, vim.log.levels.ERROR)
          else
            local msg = new_name ~= "" and ('Renamed stack to "' .. new_name .. '"') or "Cleared stack name"
            vim.notify(msg, vim.log.levels.INFO)
          end
        end)
      end)
    end
  end, { force = true })
end

--- `:Ezs goto [branch]`
subcommands["goto"] = function(args)
  local target = args[1]

  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("Failed to list stacks: " .. err, vim.log.levels.ERROR)
      return
    end

    -- Build branch -> worktree_path map
    local branch_map = {}
    local branch_names = {}
    for _, s in ipairs(stacks) do
      for _, b in ipairs(s.branches or {}) do
        if b.worktree_path and b.worktree_path ~= "" then
          branch_map[b.name] = b.worktree_path
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

    -- Try Telescope first, fall back to vim.ui.select
    local ok, telescope = pcall(require, "ezstack.telescope")
    if ok and telescope.available() then
      telescope.branches()
    else
      vim.ui.select(branch_names, { prompt = "Go to branch:" }, function(choice)
        if not choice then
          return
        end
        -- Strip " (current)" suffix if present
        local name = choice:gsub(" %(current%)$", "")
        local wt = branch_map[name]
        if wt then
          ezstack.goto_worktree(wt)
        end
      end)
    end
  end, { force = true })
end

--- `:Ezs diff [-- git-diff-options]` — show diff against parent
subcommands["diff"] = function(args)
  local cli_args = { "diff" }
  for _, a in ipairs(args) do
    table.insert(cli_args, a)
  end
  cli.run_in_terminal(cli_args)
end

--- `:Ezs commit [args]` — commit and auto-sync children
subcommands["commit"] = function(args)
  local cli_args = { "commit" }
  for _, a in ipairs(args) do
    table.insert(cli_args, a)
  end
  cli.run_in_terminal(cli_args)
end

--- `:Ezs amend [args]` — amend last commit and auto-sync children
subcommands["amend"] = function(args)
  local cli_args = { "amend" }
  for _, a in ipairs(args) do
    table.insert(cli_args, a)
  end
  cli.run_in_terminal(cli_args)
end

--- `:Ezs stack [branch] [parent]` — add branch to stack
subcommands["stack"] = function(args)
  -- "stack rename" is handled separately by "rename" subcommand
  if args[1] == "rename" then
    table.remove(args, 1)
    subcommands["rename"](args)
    return
  end
  local cli_args = { "stack" }
  for _, a in ipairs(args) do
    table.insert(cli_args, a)
  end
  cli.run_in_terminal(cli_args)
end

--- `:Ezs unstack [branch]` — remove branch from tracking
subcommands["unstack"] = function(args)
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
        vim.notify("No branches to unstack", vim.log.levels.INFO)
        return
      end
      vim.ui.select(candidates, { prompt = "Branch to unstack:" }, function(choice)
        if not choice then
          return
        end
        cli.exec_yes({ "unstack", choice }, function(unstack_err)
          if unstack_err then
            vim.notify("Unstack failed: " .. unstack_err, vim.log.levels.ERROR)
          else
            vim.notify('Unstacked "' .. choice .. '"', vim.log.levels.INFO)
            cli.invalidate_cache()
          end
        end)
      end)
    end, { force = true })
    return
  end
  cli.exec_yes({ "unstack", branch }, function(err)
    if err then
      vim.notify("Unstack failed: " .. err, vim.log.levels.ERROR)
    else
      vim.notify('Unstacked "' .. branch .. '"', vim.log.levels.INFO)
      cli.invalidate_cache()
    end
  end)
end

--- Agent prompt management — native Neovim integration.
--- Supports: view (default), edit, reset.
--- v3.1.0: 3-layer prompt system (shipped, custom, repo).
--- `work` or `feature` is a required positional arg for CLI calls; no "both" option.
---@param args string[] Remaining args after "prompt"
function M._agent_prompt(args)
  -- Parse args: action may be a layer (shipped/custom/repo), an action (edit/reset),
  -- or a type (work/feature) for the default shipped view.
  local action = args[1] -- nil, "edit", "reset", "shipped", "custom", "repo", "work", "feature"
  local arg2 = args[2]   -- varies by action
  local arg3 = args[3]   -- varies by action

  if action == "edit" then
    -- :Ezs agent prompt edit [repo] [work|feature]
    local is_repo = arg2 == "repo"
    local which = is_repo and arg3 or arg2  -- "work", "feature", or nil (both)

    local home = vim.env.HOME or ""
    local files = {}

    if is_repo then
      -- Repo-specific instructions live in <repo>/.ezstack/
      local repo_dir = vim.fn.getcwd() .. "/.ezstack"
      if which == "feature" then
        files = { repo_dir .. "/agent-feature-prompt.md" }
      elseif which == "work" then
        files = { repo_dir .. "/agent-work-prompt.md" }
      else
        files = {
          repo_dir .. "/agent-work-prompt.md",
          repo_dir .. "/agent-feature-prompt.md",
        }
      end
    else
      -- Custom instructions live in ~/.ezstack/
      local config_dir = home .. "/.ezstack"
      if which == "feature" then
        files = { config_dir .. "/agent-feature-prompt.md" }
      elseif which == "work" then
        files = { config_dir .. "/agent-work-prompt.md" }
      else
        files = {
          config_dir .. "/agent-work-prompt.md",
          config_dir .. "/agent-feature-prompt.md",
        }
      end
    end

    -- Ensure files exist by running reset for any missing ones
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
      -- Create missing files by resetting them to defaults (one type at a time)
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
    -- :Ezs agent prompt reset [repo] [work|feature]
    local is_repo = arg2 == "repo"
    local which = is_repo and arg3 or arg2  -- "work", "feature", or nil (both)

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
      -- Reset just the specified type
      do_reset(which, function(err)
        if err then
          vim.notify("Failed to reset prompts: " .. err, vim.log.levels.ERROR)
        else
          vim.notify("Reset " .. label .. which .. " agent prompt to default", vim.log.levels.INFO)
        end
      end)
    else
      -- Reset both individually
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

  -- View prompts in a scratch buffer
  -- Determine layer and type from args:
  --   :Ezs agent prompt                     → shipped work + feature
  --   :Ezs agent prompt work                → shipped work
  --   :Ezs agent prompt feature             → shipped feature
  --   :Ezs agent prompt shipped [work|feat] → shipped prompt(s)
  --   :Ezs agent prompt custom [work|feat]  → custom instructions
  --   :Ezs agent prompt repo [work|feat]    → repo-specific instructions
  local layer = nil
  local which = nil

  if action == "shipped" or action == "custom" or action == "repo" then
    layer = action
    which = arg2  -- "work", "feature", or nil
  elseif action == "work" or action == "feature" then
    layer = "shipped"
    which = action
  else
    -- bare `:Ezs agent prompt` → show shipped for both
    layer = "shipped"
  end

  local cli_flag = "--" .. layer  -- --shipped, --custom, or --repo
  local types_to_fetch = which and { which } or { "work", "feature" }
  local results = {}
  local remaining = #types_to_fetch

  for _, type_name in ipairs(types_to_fetch) do
    cli.exec({ "agent", "prompt", cli_flag, type_name }, function(err, stdout)
      if err then
        results[type_name] = "--- " .. type_name .. " ---\nError: " .. err
      else
        results[type_name] = "--- " .. layer .. " " .. type_name .. " prompt ---\n" .. stdout
      end
      remaining = remaining - 1
      if remaining == 0 then
        -- All fetched, assemble and display
        local parts = {}
        for _, tn in ipairs(types_to_fetch) do
          table.insert(parts, results[tn])
        end
        local content = table.concat(parts, "\n\n")
        -- Strip ANSI escape codes for clean display
        content = content:gsub("\027%[[0-9;]*m", "")
        local lines = vim.split(content, "\n")

        -- Create scratch buffer
        local bufnr = vim.api.nvim_create_buf(false, true)
        vim.api.nvim_buf_set_lines(bufnr, 0, -1, false, lines)
        vim.bo[bufnr].modifiable = false
        vim.bo[bufnr].buftype = "nofile"
        vim.bo[bufnr].bufhidden = "wipe"
        vim.bo[bufnr].filetype = "markdown"
        vim.api.nvim_buf_set_name(bufnr, "ezstack://agent-prompts")

        -- Open in a split
        vim.cmd("botright split")
        vim.api.nvim_win_set_buf(0, bufnr)

        -- Close with q
        vim.keymap.set("n", "q", "<cmd>close<cr>", { buffer = bufnr, silent = true })
      end
    end)
  end
end

--- `:Ezs up` — navigate to parent branch
subcommands["up"] = function()
  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("ezstack: " .. err, vim.log.levels.ERROR)
      return
    end
    for _, s in ipairs(stacks) do
      for _, b in ipairs(s.branches or {}) do
        if b.is_current then
          -- Find parent in branches
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
  end, { force = true })
end

--- `:Ezs down` — navigate to child branch
subcommands["down"] = function()
  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("ezstack: " .. err, vim.log.levels.ERROR)
      return
    end
    for _, s in ipairs(stacks) do
      for _, b in ipairs(s.branches or {}) do
        if b.is_current then
          -- Find children
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
  end, { force = true })
end

--- Register the `:Ezs` command.
function M.register()
  vim.api.nvim_create_user_command("Ezs", function(opts)
    local args = {}
    for word in opts.args:gmatch("%S+") do
      table.insert(args, word)
    end

    local sub = args[1] or "list"
    table.remove(args, 1)

    local handler = subcommands[sub]
    if handler then
      handler(args)
    else
      vim.notify("Unknown ezstack command: " .. sub, vim.log.levels.ERROR)
      vim.notify(
        "Available: agent, list, status, new, sync, push, pr, delete, reparent, rename, goto, up, down, diff, commit, amend, stack, unstack",
        vim.log.levels.INFO
      )
    end
  end, {
    nargs = "*",
    desc = "ezstack — manage stacked PRs",
    complete = function(arglead, cmdline)
      local parts = {}
      for word in cmdline:gmatch("%S+") do
        table.insert(parts, word)
      end
      -- Account for trailing space (user is starting a new word)
      if cmdline:match("%s$") then
        table.insert(parts, "")
      end

      -- Complete subcommand (":Ezs <tab>" or ":Ezs li<tab>")
      if #parts <= 2 then
        local subs = {
          "agent", "list", "status", "new", "sync", "push", "pr",
          "delete", "reparent", "rename", "goto", "up", "down",
          "diff", "commit", "amend", "stack", "unstack",
        }
        return vim.tbl_filter(function(s)
          return s:find(arglead, 1, true) == 1
        end, subs)
      end

      -- Complete agent subcommands (":Ezs agent <tab>")
      if parts[2] == "agent" and #parts <= 3 then
        local agent_subs = { "feature", "prompt" }
        return vim.tbl_filter(function(s)
          return s:find(arglead, 1, true) == 1
        end, agent_subs)
      end

      -- Complete agent prompt subcommands (":Ezs agent prompt <tab>")
      if parts[2] == "agent" and parts[3] == "prompt" and #parts <= 4 then
        local prompt_subs = { "edit", "reset", "shipped", "custom", "repo", "work", "feature" }
        return vim.tbl_filter(function(s)
          return s:find(arglead, 1, true) == 1
        end, prompt_subs)
      end

      -- Complete agent prompt action args (":Ezs agent prompt edit <tab>")
      if parts[2] == "agent" and parts[3] == "prompt" and #parts <= 5 then
        local p4 = parts[4]
        if p4 == "edit" or p4 == "reset" then
          local targets = { "repo", "work", "feature" }
          return vim.tbl_filter(function(s)
            return s:find(arglead, 1, true) == 1
          end, targets)
        elseif p4 == "shipped" or p4 == "custom" or p4 == "repo" then
          local targets = { "work", "feature" }
          return vim.tbl_filter(function(s)
            return s:find(arglead, 1, true) == 1
          end, targets)
        end
      end

      -- Complete agent prompt edit/reset repo type (":Ezs agent prompt edit repo <tab>")
      if parts[2] == "agent" and parts[3] == "prompt"
        and (parts[4] == "edit" or parts[4] == "reset")
        and parts[5] == "repo" and #parts <= 6 then
        local targets = { "work", "feature" }
        return vim.tbl_filter(function(s)
          return s:find(arglead, 1, true) == 1
        end, targets)
      end

      -- Complete pr subcommands (":Ezs pr <tab>" or ":Ezs pr cr<tab>")
      if parts[2] == "pr" and #parts <= 3 then
        local pr_subs = { "create", "update", "merge", "draft", "stack" }
        return vim.tbl_filter(function(s)
          return s:find(arglead, 1, true) == 1
        end, pr_subs)
      end

      return {}
    end,
  })
end

return M
