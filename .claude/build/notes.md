# desk build notes

The global `/build` skill holds the process. This file holds what is true only of desk.

## build

- **The plan is the spec**: `~/.claude/plans/task-runner-plugin.md` (v3, approved). Review synthesis
  and reports: `~/Programming/Scratch/agent-docs/.claude-main/task-runner-review-*.md`.
- **No tracker.** Leftovers go in the PR body.
- **Class moment.** No users, no data: recommend R0 for scaffolding and docs, R1 for code that
  spawns processes or serves the API, R2 only for the runner's spawn path and the token auth.
- **Stack**: Go 1.27, bubbletea/lipgloss/bubbles, modernc.org/sqlite (pure Go; no cgo).
  `go test ./...` must pass with `CGO_ENABLED=0`.
- **Open source from day one**: nothing in the binary names the operator's machines, paths, agents,
  or harness. Agent commands are argv templates in config; `session_env` names the session id
  variable; herdr and claude are optional runtime dependencies detected at run time.
- **Boundary**: the operator's own adoption (hooks text, contract, data moves) lives in `~/.agents`,
  never here. The `claude-code` profile under `profiles/` is generic to any Claude Code user.
- **Exit codes** are a contract: 0 ok / already true; 1 item refused with a stable code first;
  2 usage; 3 store or home I/O.

## all readers

- A finding that the binary hard-codes a path, host, agent, or harness concept is `behavior`, not
  `text`.
- Templates run as argv arrays; any shell interpolation of task text is `security`.
