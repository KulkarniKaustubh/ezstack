# ezstack.nvim

Neovim plugin for [ezstack](https://github.com/KulkarniKaustubh/ezstack) — manage stacked PRs with git worktrees, all without leaving your editor.

## Requirements

- Neovim **0.10+** (uses `vim.system`, `vim.uv`, `vim.json`)
- The [`ezs`](https://github.com/KulkarniKaustubh/ezstack) CLI on `$PATH` (the plugin also probes `~/.local/bin`, `~/go/bin`, `$GOBIN`, `$GOPATH/bin`, `/usr/local/bin`, `/opt/homebrew/bin`)
- Optional: [telescope.nvim](https://github.com/nvim-telescope/telescope.nvim) for fuzzy pickers
- Optional: [vim-fugitive](https://github.com/tpope/vim-fugitive) for auto-refresh on git operations

## Installation

`:Ezs` is registered immediately when the plugin is on the runtimepath, so the plugin works without an explicit `setup()` call. Calling `setup()` is recommended to override defaults.

### lazy.nvim

```lua
{
  "KulkarniKaustubh/ezstack",
  subdir = "neovim-plugin",
  cmd = { "Ezs" },                  -- lazy-load on first :Ezs
  keys = { { "<leader>ez", "<cmd>Ezs<cr>", desc = "Ezstack viewer" } },
  config = function()
    require("ezstack").setup()
  end,
}
```

If you use Telescope:

```lua
require("telescope").load_extension("ezstack")
```

### packer.nvim

```lua
use {
  "KulkarniKaustubh/ezstack",
  rtp = "neovim-plugin",
  config = function()
    require("ezstack").setup()
  end,
}
```

### Manual

Clone the repo and add `neovim-plugin/` to your runtimepath:

```vim
set runtimepath+=/path/to/ezstack/neovim-plugin
```

## Configuration

```lua
require("ezstack").setup({
  cli_path = "ezs",            -- path to ezs binary (auto-discovered)
  auto_refresh = true,          -- refresh viewer on FugitiveChanged / EzstackChanged
  viewer_position = "botright", -- split position for viewer
  viewer_height = 15,           -- viewer window height
  statusline_cache_ttl = 5000,  -- statusline cache TTL in milliseconds
  goto_strategy = "tcd",        -- "tcd" (tab-local), "cd" (global), "lcd" (window-local)
  goto_close_buffers = false,   -- close unmodified buffers from old worktree on goto
  goto_open_explorer = true,    -- open file explorer at new worktree root
})
```

## Commands

All commands are exposed under `:Ezs`. Tab completion is available for subcommands, common flags, and branch names.

| Command | Description |
|---------|-------------|
| `:Ezs` / `:Ezs list` | Open the stack viewer |
| `:Ezs status` | Stack viewer with PR/CI info |
| `:Ezs new <name> [parent]` | Create a new branch |
| `:Ezs sync` | Open the interactive sync menu (terminal) |
| `:Ezs sync -s` | Sync the current stack |
| `:Ezs sync -c` | Sync the current branch only |
| `:Ezs sync --continue` | Continue an in-progress sync (after resolving conflicts) |
| `:Ezs sync --dry-run` | Show the sync plan as JSON in a scratch buffer |
| `:Ezs push` | Push current branch |
| `:Ezs push -s` | Push entire stack |
| `:Ezs push -f` | Force-push (combine with `-s` for stack force-push) |
| `:Ezs pr create [title]` | Create a pull request |
| `:Ezs pr update` | Update PR description |
| `:Ezs pr merge` | Merge PR (prompts for method) |
| `:Ezs pr draft` | Toggle PR draft status |
| `:Ezs pr stack` | Update stack info in all PRs |
| `:Ezs pr open` | Open the current branch's PR in the browser |
| `:Ezs diff [-- opts]` | Show diff against parent branch (terminal) |
| `:Ezs commit [opts]` | Commit and auto-sync child branches (terminal) |
| `:Ezs amend [opts]` | Amend last commit and auto-sync children (terminal) |
| `:Ezs delete [branch]` | Delete a branch and worktree |
| `:Ezs reparent [branch] [parent]` | Change branch parent |
| `:Ezs rename [hash] [name]` | Name or rename a stack |
| `:Ezs stack [branch] [parent]` | Add a branch to a stack |
| `:Ezs unstack [branch]` | Remove a branch from tracking |
| `:Ezs goto [branch]` | Switch to a branch worktree |
| `:Ezs up` | Navigate to parent branch |
| `:Ezs down` | Navigate to child branch |
| `:Ezs log [branch]` | Show commits in a branch since its parent |
| `:Ezs config [show]` | Show ezs configuration in a scratch buffer |
| `:Ezs menu` | Open the interactive ezs menu (terminal) |
| `:Ezs agent` | Launch AI agent with stack context (terminal) |
| `:Ezs agent feature "desc"` | Launch agent to build a feature (terminal) |
| `:Ezs agent prompt` | View shipped work + feature prompts |
| `:Ezs agent prompt custom work` | View custom work instructions |
| `:Ezs agent prompt repo feature` | View repo-specific feature instructions |
| `:Ezs agent prompt edit [work\|feature]` | Edit custom instructions in Neovim |
| `:Ezs agent prompt edit repo [work\|feature]` | Edit repo-specific instructions |
| `:Ezs agent prompt reset [work\|feature]` | Reset custom instructions to defaults |
| `:Ezs agent prompt reset repo [work\|feature]` | Reset repo-specific instructions |

## Stack Viewer

The stack viewer (`:Ezs`) shows all stacks in a styled buffer:

```
 Stack: my-feature [a1b2c3d]                          root: main
 -----------------------------------------------------------------
   > ├── feature-1     PR #100 [OPEN]    CI: 3/3     +120 -8   (→ main)
     ├── feature-2     PR #101 [DRAFT]   CI: pending +50 -2    (→ feature-1)
     └── feature-3     [no PR]                                  (→ feature-1)
```

The viewer is non-modifiable; cursor position is preserved across refreshes.

### Viewer Keymaps

| Key | Action |
|-----|--------|
| `<CR>` | Go to worktree |
| `o` | Open PR in browser |
| `r` | Refresh |
| `R` | Rename stack |
| `n` | New branch |
| `d` | Delete branch under cursor |
| `p` | Push branch under cursor |
| `P` | Push entire stack |
| `s` | Sync (interactive terminal) |
| `c` | Continue an in-progress sync |
| `u` | Update PR for branch under cursor |
| `D` | Diff against parent (terminal) |
| `a` | Open agent (branch- or stack-scoped) |
| `A` | Build feature with agent (prompts for description) |
| `?` | Show keymap help |
| `q` | Close viewer |

## Telescope Integration

```lua
require("telescope").load_extension("ezstack")
```

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
- Integrates with `nvim-tree`, `neo-tree`, and `oil` file explorers

## Statusline

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

Returns a string like ` feature-1 | my-feature [a1b2c3d]` or `""`. Results are cached (default: 5s) to avoid hammering the CLI.

## Fugitive Integration

When [vim-fugitive](https://github.com/tpope/vim-fugitive) is installed and `auto_refresh = true`, the stack viewer automatically refreshes after fugitive operations (commits, checkouts, rebases).

The plugin also fires its own `User EzstackChanged` autocommand after every CLI mutation it performs (push, sync, delete, ...), so the viewer refreshes itself even without fugitive.

## Autocommands

| Event | Pattern | Description |
|-------|---------|-------------|
| `User` | `EzstackGoto` | Fired after switching worktrees via `:Ezs goto` |
| `User` | `EzstackChanged` | Fired after CLI mutations (sync, delete, etc.) |

## Help

Once installed, `:help ezstack` shows the full Vim help file.
