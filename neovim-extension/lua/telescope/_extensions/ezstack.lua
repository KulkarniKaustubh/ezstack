-- Telescope extension registration for ezstack.nvim.
--
-- Loaded by users via:
--     require("telescope").load_extension("ezstack")
--
-- After loading, the following pickers become available:
--     :Telescope ezstack branches
--     :Telescope ezstack stacks
--
-- The README documents this — without this file, those commands fail.

local ok, telescope = pcall(require, "telescope")
if not ok then
  error("ezstack telescope extension requires nvim-telescope/telescope.nvim")
end

return telescope.register_extension({
  setup = function(_ext_config, _config)
    -- No-op; ezstack.nvim's own setup() configures everything.
  end,
  exports = {
    -- Default exported when called as `:Telescope ezstack`
    ezstack = function(opts)
      require("ezstack.telescope").branches(opts)
    end,
    branches = function(opts)
      require("ezstack.telescope").branches(opts)
    end,
    stacks = function(opts)
      require("ezstack.telescope").stacks(opts)
    end,
  },
})
