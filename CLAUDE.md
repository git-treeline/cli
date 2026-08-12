# git-treeline CLI — agent guide

Go CLI for worktree environment management. Allocates ports, databases, and
services across parallel git worktrees; exposes an MCP server for AI agent
integration.

## Stack

- Go 1.26+
- Cobra for CLI structure (`cmd/`)
- bubbletea/v2 for TUI components
- mcp-go for the MCP server (`cmd/serve.go`)

## Development

```bash
make ci          # lint + test (installs golangci-lint and govulncheck if absent)
go test ./...    # tests only
go build .       # build the binary
```

## Testing conventions

Prefer pure extraction over dependency injection — see CONTRIBUTING.md for the
full hierarchy. New commands need table-driven tests in `cmd/<name>_test.go`.
Run `go test ./cmd/...` after any `cmd/` change; run `go test ./...` before
opening a PR.

## README update policy

`README.md` is the canonical user-facing reference. Keep it accurate; it is
not auto-generated.

**Update the README when a PR:**
- Adds a new command (add a row to the CLI reference table and, if the command
  warrants it, a numbered section in Quick start)
- Adds a flag to an existing command (add it to the flags column of that
  command's row in the CLI reference table)
- Changes a flag's name or behavior in a user-visible way
- Adds or changes a `.treeline.yml` field (update the project config fields
  table)
- Adds or changes a user config key (update the user config section)
- Adds or changes an interpolation token (update the tokens table)
- Changes the behavior of an existing command in a way that contradicts the
  current docs

**Do not update the README for:**
- Bug fixes that restore documented behavior
- Internal refactors with no user-visible surface change
- Dependency bumps
- CI / workflow changes
- Test-only changes

**How to update:**

1. CLI reference table (bottom of README): one row per command, format
   `| \`gtl <cmd>\` | \`--flag1\` \`--flag2\` | Description |`. Keep flags
   in roughly the order they appear in `--help`. Use backtick-wrapped flag
   names.
2. Quick start numbered sections: only add a new section for commands a new
   user would encounter early. Networking, database, and advanced lifecycle
   commands already have sections; don't duplicate them. Extend an existing
   section when adding a flag to a well-known command.
3. Config tables: mirror the field names and types exactly as they appear in
   the Go structs — don't invent defaults that aren't enforced in code.

When in doubt: update the CLI reference table row (it's always correct to keep
flags current) and skip the prose sections unless the feature is genuinely new
and user-facing enough to warrant a walkthrough.
