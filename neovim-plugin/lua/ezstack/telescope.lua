local cli = require("ezstack.cli")
local ui = require("ezstack.ui")
local ezstack = require("ezstack")

local M = {}

--- Check if Telescope is available.
---@return boolean
function M.available()
  local ok = pcall(require, "telescope")
  return ok
end

--- Telescope picker: browse all branches across all stacks.
function M.branches()
  local pickers = require("telescope.pickers")
  local finders = require("telescope.finders")
  local conf = require("telescope.config").values
  local actions = require("telescope.actions")
  local action_state = require("telescope.actions.state")
  local previewers = require("telescope.previewers")

  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("ezstack: " .. err, vim.log.levels.ERROR)
      return
    end
    if #stacks == 0 then
      vim.notify("No stacks found", vim.log.levels.INFO)
      return
    end

    -- Build flat list of entries
    local entries = {}
    for _, stack in ipairs(stacks) do
      for _, branch in ipairs(stack.branches or {}) do
        table.insert(entries, {
          branch = branch,
          stack = stack,
        })
      end
    end

    if #entries == 0 then
      vim.notify("No branches found", vim.log.levels.INFO)
      return
    end

    pickers.new({}, {
      prompt_title = "ezstack branches",
      finder = finders.new_table({
        results = entries,
        entry_maker = function(entry)
          local b = entry.branch
          local s = entry.stack

          -- Build display string
          local parts = { b.name }
          if b.pr_number and b.pr_number > 0 then
            table.insert(parts, string.format("PR #%d", b.pr_number))
            if b.pr_state and b.pr_state ~= "" then
              table.insert(parts, "[" .. b.pr_state .. "]")
            end
          end
          if b.additions or b.deletions then
            local adds = b.additions or 0
            local dels = b.deletions or 0
            table.insert(parts, string.format("+%d -%d", adds, dels))
          end
          local stack_label = s.name and (s.name .. " [" .. s.hash .. "]") or s.hash
          table.insert(parts, "stack: " .. stack_label)

          local display = table.concat(parts, "  ")

          return {
            value = entry,
            display = display,
            ordinal = b.name .. " " .. (s.name or s.hash),
          }
        end,
      }),
      sorter = conf.generic_sorter({}),
      previewer = previewers.new_buffer_previewer({
        title = "Stack Preview",
        define_preview = function(self, entry)
          local lines = ui.render_preview(entry.value.stack, entry.value.branch.name)
          if vim.api.nvim_buf_is_valid(self.state.bufnr) then
            vim.api.nvim_buf_set_lines(self.state.bufnr, 0, -1, false, lines)
          end
        end,
      }),
      attach_mappings = function(prompt_bufnr, map)
        -- <CR> — goto worktree
        actions.select_default:replace(function()
          actions.close(prompt_bufnr)
          local selection = action_state.get_selected_entry()
          if not selection then
            return
          end
          local wt = selection.value.branch.worktree_path
          if wt and wt ~= "" then
            ezstack.goto_worktree(wt)
          else
            vim.notify(
              "No worktree path for " .. selection.value.branch.name,
              vim.log.levels.WARN
            )
          end
        end)

        -- <C-o> — open PR in browser
        map("i", "<C-o>", function()
          local selection = action_state.get_selected_entry()
          if not selection then
            return
          end
          local url = selection.value.branch.pr_url
          if url and url ~= "" then
            vim.ui.open(url)
          else
            vim.notify("No PR URL", vim.log.levels.INFO)
          end
        end)

        -- <C-d> — delete branch
        map("i", "<C-d>", function()
          local selection = action_state.get_selected_entry()
          if not selection then
            return
          end
          actions.close(prompt_bufnr)
          local name = selection.value.branch.name
          vim.ui.select({ "Yes", "No" }, {
            prompt = 'Delete branch "' .. name .. '"?',
          }, function(choice)
            if choice == "Yes" then
              cli.delete_branch(name, function(del_err)
                if del_err then
                  vim.notify("Delete failed: " .. del_err, vim.log.levels.ERROR)
                else
                  vim.notify('Deleted "' .. name .. '"', vim.log.levels.INFO)
                end
              end)
            end
          end)
        end)

        -- <C-p> — push branch
        map("i", "<C-p>", function()
          local selection = action_state.get_selected_entry()
          if not selection then
            return
          end
          vim.notify("Pushing...", vim.log.levels.INFO)
          -- cli.push signature is (opts, callback) — pass an empty opts
          -- table so the callback isn't bound to opts and silently dropped.
          cli.push({}, function(push_err)
            if push_err then
              vim.notify("Push failed: " .. push_err, vim.log.levels.ERROR)
            else
              vim.notify("Pushed", vim.log.levels.INFO)
            end
          end)
        end)

        return true
      end,
    }):find()
  end, { force = true, all = true })
end

--- Telescope picker: browse stacks.
function M.stacks()
  local pickers = require("telescope.pickers")
  local finders = require("telescope.finders")
  local conf = require("telescope.config").values
  local actions = require("telescope.actions")
  local action_state = require("telescope.actions.state")
  local previewers = require("telescope.previewers")

  cli.list_stacks(function(err, stacks)
    if err then
      vim.notify("ezstack: " .. err, vim.log.levels.ERROR)
      return
    end
    if #stacks == 0 then
      vim.notify("No stacks found", vim.log.levels.INFO)
      return
    end

    pickers.new({}, {
      prompt_title = "ezstack stacks",
      finder = finders.new_table({
        results = stacks,
        entry_maker = function(stack)
          local label = stack.name and (stack.name .. " [" .. stack.hash .. "]") or stack.hash
          local n = #(stack.branches or {})
          local display = string.format(
            "%s  root: %s  (%d branch%s)",
            label, stack.root, n, n == 1 and "" or "es"
          )

          return {
            value = stack,
            display = display,
            ordinal = (stack.name or "") .. " " .. stack.hash,
          }
        end,
      }),
      sorter = conf.generic_sorter({}),
      previewer = previewers.new_buffer_previewer({
        title = "Stack Preview",
        define_preview = function(self, entry)
          local lines = ui.render_preview(entry.value)
          if vim.api.nvim_buf_is_valid(self.state.bufnr) then
            vim.api.nvim_buf_set_lines(self.state.bufnr, 0, -1, false, lines)
          end
        end,
      }),
      attach_mappings = function(prompt_bufnr, map)
        -- <CR> — open stack viewer
        actions.select_default:replace(function()
          actions.close(prompt_bufnr)
          ui.open(false)
        end)

        -- <C-r> — rename stack
        map("i", "<C-r>", function()
          local selection = action_state.get_selected_entry()
          if not selection then
            return
          end
          actions.close(prompt_bufnr)
          local stack = selection.value
          vim.ui.input({
            prompt = "New name (empty to clear): ",
            default = stack.name or "",
          }, function(name)
            if name == nil then
              return
            end
            cli.rename_stack(stack.hash, name, function(rename_err)
              if rename_err then
                vim.notify("Rename failed: " .. rename_err, vim.log.levels.ERROR)
              else
                local msg = name ~= "" and ('Renamed to "' .. name .. '"') or "Cleared name"
                vim.notify(msg, vim.log.levels.INFO)
              end
            end)
          end)
        end)

        return true
      end,
    }):find()
  end, { force = true, all = true })
end

return M
