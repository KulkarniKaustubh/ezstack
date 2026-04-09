" ezstack.nvim — Manage stacked PRs with git worktrees
" Requires Neovim 0.10+ (for vim.system)

if exists('g:loaded_ezstack')
  finish
endif
let g:loaded_ezstack = 1

" Lazy setup: if the user hasn't called setup() by the time
" a git repo is detected, auto-setup with defaults.
augroup ezstack_auto
  autocmd!
  autocmd VimEnter * ++once lua require("ezstack").auto_setup()
augroup END
