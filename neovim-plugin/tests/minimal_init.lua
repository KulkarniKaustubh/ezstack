-- Minimal init for headless `plenary.nvim` test runs.
--
-- Usage (from repo root):
--   nvim --headless --noplugin -u neovim-plugin/tests/minimal_init.lua \
--        -c "PlenaryBustedDirectory neovim-plugin/tests/ {minimal_init = 'neovim-plugin/tests/minimal_init.lua'}"
--
-- The harness expects `plenary.nvim` to be on runtimepath (either installed
-- globally for the user running tests or cloned into ./vendor/plenary.nvim).
-- Tests that need to avoid touching `stdpath('state')` set it to a temp dir
-- in their `before_each`.

local cwd = vim.fn.getcwd()
vim.opt.rtp:prepend(cwd .. "/neovim-plugin")

-- Make `require("tests.helpers")` work regardless of where nvim was
-- launched from.
package.path = cwd .. "/neovim-plugin/?.lua;" .. package.path

-- Locate `plenary.nvim`. Probed, in order:
--   1. $EZSTACK_PLENARY — explicit override
--   2. ./vendor/plenary.nvim — repo-local checkout
--   3. common package-manager install paths under $HOME
local function find_plenary()
  local candidates = {}
  if vim.env.EZSTACK_PLENARY and vim.env.EZSTACK_PLENARY ~= "" then
    table.insert(candidates, vim.env.EZSTACK_PLENARY)
  end
  table.insert(candidates, cwd .. "/vendor/plenary.nvim")
  local home = vim.env.HOME or ""
  if home ~= "" then
    table.insert(candidates, home .. "/.local/share/nvim/lazy/plenary.nvim")
    table.insert(candidates, home .. "/.local/share/nvim/site/pack/packer/start/plenary.nvim")
    table.insert(candidates, home .. "/.vim/plugged/plenary.nvim")
  end
  for _, p in ipairs(candidates) do
    if vim.fn.isdirectory(p) == 1 then
      return p
    end
  end
  return nil
end

local plenary_path = find_plenary()
if plenary_path then
  vim.opt.rtp:prepend(plenary_path)
else
  io.stderr:write(
    "[ezstack tests] plenary.nvim not found. Set $EZSTACK_PLENARY or clone it into vendor/plenary.nvim.\n"
  )
end

-- Don't let tests write to the user's real state dir by accident.
local tmp_state = vim.fn.tempname() .. "-ezstack-state"
vim.fn.mkdir(tmp_state, "p")
vim.env.XDG_STATE_HOME = tmp_state

if plenary_path then
  vim.cmd("runtime plugin/plenary.vim")
end
