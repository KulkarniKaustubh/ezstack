return require("telescope").register_extension({
  exports = {
    branches = function()
      require("ezstack.telescope").branches()
    end,
    stacks = function()
      require("ezstack.telescope").stacks()
    end,
  },
})
