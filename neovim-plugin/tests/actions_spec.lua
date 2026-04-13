-- Tests for `:EzsActions` / `M.actions_menu`.

local H = require("tests.helpers")

describe("ezstack.actions_menu", function()
  local ezstack

  before_each(function()
    ezstack = H.reload()
    ezstack.setup({ welcome = false })
  end)

  it("exposes a stable labels list", function()
    local labels = ezstack._action_labels()
    assert.is_true(#labels >= 10)
    -- Core ops must be present.
    local set = {}
    for _, l in ipairs(labels) do set[l] = true end
    assert.is_true(set["sync (whole stack)"])
    assert.is_true(set["push stack"])
    assert.is_true(set["pr create"])
    assert.is_true(set["pr merge"])
    assert.is_true(set["pr draft (toggle)"])
    assert.is_true(set["pr open in browser"])
    assert.is_true(set["graph"])
  end)

  it("every label maps to a callable action", function()
    local actions = ezstack._actions()
    for _, label in ipairs(ezstack._action_labels()) do
      assert.equals("function", type(actions[label]),
        "missing action: " .. label)
    end
  end)
end)
