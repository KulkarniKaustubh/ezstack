local cli = require("ezstack.cli")

local M = {}

--- Setup fugitive integration autocommands.
function M.setup()
  local group = vim.api.nvim_create_augroup("ezstack_fugitive", { clear = true })

  -- Refresh ezstack state when fugitive fires its changed event
  -- (after commits, checkouts, rebases, etc.)
  vim.api.nvim_create_autocmd("User", {
    group = group,
    pattern = "FugitiveChanged",
    callback = function()
      cli.invalidate_cache()
      -- Refresh any open viewer buffers, preserving status mode
      vim.defer_fn(function()
        local ui = require("ezstack.ui")
        local bufnr = ui._find_viewer_buf()
        if bufnr then
          local use_status = ui.get_viewer_use_status(bufnr)
          ui.open(use_status)
        end
      end, 500)
    end,
    desc = "ezstack: refresh on fugitive changes",
  })

  -- Also refresh when terminals close (after interactive sync, etc.)
  vim.api.nvim_create_autocmd("User", {
    group = group,
    pattern = "EzstackChanged",
    callback = function()
      local ui = require("ezstack.ui")
      local bufnr = ui._find_viewer_buf()
      if bufnr then
        local use_status = ui.get_viewer_use_status(bufnr)
        ui.open(use_status)
      end
    end,
    desc = "ezstack: refresh viewer on ezstack changes",
  })
end

return M
