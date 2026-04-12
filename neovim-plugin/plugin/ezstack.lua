-- ezstack.nvim plugin entry point.
--
-- This file is auto-sourced by Neovim from `plugin/` directories on the
-- runtimepath. It registers the `:Ezs` user command up-front so the plugin
-- works even when the user has not explicitly called `require("ezstack").setup()`.
-- The actual handler lazy-loads the implementation on first use, so adding
-- the plugin to the rtp incurs no startup cost.

if vim.g.loaded_ezstack == 1 then
  return
end
vim.g.loaded_ezstack = 1

-- Minimum supported Neovim version: 0.10 (vim.uv, vim.system, vim.json).
if vim.fn.has("nvim-0.10") == 0 then
  vim.api.nvim_echo(
    { { "ezstack.nvim requires Neovim 0.10 or newer.", "WarningMsg" } },
    true,
    {}
  )
  return
end

-- Lazy entry: forwards to the real command implementation, performing
-- a one-time setup the first time `:Ezs` is invoked.
vim.api.nvim_create_user_command("Ezs", function(opts)
  local ezstack = require("ezstack")
  if not ezstack._setup_done() then
    ezstack.setup()
  end
  require("ezstack.commands").dispatch(opts)
end, {
  nargs = "*",
  desc = "ezstack — manage stacked PRs",
  complete = function(arglead, cmdline, cursorpos)
    -- Lazy-load just enough to provide completions.
    require("ezstack")
    return require("ezstack.commands").complete(arglead, cmdline, cursorpos)
  end,
})

-- Register the syntax/highlight defaults early so colorschemes loaded
-- after this file still see them via the EzstackHighlights autocmd below.
vim.api.nvim_create_autocmd("ColorScheme", {
  group = vim.api.nvim_create_augroup("ezstack_colors", { clear = true }),
  callback = function()
    -- Re-apply default highlights when the colorscheme changes; the user
    -- can override any of them in their colorscheme file before us.
    pcall(function()
      require("ezstack")._setup_highlights()
    end)
  end,
  desc = "ezstack: re-link highlights after colorscheme change",
})
