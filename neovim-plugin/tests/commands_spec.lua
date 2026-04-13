-- Contract tests for the `:Ezs` command dispatcher.

local H = require("tests.helpers")

describe("ezstack.commands", function()
  local cmds

  before_each(function()
    H.reload()
    cmds = require("ezstack.commands")
  end)

  it("every SUBCOMMAND_NAMES entry has a dispatch handler", function()
    -- This is the regression test for the `:Ezs actions` bug: a name
    -- advertised in tab-completion must resolve to a real handler.
    for _, name in ipairs(cmds._subcommand_names) do
      assert.equals(
        "function",
        type(cmds._subcommands[name]),
        "missing handler for subcommand: " .. name
      )
    end
  end)

  it("SUBCOMMAND_NAMES is deduplicated and sorted-ish (no trailing dupes)", function()
    local seen = {}
    for _, n in ipairs(cmds._subcommand_names) do
      assert.is_nil(seen[n], "duplicate in SUBCOMMAND_NAMES: " .. n)
      seen[n] = true
    end
  end)

  describe(":Ezs diff passthrough detection", function()
    local p

    before_each(function()
      p = cmds._looks_like_diff_passthrough
    end)

    it("returns false for no args (use viewer)", function()
      assert.is_false(p({}))
    end)

    it("returns false for a plain branch name", function()
      assert.is_false(p({ "feature-1" }))
      assert.is_false(p({ "origin/main" }))
    end)

    it("returns true for an explicit `--` terminator", function()
      assert.is_true(p({ "--", "--stat" }))
    end)

    it("returns true for any single-dash or double-dash flag", function()
      assert.is_true(p({ "-b", "main" }))
      assert.is_true(p({ "--stat" }))
      assert.is_true(p({ "--json" }))
      assert.is_true(p({ "branch", "--stat" }))
    end)
  end)
end)
