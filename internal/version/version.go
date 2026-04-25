// Package version exposes the ezstack release version as a single source of
// truth so cmd/ezs, cmd/ezs-mcp, and runtime code (e.g. the agent MCP
// auto-install) stay in lock-step. scripts/bump-version.sh rewrites the
// constant below; do not hand-edit.
package version

// Version is the current ezstack release. Kept in sync by
// scripts/bump-version.sh.
const Version = "4.7.1"
