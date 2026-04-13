// Package ezstack provides embedded documentation files for use in agent prompts.
package ezstack

import _ "embed"

//go:embed DOCUMENTATION.md
var Documentation string

//go:embed AGENTS.md
var Agents string
