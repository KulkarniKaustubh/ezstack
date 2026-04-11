// Package docs provides embedded documentation files for use in agent prompts.
package docs

import _ "embed"

//go:embed DOCUMENTATION.md
var Documentation string

//go:embed AGENTS.md
var Agents string
