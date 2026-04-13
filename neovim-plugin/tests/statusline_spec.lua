-- Statusline formatter tests.

local H = require("tests.helpers")

describe("ezstack.statusline", function()
  local ezstack

  before_each(function()
    ezstack = H.reload()
    ezstack.setup({ welcome = false })
  end)

  it("returns empty string when no stacks", function()
    local restore = H.stub_list_stacks_sync({})
    assert.equals("", ezstack.statusline())
    restore()
  end)

  it("returns empty string when no branch is current", function()
    local restore = H.stub_list_stacks_sync({
      H.fake_stack({ branches = { { name = "a", parent = "main" } } }),
    })
    assert.equals("", ezstack.statusline())
    restore()
  end)

  describe("format = 'stack' (default, backwards compatible)", function()
    it("renders ` branch | stack [hash]`", function()
      ezstack.config.statusline_format = "stack"
      local restore = H.stub_list_stacks_sync({
        H.fake_stack({
          branches = {
            {
              name = "feat-1",
              parent = "main",
              is_current = true,
              pr_number = 42,
              pr_state = "OPEN",
            },
          },
        }),
      })
      local s = ezstack.statusline()
      assert.truthy(s:find("feat%-1"))
      assert.truthy(s:find("my%-feat"))
      assert.truthy(s:find("a1b2c3d")) -- 7-char hash prefix
      assert.is_nil(s:find("PR#")) -- PR info must NOT appear in "stack" mode
      restore()
    end)
  end)

  describe("format = 'pr'", function()
    it("renders branch + PR info when PR exists", function()
      local out = ezstack._format_statusline(
        { name = "feat-1", pr_number = 42, pr_state = "OPEN" },
        { name = "s", hash = "abcdefghi" },
        "pr"
      )
      assert.equals(" feat-1 | PR#42 OPEN", out)
    end)

    it("omits PR section when no PR", function()
      local out = ezstack._format_statusline(
        { name = "feat-1", pr_number = 0 },
        { name = "s", hash = "abc" },
        "pr"
      )
      assert.equals(" feat-1", out)
    end)
  end)

  describe("format = 'full'", function()
    it("combines stack and PR info", function()
      local out = ezstack._format_statusline(
        { name = "feat-1", pr_number = 42, pr_state = "OPEN" },
        { name = "my-feat", hash = "a1b2c3d4e5" },
        "full"
      )
      assert.truthy(out:find("feat%-1"))
      assert.truthy(out:find("my%-feat"))
      assert.truthy(out:find("a1b2c3d"))
      assert.truthy(out:find("PR#42 OPEN"))
    end)
  end)
end)
