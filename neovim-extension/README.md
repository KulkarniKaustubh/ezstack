# ezstack.nvim

Neovim plugin for [ezstack](https://github.com/KulkarniKaustubh/ezstack) — manage stacked PRs with git worktrees.

## Requirements

- Neovim 0.10+ (for `vim.system`)
- [ezs](https://github.com/KulkarniKaustubh/ezstack) CLI installed
- Optional: [telescope.nvim](https://github.com/nvim-telescope/telescope.nvim) for fuzzy pickers
- Optional: [vim-fugitive](https://github.com/tpope/vim-fugitive) for auto-refresh on git operations

## Installation

### lazy.nvim

```lua
{
  "KulkarniKaustubh/ezstack",
  subdir = "neovim-extension",
  config = function()
    require("ezstack").setup()
  end,
}
```

### packer.nvim

```lua
use {
  "KulkarniKaustubh/ezstack",
  rtp = "neovim-extension",
  config = function()
    require("ezstack").setup()
  end,
}
```

## Configuration

```lua
require("ezstack").setup({
  cli_path = "ezs",            -- path to ezs binary
  auto_refresh = true,          -- refresh on FugitiveChanged/TermClose
  viewer_position = "botright", -- split position for viewer
  viewer_height = 15,           -- viewer window height
  statusline_cache_ttl = 5000,  -- statusline cache TTL in milliseconds
  goto_strategy = "tcd",        -- "tcd" (tab-local), "cd" (global), "lcd" (window-local)
  goto_close_buffers = false,   -- close unmodified buffers from old worktree on goto
  goto_open_explorer = true,    -- open file explorer at new worktree root
})
```

## Commands

All commands are under `:Ezs`:

| Command | Description |
|---------|-------------|
| `:Ezs` / `:Ezs list` | Open the stack viewer |
| `:Ezs status` | Stack viewer with PR/CI info |
| `:Ezs new <name> [parent]` | Create a new branch |
| `:Ezs sync` | Sync stack (opens terminal for conflict resolution) |
| `:Ezs push` | Push current branch |
| `:Ezs push -s` | Push entire stack |
| `:Ezs pr create [title]` | Create a pull request |
| `:Ezs pr update` | Update PR description |
| `:Ezs pr merge` | Merge PR (prompts for method) |
| `:Ezs pr draft` | Toggle PR draft status |
| `:Ezs pr stack` | Update stack info in all PRs |
| `:Ezs delete [branch]` | Delete a branch and worktree |
| `:Ezs reparent [branch] [parent]` | Change branch parent |
| `:Ezs rename [hash] [name]` | Name or rename a stack |
| `:Ezs goto [branch]` | Switch to a branch worktree |
| `:Ezs up` | Navigate to parent branch |
| `:Ezs down` | Navigate to child branch |

## Stack Viewer

The stack viewer (`:Ezs`) shows all stacks in a styled buffer:

```
 Stack: my-feature [a1b2c3d]                          root: main
 -----------------------------------------------------------------
   > |-- feature-1     PR #100 [OPEN]    CI: 3/3     (-> main)
     |-- feature-2     PR #101 [DRAFT]   CI: pending (-> feature-1)
     '-- feature-3     [no PR]                        (-> feature-1)
```

### Viewer Keymaps

| Key | Action |
|-----|--------|
| `<CR>` | Go to worktree |
| `o` | Open PR in browser |
| `r` | Refresh |
| `R` | Rename stack |
| `n` | New branch |
| `d` | Delete branch |
| `p` | Push branch |
| `P` | Push stack |
| `s` | Sync (terminal) |
| `q` | Close viewer |

## Telescope Integration

If [telescope.nvim](https://github.com/nvim-telescope/telescope.nvim) is installed:

```vim
:Telescope ezstack branches    " Browse and switch branches
:Telescope ezstack stacks      " Browse and rename stacks
```

### Telescope Keymaps

**Branches picker:**
- `<CR>` — go to worktree
- `<C-o>` — open PR in browser
- `<C-d>` — delete branch
- `<C-p>` — push branch

**Stacks picker:**
- `<CR>` — open stack viewer
- `<C-r>` — rename stack

When Telescope is not installed, `:Ezs goto` falls back to `vim.ui.select`.

## Worktree Navigation

`:Ezs goto` switches your Neovim working directory to the selected branch's worktree:

- Uses `:tcd` by default (tab-local — each tab can be a different worktree)
- Fires `User EzstackGoto` autocommand for custom hooks
- Integrates with nvim-tree, neo-tree, and oil file explorers

## Statusline

Add to your statusline (works with lualine, heirline, etc.):

```lua
-- lualine example
require("lualine").setup({
  sections = {
    lualine_b = {
      "branch",
      { function() return require("ezstack").statusline() end },
    },
  },
})
```

Returns a string like `" feature-1 | my-feature [a1b2c3d]"` or `""`.

## Fugitive Integration

When [vim-fugitive](https://github.com/tpope/vim-fugitive) is installed and `auto_refresh = true`, the stack viewer automatically refreshes after fugitive operations (commits, checkouts, rebases).

## Autocommands

| Event | Pattern | Description |
|-------|---------|-------------|
| `User` | `EzstackGoto` | Fired after switching worktrees via `:Ezs goto` |
| `User` | `EzstackChanged` | Fired after CLI mutations (sync, delete, etc.) |
