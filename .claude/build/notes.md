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

## builders

- **Spec**: the unit's brief under `docs/build/briefs/`. Its `## Public surface` is frozen.
- **Go**: 1.27, standard `testing` only (no test library), `gofmt` clean, `go vet` clean. The build
  must work with `CGO_ENABLED=0`. After adding an import of a new module run `go mod tidy`.
- **Commits**: `type(scope): description`. Types: `feat`, `fix`, `test`, `docs`, `refactor`,
  `chore`. Scope: the package or folder (`store`, `api`, `cli`, `scripts`, `profile`).
- **Comments**: one doc comment per exported identifier, as Go expects. Nothing else unless the
  code cannot say why.
- **Files and modes**: anything holding the token, the config, the store, the outbox, the snapshot,
  a session view, or the daemon log is 0600 in a 0700 folder. Never print the token in a log or
  a test's output.
- **Unix sockets**: macOS limits a socket path to 104 bytes. Tests get their folders from
  `internal/testutil`, never `t.TempDir()` for a path that holds a socket.
- **Docs**: `README.md` documents every command; `profiles/claude-code/skills/desk/SKILL.md` is
  what an agent may do. A change to a command or flag updates both in the same diff, unless the
  brief gives the file to another part.
- **No runtime UI** in this repo until the board (U3).
- **Shell**: `scripts/*.sh` and `scripts/e2e/*.sh` pass `shellcheck`.

## fixer

- The scoped test command is `go test ./internal/<package>/ -run '<Name>' -count=1`.
- A fix in `scripts/e2e/` changes a hand-test claim's command: it needs an `amend brief:` only
  when the claim's `pass:` line changes.

## hand-tester

- Claims run from the tree root, one script each: `bash scripts/e2e/<script>`. There is no stack
  and no simulator.
- Each script builds its own `desk` into a temp folder, runs real processes there, and removes
  the folder on exit. A script that fails prints `E2E FAIL: <reason>` on stderr.
- Tools the scripts need: `go`, `git`, `jq`, `curl`, `python3`, `tar`, `shasum`, and for three of
  them `claude` (H14, H16) and `herdr` (H15).
- Run every claim with the sandbox off: the scripts open unix sockets and a TCP port on
  127.0.0.1, H9 downloads goreleaser through `go run`, and H16 starts a short `claude -p` run.
- H15 links this tree into the running herdr as a disabled plugin and unlinks it. It refuses to
  run when a plugin with the id `desk` is already installed; that is `fail (env)`.
- The runner's claims are `r01-spawn.sh` to `r12-real-claude.sh` (H1 to H12 of U2), built on `lib.sh` and
  `runner-lib.sh`. `r01` to `r10` use a fake `herdr` (`fake-herdr.py`) and two stubs, and need no `herdr` or
  `claude`. `r09` takes over a minute (`max_run_minutes = 1`).
- `r11` needs the real `herdr` with its server running: it opens workspaces without focus in it and closes only
  those a run row of its own desk names. `r12` needs the real `herdr` and the real `claude`, and spends one router
  run and one worker run of the owner's quota: run it once, on purpose.
- Nothing serves stale code: every script builds from the tree as it is.

## test-author

- Tests are in the external package (`package store_test`), in the file the slice names, using
  only the standard `testing` package. Table-driven where the cases share a shape.
- One file: `go test ./internal/<package>/ -run '<TestName>' -count=1`. One package:
  `go test ./internal/<package>/ -count=1`.
- A test that needs a home uses `internal/testutil` (`StartHome`, `NewClientMachine`). It never
  reads or writes the real XDG folders, and never talks to anything but 127.0.0.1.
- Never sleep to wait: poll with a deadline.
- No `mutation_proof` step. To prove a test bites, change the code under test in your own tree,
  watch the test fail, and put the code back by writing the original text again (no git).

## all readers

- A finding that the binary hard-codes a path, host, agent, or harness concept is `behavior`, not
  `text`.
- Templates run as argv arrays; any shell interpolation of task text is `security`.
