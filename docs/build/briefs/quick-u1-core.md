class: R2 — agent (unconfirmed), 2026-10-03
model: opus — the store write path, the API auth boundary, and the CLI carry `## Design` entries (P2, P4, P6)

# desk U1: store, daemon, API, CLI, journal, claude-code profile, herdr bridge, release config

Unit 1 of the plan `~/.claude/plans/task-runner-plugin.md` (v3, approved 2026-10-03). The repo holds only a scaffold (`LICENSE`, `README.md`, `go.mod`, `AGENTS.md`, `.claude/`), so every file below is new unless marked.

Branch contract: commits on `quick/u1-core` (builders: on the harness-named branch of your own tree). No push and no PR from a builder; the session pushes at SHIP.

Operator instruction for this unit (2026-10-03): "Make sure there is very thorough hand testing and code coverage". It is why this brief carries sixteen hand-test claims, ten test slices, and a coverage gate.

## Spec

The plan's sections for this unit, verbatim except that each heading is one level deeper.

### The model

- **One home per desk.** The home machine runs the daemon, owns the SQLite store, and is the only writer. By default the home is the machine you ran `desk setup` on. Other machines are **clients**: their CLI, board, and hooks talk to the home over the tailnet (token auth). A client with the home unreachable refuses writes with `home-unreachable` (exit 3) and opens the board read-only from its last snapshot with an "offline" banner. Journal writes (`note`, `decide`) are the one exception: they spill to a local outbox and forward on the next successful call, because they are append-only facts.
- **One runner per desk**, on the home. Two machines that should each run their own tasks are two desks.
- **Task ids** are a global `T<n>` counter assigned by the home.
- **Arming** is the user's act: a task runs only when a `who=user` event set `status=ready` with thread `agent`. Agents may add tasks and set the `agent` thread (a proposal lands in the inbox with `#agent`), never `ready` or `done`. Config `runner.agents_may_arm = true` lifts this, with a warning comment above it.
- **Hand-back**: the worker sets `review` or `blocked`; `on_merged = "review" | "done"` (default `review`; the operator sets `done`) decides what a merged PR does when the worker reports it with `--ref <pr-url> --merged`.
- **Docs are not desk's.** Agent-docs, plans, agent-memory move to a private git repo synced by the harness (U4); desk stores paths.

### U1. Core: store, daemon, API, CLI, journal, profile, release

#### Store (SQLite, on the home, WAL)

```
events(id INTEGER PK, ts TEXT utc, session TEXT, who TEXT, kind TEXT, task INTEGER NULL, data JSON, tags JSON NULL, run INTEGER NULL, v INTEGER)
tasks(id INTEGER PK, number INTEGER UNIQUE, title, notes, status, project, thread, archived, root, isolation, model, created_ts, updated_ts)   -- materialized by the daemon from events
steps(task, short_id, text, done, pos)
runs(id INTEGER PK, task, state, root, isolation, model, reason, session, workspace, pane, started_ts, ended_ts, exit)
sessions(id TEXT PK, continues TEXT NULL, first_ts, last_ts)
```

Events are the history (journal, task page HISTORY); tables are the current state. Kinds: `task` (create), `set` (field patch incl. status/archive/root/isolation/model), `step`, `note` (text, ref?, branch?, merged?), `decision` (text, who, replaces?), `merged` (branch, pr, sha), `compacted`, `continues`. `v=1`. The secret scan (built-in patterns: private keys, AWS, GitHub, Anthropic, Slack; `[secret_scan] command` overrides, e.g. `gitleaks stdin`) runs in the one write path before insert; a hit refuses with `secret-detected`. Backup: `[backup] git_remote` set → the daemon exports `events.jsonl` nightly and commits/pushes; off by default.

#### Daemon and API

`desk daemon`: single instance (`flock` on `$XDG_STATE_HOME/desk/daemon.lock`); started by herdr `[[startup]]`, by `desk` commands when its heartbeat (`state/daemon.json`) is stale, or by hand / a launchd unit (documented, not shipped). Serves a JSON API on a unix socket (`$XDG_STATE_HOME/desk/desk.sock`, 0600) and, when `[home] listen` is set, on a tailnet address with a bearer token minted by `setup` (`desk token` prints/rotates it; `desk client add <host>` on a client stores it). Endpoints: `tasks.list|get|add|set|steps`, `events.append`, `session.view`, `runs.list|kill`, `status`. Every write names `who` from the token (`user` for the TUI and a human's shell, `agent` when `session_env` is set in the caller's environment, enforced server-side by the client sending both and the server trusting only the session claim, not the who claim: a caller with a session id is an agent). Clients cache the last `tasks.list` under `$XDG_CACHE_HOME/desk/snapshot.json`.

#### CLI

`desk` (board), `add`, `list`, `show`, `set`, `edit`, `steps`, `note`, `decide`, `session`, `capture`, `worker`, `daemon`, `token`, `client`, `roots`, `setup`, `hook`, `backup`, `version`. `--json` on reads; stable refusal codes (`unknown-task`, `unknown-project`, `secret-detected`, `not-allowed` for an agent setting ready/done, `home-unreachable`); exit 0/1/2/3 as tsk's contract. No trash/undo. `edit --notes` replaces the body (the board's "answer a blocked task" flow writes a `note`, never a replace). `project` = main checkout top level (`git rev-parse --path-format=absolute --git-common-dir`, parent), never a worktree. The skill (`skills/desk/SKILL.md`, embedded, written out by `setup`) is desk's own text: agents set `review`/`blocked`, never `ready`/`done`; propose with `--thread agent`; `--json`; never blind-retry; `note --ref` for files/PRs; `decide --tag k:v`; `session <id> --md`.

#### Journal

`desk session <id> --md [--all]` renders Work log / Todo / Decisions from the session's events and the sessions it `continues` (cycle-guarded). Hidden unless `--all`: a note or done task tagged `branch:<b>` after a `merged` for `<b>`; an untagged note after the second `compacted` following it; a done task with no branch after the next `compacted`; a decision replaced by a later one, after the next `merged` or `compacted`; never hidden: open tasks, `question`-tagged lines, the last decision per `tunable:<k>` tag, merged lines. Tests: the rule tests of `~/.agents/src/runtime/build/__tests__/sessionLogPrune.test.ts` (`:58, :74, :84, :95, :130, :141, :205, :219, :230, :258`) ported, plus `replaces` by id and `continues`. Output over 10k chars is the caller's problem: `hook start` writes it to `$XDG_STATE_HOME/desk/sessions/<id>.md` and prints the path.

#### Profile `claude-code` (a Claude Code plugin in `profiles/claude-code/`)

`hooks/hooks.json`: `SessionStart` → `desk hook start --format claude-code` (stdin JSON; `source=startup|resume` renders the view file and prints the inject text; `compact` appends `compacted`); the skill is the same file. `setup --profile claude-code` writes `[agent]` templates: `router = ["claude","-p","--safe-mode","--tools","","--system-prompt-file","{system}","--json-schema","{schema}","--max-budget-usd","0.10","--no-session-persistence","--output-format","json"]` (task on stdin; read `structured_output`), `worker = ["claude","--model","{model}","--permission-mode","auto","--session-id","{session}","--","{message}"]`, `session_env = "CLAUDE_CODE_SESSION_ID"`. Templates are argv arrays, never shell; `DESK_HOOKS=off` in the router's env makes `desk hook` a no-op. Install: `/plugin marketplace add federbenjamin/desk` then install `desk`.

#### herdr bridge

`herdr-plugin.toml` at repo root: `id = "desk"`, `min_herdr_version = "0.9.0"`, `platforms = ["linux","macos"]`, `[[build]] scripts/fetch-or-build.sh` (release by version + platform incl. linux arm64, sha256, else `go build`; installs to `~/.local/bin/desk` if not on PATH and tells the user), `[[startup]] desk daemon`, actions `open-board` (open or focus the pane titled `desk` in this workspace), `capture` (popup `desk capture`), `restart`. `setup` writes `prefix+t`/`prefix+a` into herdr's config.toml inside a marker-fenced block only when free (`--force` replaces, e.g. tsk's), backs up the file, never binds `ctrl+d`. Notifications via `[notify] command` (default `herdr notification show {title} --body {body}` when herdr is present; the operator sets `notify-user {title}: {body}` since his toasts are off).

#### Config `$XDG_CONFIG_HOME/desk/config.toml`

```toml
[home]            # present on the home only
listen = "100.100.198.1:7411"   # tailnet address; empty = local only
[client]          # present on clients only
home = "100.100.198.1:7411"
[runner]
enabled = false   # setup asks on the home
cap = 1
max_runs_per_day = 20
max_run_minutes = 180
poll_seconds = 30
agents_may_arm = false   # WARNING: true lets any agent start unattended runs that spend your quota
on_merged = "review"
[[roots]]
path = "~/Programming/Quirk"
about = "the app; runs its own build pipeline"
isolation = "self"       # self | worktree | in-place; unset = router chooses worktree or in-place
[agent]           # written by a profile
router = [...]; worker = [...]; session_env = "..."
[notify] command = [...]
[secret_scan] command = []
[backup] git_remote = ""
```

A default root `scratch` (`$XDG_DATA_HOME/desk/scratch`, a git repo) is always present so no-project tasks are routable.

#### Release

goreleaser: darwin/linux × arm64/amd64, GitHub Releases with checksums, tap formula in `federbenjamin/homebrew-tap`. `THIRD_PARTY_NOTICES.md` for Go deps. README: install for herdr+Claude Code, herdr+other agent (set the three `[agent]` values), no herdr. `.claude/build-steps.toml` in the repo: `checks = go vet ./... && go test ./... && test -z "$(gofmt -l .)"`.

#### Acceptance (U1)

`desk add -t x` on the home → `T1`; on a client `desk list --json` shows `T1` within one call; client with home down: `add` exits 3 `home-unreachable`, `desk` opens with the offline banner; `desk note` on the offline client succeeds and appears on the home after reconnect; an agent session's `desk set T1 ready` → `not-allowed`; a note with an AWS key → `secret-detected`; `desk session <id> --md` passes the ported rule tests; `desk hook start --format claude-code < fixture` prints a path for startup/resume and appends `compacted` for compact; `herdr plugin install federbenjamin/desk` on a Mac without Go yields a working `desk`; `brew install federbenjamin/tap/desk` works; `go vet`, `go test`, `gofmt -l` clean.

### Out of scope

Multi-home or multi-writer stores; any-machine claiming; importing old session logs; profiles for codex/opencode (templates documented, untested); the wiki ingesting events; Windows; a web UI.

### Risks (principle 7)

- **Unattended runs spend quota on text nobody reviewed.** Bound: user-only arming by default; sealed router (`--safe-mode`, enum output, no model-written prompt); caps per day and per run; visible pane; notify per spawn.
- **A stale run overwrites the task.** Bound: run id on every write; the daemon ignores an old run's status writes.
- **The home is a single point.** Bound: clients degrade to read-only with a banner; journal outbox; nightly backup when configured; the store is one file to copy.
- **A secret lands in the store or the docs repo.** Bound: scan in the one write path; docs scan before commit; private repos only.
- **Token leaks on the tailnet.** Bound: tailnet-only bind, bearer token rotatable with `desk token rotate`, 0600 files.

## Corrections to the spec

Each line is a place where this brief departs from the plan text above. Where the two differ, this section wins.

1. `desk worker` and `runs.kill` are not built here. They have no behaviour without the runner, so U2 builds them. U1 ships the `runs` table, the `events.run` column, and the read-only `runs.list` endpoint, because U3's board reads it while U2 is still being built.
2. Bare `desk` prints a static text board (U3 replaces it with the bubbletea board). `desk capture` reads one line from stdin (U3 owns the popup's look).
3. The skill file lives at `profiles/claude-code/skills/desk/SKILL.md`, not `skills/desk/SKILL.md`. An installed Claude Code plugin is copied to a cache and cannot read a file outside its own root, so the one copy of the skill sits inside the plugin and the binary embeds it from there.
4. `desk hook start` reads the session id from the `session_id` field of the stdin JSON. `CLAUDE_CODE_SESSION_ID` is set by Claude Code today but is not documented, so only `[agent] session_env` names it, for commands an agent runs later.
5. The journal ports nine of the ten listed tests. From the test at `:258` only the half "compaction hides no decision" is ported. Its other half warns when an agent's decision names no "principle or contract rule", which is a concept of the author's own harness and may not appear in this binary (`.claude/build/notes.md` §build).
6. The first release (the tag, the `federbenjamin/homebrew-tap` repo, making the repo public) happens after this unit merges and needs the operator. This unit ships the release config and proves it with a snapshot build (H9) and a hermetic install test (H8). The two acceptance lines `herdr plugin install …` and `brew install …` are proven at the release, not here.
7. The offline outbox takes every `events.append` kind (`note`, `decision`, `merged`, `compacted`, `continues`), not only `note` and `decision`. All five are append-only facts and the hook's `compacted` must not be lost when the home is down.
8. A `backup.run` endpoint is added, served on the unix socket only. `desk backup` needs it because only the daemon opens the store.
9. `[secret_scan] command` exit codes: 0 clean, 1 a secret was found, anything else (or a command that cannot start) is a scanner failure and refuses the write with `scan-failed` (exit 3). The plan says only "overrides"; without this a missing binary would report every write as a secret, or pass every secret.
10. A coverage gate is added to the repo's checks (the operator instruction above).
11. `tasks` has `number INTEGER PRIMARY KEY` and no separate `id`. The plan lists both; nothing reads `id`, and `steps.task`, `runs.task`, and `events.task` all hold the number.
12. `sessions` is `(id TEXT PRIMARY KEY, continues TEXT NULL)`. The plan's `first_ts` and `last_ts` have no reader in any unit.
13. No heartbeat. The plan starts the daemon "when its heartbeat is stale"; here a command starts it when the socket does not answer, and `daemon.json` is written once at start (pid, version, start time, socket, listen address). One liveness test, no periodic write.
14. There is no `internal/hook` package: `desk hook start` has one caller, so it is a command file in `internal/cli`.

## Prior art

The repo has no code, so nothing in it can be reused. The plan's table, verbatim, with where each reference sits on this machine:

| artifact | verdict | reference |
|---|---|---|
| statuses, exit contract, skill rules, board sections | justified-new: shapes from tsk (MIT), text rewritten; no notice needed | `tsk --help`, `~/.agents/skills/tsk-cli/SKILL.md` |
| install script | reuse the shape of herdr-file-viewer's fetch-or-build | `~/.config/herdr/plugins/github/herdr-file-viewer-c993314e2614/scripts/fetch-or-build.sh` |
| daemon start | reuse auto-title's `[[startup]]` + `restart` | `~/.config/herdr/plugins/github/herdr.auto-title-4b7d61f48ce8/herdr-plugin.toml` |
| dual herdr + Claude Code plugin layout | reuse clauth's | `~/.config/herdr/plugins/github/clauth-4596d4a41686/` (`.claude-plugin/marketplace.json`, `plugins/.claude-plugin/plugin.json`, `plugins/hooks/hooks.json`, `herdr-plugin/herdr-plugin.toml`) |
| journal rules | reuse `sessionLogPrune.ts` as spec | `~/.agents/src/runtime/build/sessionLogPrune.ts`, `~/.agents/src/runtime/build/__tests__/sessionLogPrune.test.ts` |
| spawn | U2, not this unit | |

The reference paths are for reading only. No path, host, or name from them may appear in the repo.

## Design

Chosen shape, one entry per choice. Each names the files it binds.

1. **One writer: the daemon.** Only `desk daemon` opens the SQLite file. Every CLI command, hook, and (later) the board talks to it over a JSON API, on the home through the unix socket and on a client through TCP. `internal/store` has one private append function: it scans for secrets, inserts the event, and updates the state tables in one transaction. Every exported write method goes through it. Binds `internal/store/**`, `internal/daemon/**`.
   - Rejected: the home's CLI opening the file itself, with the daemon only for clients. Two writers on one file, and U2's runner needs the daemon anyway.
   - Rejected: rebuilding the state tables from events in the background. A read right after a write could miss it.
2. **The API is HTTP with JSON bodies, `POST /v1/<method>`, on two listeners with one handler set.** The unix socket (file mode 0600, in a 0700 directory) is trusted. The TCP listener, present only when `[home] listen` is set, requires `Authorization: Bearer <token>`. Binds `internal/api/**`.
   - Rejected: a custom line protocol. HTTP is testable with curl and gives timeouts and body limits for free.
   - Rejected: TLS on the TCP listener. The listener binds one named address on a private network (a tailnet is already encrypted); certificate handling would be most of the code.
3. **Who wrote it is derived, never claimed.** A request carries a session id or none. A caller with a session id is `agent`; one without is `user`. The server never reads a `who` field. Binds `internal/store/**`, `internal/api/**`.
4. **Policy lives in the store's write path, not in the CLI.** `not-allowed` (an agent setting `ready` or `done`), `runner.agents_may_arm` (lifts `ready` only; `done` stays the user's), and `runner.on_merged` are checked where the event is written, so no client can skip them. Binds `internal/store/**`.
5. **The journal is a pure function on the client side.** `session.view` returns the session's events and tasks; `internal/journal` turns them into the view. The server renders nothing, so `internal/journal` imports only `internal/model`. Binds `internal/journal/**`, `internal/api/**`.
6. **The CLI is built on cobra.** The plan's own examples put flags after positionals (`desk set T1 review --ref <url> --merged`), which the standard `flag` package does not parse. `internal/cli.Run` takes its streams, environment, and working directory as values and returns the exit code, so tests never touch the process. Binds `internal/cli/**`, `cmd/desk/**`.
   - Rejected: the standard `flag` package (no flags after positionals). Rejected: urfave/cli (no gain over cobra).
7. **Config writes keep their comments.** The config struct carries `comment` tags and is marshalled with `github.com/pelletier/go-toml/v2`, so the `WARNING` line above `agents_may_arm` survives every `desk roots add`. Binds `internal/config/**`.
8. **Offline is the client's job.** `internal/api.Client` owns the snapshot and the outbox. A caller sees one of three results: the answer, a refusal, or `home-unreachable`. Binds `internal/api/**`.

9. **One definition per shape.** An event's payload type is also the type the caller passes in (`model.Patch`, `model.StepOp`, `model.MergedData`; the store's `AddTaskInput`, `NoteInput`, `DecisionInput` embed their payload). The filter decides in one function, `store.Filter.Match`. The session-id rule is one function, `model.ValidSessionID`. Binds `internal/model/**`, `internal/store/**`.
10. **Each package owns its own data.** The token file and the roots list are config's (`config.ReadToken`, `(*Config).AddRoot`), the backup result and the backup state file are backup's, and `internal/setup` keeps three jobs: first-time setup, the herdr key edit, and joining a home. Binds `internal/config/**`, `internal/backup/**`, `internal/setup/**`.
11. **A write is scanned once.** The store joins a write's text fields with newlines and calls the scanner one time, so an external scanner is one process per write. Binds `internal/store/**`, `internal/secretscan/**`.

Design pass (simplifier, 2026-10-03): entries 9 to 11, corrections 11 to 14, the `Scanner` func type, `Status.RunnerOn`, the snapshot rule inside `Client.ListTasks`, `backup.Result`, `HomeOptions{Config, Listen}`, and the external-test-package rule come from it. Two of its verdicts are not applied. `testutil.NewClientMachine` does not call `setup.ClientAdd`: `testutil` is built a part earlier than `setup`, and both write through `config.Save` and `config.WriteToken`. The install script keeps both of its proofs: the Go test stubs `curl` and `go` and runs in the checks, H8 runs the real `curl`, `tar`, `shasum`, and `go build`.

Assumptions the design rests on:

- "A caller with a session id is an agent" guards against accidents, not against a hostile local process. Anything running as the user can drop the variable and write as the user. The security boundary is the socket's file mode and the token.
- The token grants every write. There is one token per desk.
- Event ids are a total order (one writer, `INTEGER PRIMARY KEY`). Every journal rule compares ids, never timestamps.
- Delivery from the outbox is at-least-once: a forward whose answer is lost is sent again and lands twice. A doubled journal line is accepted over a dedupe key.
- macOS limits a unix socket path to 104 bytes. A longer path is an error that names the path, not a truncation.

Libraries (check current docs through context7 before the first call): `modernc.org/sqlite` (pure Go; `go test` must pass with `CGO_ENABLED=0`), `github.com/spf13/cobra`, `github.com/pelletier/go-toml/v2`, `golang.org/x/sys/unix` for `flock`. No test library: tests use the standard `testing` package only.

## Rules every part follows

- Nothing in the repo names the author's machines, paths, agents, or harness (`AGENTS.md`). Example values are `127.0.0.1` and `example`.
- Files: config, token, daemon log, outbox, snapshot, session views are mode 0600; their directories 0700. The store file is 0600.
- A refusal message never contains the text that was refused. `secret-detected` names the pattern (`aws-access-key`), never the match.
- Agent argv templates are arrays. No task text is ever passed through a shell.
- A session id is used in a file name. `model.ValidSessionID` is the one check; an id it rejects is a usage error (exit 2).
- Every `git` child process runs with `GIT_TERMINAL_PROMPT=0` and a timeout.
- Times are stored as RFC 3339 UTC text with nanoseconds.
- No sleeping in tests to wait for something: poll with a deadline.
- `go.mod` and `go.sum` belong to no part. P2, P4, and P6 run one after another and each adds the dependency it imports, then runs `go mod tidy`. No other part touches them.

## Public surface

Frozen. Test writers author against these paths, names, and signatures before the code exists. A needed rename or move is a STOP.

Module `github.com/federbenjamin/desk`, Go 1.27. `ctx` is `context.Context` throughout. The version is read from `internal/version.Version` where it is used; no function takes it as a parameter.

### `embed.go` (package `desk`, repo root)

```go
// Skill returns the text of the agent skill (profiles/claude-code/skills/desk/SKILL.md).
func Skill() string
```

### `internal/version`

```go
var Version = "dev" // set by -ldflags at release
```

### `internal/model` (types only; no I/O)

```go
type Status string
const (
	StatusOpen    Status = "open"
	StatusReady   Status = "ready"
	StatusStarted Status = "started"
	StatusBlocked Status = "blocked"
	StatusReview  Status = "review"
	StatusDone    Status = "done"
)
func ParseStatus(s string) (Status, bool)

type Who string
const (
	WhoUser  Who = "user"
	WhoAgent Who = "agent"
)

type Kind string
const (
	KindTask      Kind = "task"
	KindSet       Kind = "set"
	KindStep      Kind = "step"
	KindNote      Kind = "note"
	KindDecision  Kind = "decision"
	KindMerged    Kind = "merged"
	KindCompacted Kind = "compacted"
	KindContinues Kind = "continues"
)

type Step struct {
	ShortID string `json:"short_id"`
	Text    string `json:"text"`
	Done    bool   `json:"done"`
}

type Task struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Notes     string    `json:"notes"`
	Status    Status    `json:"status"`
	Project   string    `json:"project"`
	Thread    string    `json:"thread"`
	Archived  bool      `json:"archived"`
	Root      string    `json:"root"`
	Isolation string    `json:"isolation"`
	Model     string    `json:"model"`
	CreatedTS time.Time `json:"created_ts"`
	UpdatedTS time.Time `json:"updated_ts"`
	Steps     []Step    `json:"steps"`
}

type Event struct {
	ID      int64           `json:"id"`
	TS      time.Time       `json:"ts"`
	Session string          `json:"session"`
	Who     Who             `json:"who"`
	Kind    Kind            `json:"kind"`
	Task    int             `json:"task"` // 0 = no task
	Data    json.RawMessage `json:"data"`
	Tags    []string        `json:"tags"`
	Run     int64           `json:"run"` // 0 = no run
	V       int             `json:"v"`   // 1
}

// Each kind's Event.Data is one of these types, and each type is also what the caller passes in:
// there is one definition per shape.

// TaskData is a task event's payload.
type TaskData struct {
	Title   string `json:"title"`
	Notes   string `json:"notes,omitempty"`
	Project string `json:"project,omitempty"` // an absolute path, stored as given; or the bare name of one known project
	Thread  string `json:"thread,omitempty"`
	Status  Status `json:"status,omitempty"` // "" → open
}

// Patch is a change to a task's fields. As a set event's payload it holds only the fields that changed.
type Patch struct {
	Status    *Status `json:"status,omitempty"`
	Title     *string `json:"title,omitempty"`
	Notes     *string `json:"notes,omitempty"`
	Thread    *string `json:"thread,omitempty"`
	Root      *string `json:"root,omitempty"`
	Isolation *string `json:"isolation,omitempty"`
	Model     *string `json:"model,omitempty"`
	Archived  *bool   `json:"archived,omitempty"`
	Ref       string  `json:"ref,omitempty"`
	Merged    bool    `json:"merged,omitempty"`
}

// StepOp is one change to a task's steps, and a step event's payload.
type StepOp struct {
	Op      string `json:"op"` // add | toggle | rename | remove
	ShortID string `json:"short_id,omitempty"`
	Text    string `json:"text,omitempty"`
}

type NoteData struct {
	Text string `json:"text"`
	Ref  string `json:"ref,omitempty"`
}
type DecisionData struct {
	Text     string `json:"text"`
	Replaces int64  `json:"replaces,omitempty"` // id of the decision event it replaces
}
type MergedData struct {
	Branch string `json:"branch"`
	PR     int    `json:"pr,omitempty"`
	SHA    string `json:"sha,omitempty"`
	Text   string `json:"text,omitempty"`
}
// ContinuesData records the link in the event log. Readers follow the chain through the sessions table.
type ContinuesData struct {
	From string `json:"from"` // the session this one continues
}

// MustData marshals a payload for Event.Data; it panics on a marshal error.
func MustData(v any) json.RawMessage

// BranchTag returns "branch:<b>"; BranchOf returns the branch a tag list carries, or "".
func BranchTag(branch string) string
func BranchOf(tags []string) string

// ValidSessionID reports whether s can name a session: ^[A-Za-z0-9._-]{1,128}$ and neither "." nor "..".
func ValidSessionID(s string) bool

type Run struct {
	ID        int64     `json:"id"`
	Task      int       `json:"task"`
	State     string    `json:"state"`
	Root      string    `json:"root"`
	Isolation string    `json:"isolation"`
	Model     string    `json:"model"`
	Reason    string    `json:"reason"`
	Session   string    `json:"session"`
	Workspace string    `json:"workspace"`
	Pane      string    `json:"pane"`
	StartedTS time.Time `json:"started_ts"`
	EndedTS   time.Time `json:"ended_ts"`
	Exit      int       `json:"exit"`
}

// SessionTask is a task created by a session in the chain, with what the journal needs.
type SessionTask struct {
	Task
	Created int64    `json:"created"` // id of its task event
	DoneAt  int64    `json:"done_at"` // id of the event that last set it done; 0 when not done
	Tags    []string `json:"tags"`    // tags of its task event
}

// SessionData is what session.view returns.
type SessionData struct {
	Session string        `json:"session"`
	Chain   []string      `json:"chain"` // the session, then each one it continues
	Events  []Event       `json:"events"`
	Tasks   []SessionTask `json:"tasks"`
}

// Refusal is a write or read the desk refused; Code is stable.
type Refusal struct {
	Code string `json:"code"`
	Msg  string `json:"message"`
}
func (r *Refusal) Error() string // "<code>: <message>"

const (
	CodeUnknownTask     = "unknown-task"
	CodeUnknownProject  = "unknown-project"
	CodeUnknownStep     = "unknown-step"
	CodeUnknownEvent    = "unknown-event"
	CodeEmptyTitle      = "empty-title"
	CodeEmptyText       = "empty-text"
	CodeSecretDetected  = "secret-detected"
	CodeNotAllowed      = "not-allowed"
	CodeBackupOff       = "backup-off"
	CodeHomeUnreachable = "home-unreachable" // exit 3
	CodeScanFailed      = "scan-failed"      // exit 3
)

// AsRefusal reports the Refusal in err's chain, if any.
func AsRefusal(err error) (*Refusal, bool)
```

### `internal/secretscan`

```go
// Scanner reports the name of the pattern text matches, "" when it is clean, and an error when the scan
// could not run. It never returns the matched text.
type Scanner func(ctx context.Context, text string) (pattern string, err error)

func Builtin() Scanner                 // patterns: private-key, aws-access-key, github-token, anthropic-key, slack-token
func Command(argv []string) Scanner    // text on stdin; exit 0 clean, 1 a hit (pattern "command"), else an error
func FromConfig(argv []string) Scanner // empty argv → Builtin(), else Command(argv)
```

Built-in patterns: `-----BEGIN [A-Z ]*PRIVATE KEY-----`; `\b(AKIA|ASIA)[0-9A-Z]{16}\b`; `\bgh[pousr]_[A-Za-z0-9]{36,}\b` and `\bgithub_pat_[A-Za-z0-9_]{22,}\b`; `\bsk-ant-[A-Za-z0-9_-]{20,}\b`; `\bxox[baprs]-[A-Za-z0-9-]{10,}\b`.

### `internal/config`

```go
type Paths struct{ ConfigDir, StateDir, DataDir, CacheDir string } // each ends in /desk

// ResolvePaths reads XDG_CONFIG_HOME, XDG_STATE_HOME, XDG_DATA_HOME, XDG_CACHE_HOME, falling back to
// HOME/.config, HOME/.local/state, HOME/.local/share, HOME/.cache.
func ResolvePaths(getenv func(string) string) Paths

func (p Paths) ConfigFile() string  // ConfigDir/config.toml
func (p Paths) TokenFile() string   // ConfigDir/token
func (p Paths) Socket() string      // StateDir/desk.sock
func (p Paths) LockFile() string    // StateDir/daemon.lock
func (p Paths) DaemonInfo() string  // StateDir/daemon.json
func (p Paths) DaemonLog() string   // StateDir/daemon.log
func (p Paths) Outbox() string      // StateDir/outbox.jsonl
func (p Paths) SessionsDir() string // StateDir/sessions
func (p Paths) BackupState() string // StateDir/backup.json (written and read by internal/backup only)
func (p Paths) DB() string          // DataDir/desk.db
func (p Paths) ScratchRoot() string // DataDir/scratch
func (p Paths) BackupDir() string   // DataDir/backup
func (p Paths) Snapshot() string    // CacheDir/snapshot.json

type Config struct {
	Home       Home       `toml:"home"`
	Client     Client     `toml:"client"`
	Runner     Runner     `toml:"runner"`
	Roots      []Root     `toml:"roots"`
	Agent      Agent      `toml:"agent"`
	Notify     Notify     `toml:"notify"`
	SecretScan SecretScan `toml:"secret_scan"`
	Backup     Backup     `toml:"backup"`
}
type Home struct {
	Listen string `toml:"listen"`
}
type Client struct {
	Home string `toml:"home"`
}
// Runner: Enabled is shown by the board; AgentsMayArm and OnMerged are read by the store. The four
// limits are written by setup and read by the runner (U2).
type Runner struct {
	Enabled       bool   `toml:"enabled"`
	Cap           int    `toml:"cap"`
	MaxRunsPerDay int    `toml:"max_runs_per_day"`
	MaxRunMinutes int    `toml:"max_run_minutes"`
	PollSeconds   int    `toml:"poll_seconds"`
	AgentsMayArm  bool   `toml:"agents_may_arm"` // its comment starts "WARNING:"
	OnMerged      string `toml:"on_merged"`      // "review" | "done"
}
// Root: written by `desk roots`; read by the router (U2).
type Root struct {
	Path      string `toml:"path"`
	About     string `toml:"about"`
	Isolation string `toml:"isolation"` // "" | self | worktree | in-place
}
// Agent: Router and Worker are written by a profile and read by the runner (U2). SessionEnv is read by the CLI.
type Agent struct {
	Router     []string `toml:"router"`
	Worker     []string `toml:"worker"`
	SessionEnv string   `toml:"session_env"`
}
// Notify: written by setup; read by the runner (U2).
type Notify struct {
	Command []string `toml:"command"`
}
type SecretScan struct {
	Command []string `toml:"command"`
}
type Backup struct {
	GitRemote string `toml:"git_remote"`
}

func Default() Config                   // runner off, cap 1, 20 runs a day, 180 minutes, poll 30, on_merged "review"
func Load(path string) (Config, error)  // a missing file is Default(), nil; unknown keys and bad values are errors
func (c Config) Save(path string) error // 0600, written to a temp file then renamed; keeps the WARNING comment
func (c Config) Validate() error        // on_merged, isolation values, listen and client.home as host:port, listen never a wildcard host
func (c Config) IsClient() bool         // Client.Home != ""

// AddRoot adds a root or, when one with that path exists, replaces it. The path must be absolute.
func (c *Config) AddRoot(r Root) error
// RemoveRoot removes the root with that path; an unknown path is an error.
func (c *Config) RemoveRoot(path string) error

// The token: one file, 0600, 32 random bytes as hex.
func ReadToken(p Paths) (string, error)       // a missing file is an error that wraps fs.ErrNotExist
func WriteToken(p Paths, token string) error
func RotateToken(p Paths) (string, error)     // mints a new token, writes it, returns it
```

### `internal/store`

```go
type Options struct {
	Scanner      secretscan.Scanner // nil → secretscan.Builtin()
	Now          func() time.Time   // nil → time.Now
	AgentsMayArm bool
	OnMerged     model.Status       // "" → model.StatusReview
}

func Open(path string, o Options) (*Store, error) // creates the parent dir 0700 and the file 0600; WAL; applies migrations
func (s *Store) Close() error

// Actor is who makes a write. A non-empty Session means an agent.
type Actor struct {
	Session string     `json:"session,omitempty"`
	Run     int64      `json:"run,omitempty"`
	TS      *time.Time `json:"ts,omitempty"` // set only by an outbox replay; nil → Options.Now()
}
func (a Actor) Who() model.Who

// The inputs are the payload plus what sits on the event itself.
type AddTaskInput struct {
	model.TaskData
	Tags []string `json:"tags,omitempty"`
}
type NoteInput struct {
	model.NoteData
	Task int      `json:"task,omitempty"`
	Tags []string `json:"tags,omitempty"`
}
type DecisionInput struct {
	model.DecisionData
	Task int      `json:"task,omitempty"`
	Tags []string `json:"tags,omitempty"`
}

type Filter struct {
	Statuses []model.Status `json:"statuses,omitempty"` // empty → the five live statuses
	Project  *string        `json:"project,omitempty"`  // nil → every project; "" → tasks with no project
	Archived bool           `json:"archived,omitempty"` // true → archived tasks only
	All      bool           `json:"all,omitempty"`      // every task, any status, archived or not
}
// Match reports whether t passes the filter. ListTasks and the API client both decide with it.
func (f Filter) Match(t model.Task) bool
// Live reports whether the filter only narrows the live board: not All, not Archived, no status but the five live ones.
func (f Filter) Live() bool

type TaskDetail struct {
	Task    model.Task    `json:"task"`
	History []model.Event `json:"history"` // every event of this task, oldest first
}

func (s *Store) AddTask(ctx context.Context, a Actor, in AddTaskInput) (model.Task, error)
func (s *Store) SetTask(ctx context.Context, a Actor, number int, p model.Patch) (model.Task, error)
func (s *Store) Step(ctx context.Context, a Actor, number int, op model.StepOp) (model.Task, error)
func (s *Store) Note(ctx context.Context, a Actor, in NoteInput) (model.Event, error)
func (s *Store) Decide(ctx context.Context, a Actor, in DecisionInput) (model.Event, error)
func (s *Store) Merged(ctx context.Context, a Actor, in model.MergedData) (model.Event, error)
func (s *Store) Compacted(ctx context.Context, a Actor) (model.Event, error)
func (s *Store) Continues(ctx context.Context, a Actor, from string) (model.Event, error)

func (s *Store) ListTasks(ctx context.Context, f Filter) ([]model.Task, error) // ordered by number
func (s *Store) GetTask(ctx context.Context, number int) (TaskDetail, error)
func (s *Store) SessionEvents(ctx context.Context, session string) (model.SessionData, error)
func (s *Store) ListRuns(ctx context.Context) ([]model.Run, error)
func (s *Store) CountByStatus(ctx context.Context) (map[model.Status]int, error) // live tasks only
func (s *Store) ExportEvents(ctx context.Context, w io.Writer) (int, error)       // one JSON event per line, by id; returns the count
```

### `internal/journal`

```go
type Line struct {
	EventID  int64        // 0 for a task line
	Task     int          // 0 when the line is about no task
	TS       time.Time
	Scope    string       // a branch name, or "session"
	Text     string
	Ref      string
	Status   model.Status // task lines
	Who      model.Who    // decision lines
	Tags     []string     // tags other than branch:<b>
	Replaces int64        // decision lines
	Hidden   bool         // true only in a view built with all=true, on a line the default view leaves out
}
type View struct {
	Session   string
	Work      []Line
	Todo      []Line
	Decisions []Line
}

// Build returns the session's view. With all=false, hidden lines are left out.
func Build(data model.SessionData, all bool) View
func (v View) Markdown() string
```

### `internal/backup`

```go
type Result struct {
	Events    int  `json:"events"`
	Committed bool `json:"committed"`
	Pushed    bool `json:"pushed"`
}

// Run exports every event to <BackupDir>/events.jsonl, commits when the file changed, pushes to the remote's
// main branch, and records the time of the run in the backup state file.
func Run(ctx context.Context, st *store.Store, p config.Paths, remote string) (Result, error)
// Due reports whether no run is recorded or the last recorded run is over 24 hours before now.
func Due(p config.Paths, now time.Time) bool
```

### `internal/api`

```go
const (
	MethodTasksList    = "tasks.list"
	MethodTasksGet     = "tasks.get"
	MethodTasksAdd     = "tasks.add"
	MethodTasksSet     = "tasks.set"
	MethodTasksSteps   = "tasks.steps"
	MethodEventsAppend = "events.append"
	MethodSessionView  = "session.view"
	MethodRunsList     = "runs.list"
	MethodStatus       = "status"
	MethodBackupRun    = "backup.run" // unix socket only
)

// AppendRequest carries one journal event. Exactly the field its Kind names is set; any other pairing is a 400.
type AppendRequest struct {
	Actor    store.Actor          `json:"actor"`
	Kind     model.Kind           `json:"kind"` // note | decision | merged | compacted | continues
	Note     *store.NoteInput     `json:"note,omitempty"`
	Decision *store.DecisionInput `json:"decision,omitempty"`
	Merged   *model.MergedData    `json:"merged,omitempty"`
	From     string               `json:"from,omitempty"` // continues
}
type Status struct {
	Version   string               `json:"version"`
	Listen    string               `json:"listen"`
	StartedTS time.Time            `json:"started_ts"`
	RunnerOn  bool                 `json:"runner_on"`
	Tasks     map[model.Status]int `json:"tasks"`
}
type TaskList struct {
	Tasks      []model.Task `json:"tasks"`
	Offline    bool         `json:"offline"`
	SnapshotTS *time.Time   `json:"snapshot_ts"` // set only when Offline
}

type ServerOptions struct {
	Store     *store.Store
	Config    config.Config
	Paths     config.Paths // the token is read with config.ReadToken on each TCP request, so a rotation needs no restart
	StartedTS time.Time
	Backup    func(ctx context.Context) (backup.Result, error) // nil → backup.run refuses backup-off
}
func NewServer(o ServerOptions) *Server
// Handler serves the API. trusted=true is the unix socket: no token. trusted=false requires the bearer token
// and does not serve backup.run.
func (s *Server) Handler(trusted bool) http.Handler

type ClientOptions struct {
	Paths   config.Paths
	Config  config.Config
	Timeout time.Duration            // 0 → 5s
	Spawn   func(config.Paths) error // starts the daemon on a home; nil → never
}
func NewClient(o ClientOptions) *Client

// ListTasks keeps the snapshot whole: for a filter with f.Live() it asks the home for the zero Filter, writes
// the snapshot, and returns the tasks f matches. With the home unreachable it answers such a filter from the
// snapshot, marked Offline. Any other filter is sent as it is and is never answered offline.
func (c *Client) ListTasks(ctx context.Context, f store.Filter) (TaskList, error)
func (c *Client) GetTask(ctx context.Context, number int) (store.TaskDetail, error)
func (c *Client) AddTask(ctx context.Context, a store.Actor, in store.AddTaskInput) (model.Task, error)
func (c *Client) SetTask(ctx context.Context, a store.Actor, number int, p model.Patch) (model.Task, error)
func (c *Client) Step(ctx context.Context, a store.Actor, number int, op model.StepOp) (model.Task, error)
// Append sends one journal event. queued=true means the home was unreachable and the event is in the outbox.
func (c *Client) Append(ctx context.Context, r AppendRequest) (ev model.Event, queued bool, err error)
func (c *Client) SessionView(ctx context.Context, session string) (model.SessionData, error)
func (c *Client) ListRuns(ctx context.Context) ([]model.Run, error)
// Status never starts the daemon.
func (c *Client) Status(ctx context.Context) (Status, error)
func (c *Client) Backup(ctx context.Context) (backup.Result, error)
// Flush forwards the outbox in order and returns how many entries it sent. It stops at the first failure.
func (c *Client) Flush(ctx context.Context) (int, error)
```

HTTP mapping: `POST /v1/<method>` with a JSON body. 200 with the result; 409 with `{"code","message"}` for a refusal; 400 for a body that does not parse, an unknown method, or an `AppendRequest` whose set field does not match its kind; 401 for a missing or wrong token; 413 for a body over 1 MiB; 500 otherwise. Request bodies: `tasks.list` is a `store.Filter`; `tasks.get` is `{"number":n}`; `tasks.add` is `{"actor":…,"input":AddTaskInput}`; `tasks.set` is `{"actor":…,"number":n,"patch":Patch}`; `tasks.steps` is `{"actor":…,"number":n,"op":StepOp}`; `events.append` is an `AppendRequest`; `session.view` is `{"session":"…"}`; `runs.list`, `status`, `backup.run` are `{}`. The handlers share one decode, call, encode adapter; they are not ten copies of it.

### `internal/daemon`

```go
// Info is written once, at start, to the daemon info file.
type Info struct {
	PID       int       `json:"pid"`
	Version   string    `json:"version"`
	StartedTS time.Time `json:"started_ts"`
	Socket    string    `json:"socket"`
	Listen    string    `json:"listen"` // the address actually bound, "" when local only
}

var ErrAlreadyRunning = errors.New("daemon already running")
var ErrClient = errors.New("this machine is a client; it runs no daemon")

type Instance struct{ /* unexported */ }
// Start takes the lock, opens the store, serves the socket (and the TCP listener when configured), writes the
// info file, and starts the backup tick. It returns ErrAlreadyRunning when the lock is held and ErrClient on a client.
func Start(ctx context.Context, p config.Paths, c config.Config) (*Instance, error)
func (i *Instance) Listen() string // the TCP address bound, "" when none
func (i *Instance) Close() error   // stops serving, closes the store, removes the socket and the info file, releases the lock

// Run is Start, then wait for SIGINT/SIGTERM or ctx, then Close.
func Run(ctx context.Context, p config.Paths, c config.Config) error
func ReadInfo(p config.Paths) (Info, error)
// Stop signals the running daemon and waits for the lock to be free. It signals only when the lock is held,
// so a stale info file never gets another process killed. No daemon is not an error.
func Stop(p config.Paths, timeout time.Duration) error
// Spawn starts `<this executable> daemon run` detached, output to the daemon log, and waits up to 5s for the socket.
func Spawn(p config.Paths) error
```

Whether a daemon is alive is one question with one answer: the socket answers a `status` call. Nothing writes a periodic heartbeat.

### `internal/testutil` (imported only by tests)

A test that imports `testutil` is in the external test package (`package store_test`, `package api_test`, …); `testutil` imports `api` and `daemon`, so an internal test package would be an import cycle. Every slice's tests are external-package tests.

```go
// Machine is one machine's four XDG directories under a short temp dir.
type Machine struct{ Paths config.Paths }
func NewMachine(t testing.TB) *Machine
// Getenv returns a lookup that answers the XDG variables for this machine, then extra, else "".
func (m *Machine) Getenv(extra map[string]string) func(string) string

type HomeOptions struct {
	Config config.Config // the zero value means config.Default()
	Listen bool          // also serve TCP on 127.0.0.1, any free port, with a fresh token
}
// Home is a desk home running in this process on a Machine. It stops at test cleanup.
type Home struct {
	*Machine
	Addr  string // host:port when Listen
	Token string
}
func StartHome(t testing.TB, o HomeOptions) *Home
func (h *Home) Stop()                // the home goes down; its files stay
func (h *Home) Restart(t testing.TB) // up again on the same socket and address
func (h *Home) Client() *api.Client  // a client on the home machine itself (unix socket)

// NewClientMachine returns a second machine set up as a client of h, through config.Save and config.WriteToken.
func NewClientMachine(t testing.TB, h *Home) *Machine
func ClientFor(m *Machine) *api.Client
```

### `internal/setup`

```go
type Options struct {
	Paths    config.Paths
	Getenv   func(string) string
	Profile  string // "" | "claude-code"
	Listen   string // "" → local only
	Runner   *bool  // nil → leave as is (false on a new config)
	SkillDir string // "" → write no skill file; else <SkillDir>/desk/SKILL.md
	Force    bool   // replace another program's prefix+t / prefix+a bindings
	NoHerdr  bool   // leave herdr's config alone
	Out      io.Writer
}
// Run writes the config (keeping an existing one's values), mints the token when Listen is set and none exists,
// creates the scratch root, applies the profile, writes the skill, and writes the herdr keys. It prints one line
// per thing it wrote or skipped. It never prompts.
func Run(ctx context.Context, o Options) error

// WriteHerdrKeys edits herdr's config text. It returns the new text, the keys it bound, and the keys it left
// because another binding holds them. force replaces those bindings.
func WriteHerdrKeys(configText string, force bool) (out string, bound []string, skipped []string)

// ClientAdd checks home with the token (a status call), then writes [client] home and the token file.
func ClientAdd(ctx context.Context, p config.Paths, home, token string) error
```

herdr's config file is `<XDG_CONFIG_HOME or HOME/.config>/herdr/config.toml`; `internal/setup` finds it with an unexported function.

### `internal/cli`

```go
type Env struct {
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Getenv   func(string) string
	Cwd      string
	StdinTTY bool
	Spawn    func(config.Paths) error // starts the daemon; nil → never (tests)
}
// Run runs one desk command and returns its exit code. args excludes the program name.
func Run(ctx context.Context, args []string, env Env) int
```

The session hook is a command like any other (`internal/cli/hook.go`), tested through `Run`. There is no hook package.

### The command line (the contract `internal/cli` implements and the hand test drives)

Task ids: `T12`, `t12`, and `12` name the same task. Exit codes: 0 done or already true; 1 refused; 2 usage; 3 store or home I/O (`home-unreachable`, `scan-failed`, and any other failure to reach or read the home). A refusal prints `desk <command>: <code>: <message>` on stderr and nothing on stdout. Every command that reads or changes tasks takes `--json`.

The caller's session is the first of: `--session <id>`, the `DESK_SESSION` variable, the variable named by `[agent] session_env`. The run id is `DESK_RUN` when it is a positive integer.

| command | does | stdout |
|---|---|---|
| `desk` | the static board: live tasks under `NEEDS YOU` (blocked, review), `IN MOTION` (started), `ON DECK` (ready, then open) | first line `desk · home · runner on\|off`, or `desk · offline (snapshot <age>)` when served from the snapshot |
| `desk add -t <title> [-n <notes>] [-p <project>\|--desk] [--thread <name>] [--status <s>] [--tag <t>]… [--branch <b>]` | creates a task. No `-p` and no `--desk`: the project is the main checkout of the git repo the cwd is in, else none. `-p` takes an absolute directory (resolved to its main checkout when it is a git repo) or the bare name of one known project. `--branch b` adds the tag `branch:b` | `T<n>`; with `--json` the task |
| `desk list [--ready\|--open\|--done\|--archived\|--all] [-p <project>\|--desk]` | lists tasks; default the five live statuses, every project | one line per task: `T<n>  <status>  <title>` then `#<thread>` and the project's base name when set; `--json` prints a `TaskList` |
| `desk show <task>` | one task with its steps and history | text; `--json` prints a `TaskDetail` |
| `desk set <task> [<status>] [--thread <t>] [--root <r>] [--isolation <i>] [--model <m>] [--archive\|--unarchive] [--ref <ref>] [--merged]` | patches fields. `review --merged` writes the status `runner.on_merged` names | `T<n> <status>`; `--json` the task |
| `desk edit <task> [--title <t>] [--notes <n>]` | replaces the title or the notes | `T<n>` |
| `desk steps <task> add <text>` · `toggle <id>` · `rename <id> <text>` · `remove <id>` | step ids are `s1`, `s2`, … per task, never reused | the task's steps, one per line: `s1 [ ] text` |
| `desk note <text> [--task <task>] [--ref <ref>] [--branch <b>] [--tag <t>]…` | appends a note | `e<id>`, or `queued` when the home is unreachable (exit 0, and stderr says it will be forwarded) |
| `desk note --merged --branch <b> [--pr <n>] [--sha <sha>] [<text>]` | appends a `merged` event for the branch | same |
| `desk decide <text> [--tag <k:v>]… [--replaces e<id>] [--task <task>]` | appends a decision | same |
| `desk session [<id>] [--md] [--all] [--continues <old-id>]` | prints the session's view as markdown (`--md` is the default; `--json` prints the `View`). `--continues` first appends a `continues` event. No id: the caller's session | the view |
| `desk capture` | reads one line from stdin; words starting `#` set the thread, `@` the project (a bare name), the rest is the title; an empty line adds nothing | `T<n>` |
| `desk daemon [run]` · `stop` · `restart` · `status` | `run` in the foreground. Already running: prints `desk daemon: already running (pid <n>)`, exit 0. On a client: prints `desk daemon: this machine is a client of <home>; nothing to run`, exit 0. `status` asks the socket and prints the `Status` as JSON, exit 0 when it answers, 1 when not; it never starts a daemon | |
| `desk token [show]` · `rotate` | prints the token | the token |
| `desk client add <host:port> [--token-file <path>]` | reads the token from the file or from stdin, never from an argument; checks the home answers; then writes the config and the token | `client of <host:port>` |
| `desk roots [list]` · `add <path> [--about <a>] [--isolation <i>]` · `remove <path>` | edits `[[roots]]` | one line per root; `--json` an array |
| `desk setup [--profile claude-code] [--listen <host:port>] [--runner on\|off] [--skill-dir <dir>] [--force] [--no-herdr]` | see `setup.Run`; never prompts; a `--listen` that `Validate` refuses is a usage error (exit 2) and writes nothing | one line per thing written or skipped |
| `desk hook start --format claude-code` | reads the hook's JSON on stdin (deliverable 23) | first line `desk journal for this session: <path>`, then two lines on how to record |
| `desk backup` | runs the backup now | `backup: <n> events, committed\|unchanged, pushed` |
| `desk version` | | `desk <version>` |

Reads while the home is unreachable: bare `desk`, and `desk list` with `--ready`, `--open`, `-p`, `--desk`, or no flag (the filters with `Filter.Live()`), answer from the snapshot, exit 0, with the offline first line (bare `desk`) or `"offline": true` (`--json`) and a line on stderr. Every other read and every task write exits 3 with `home-unreachable`. Before any call, the client forwards its outbox.

On a home whose daemon is not running, a command starts it (`Env.Spawn`) and waits for the socket. `desk daemon …` and `desk version` never start it. `desk daemon stop` exits 0 whether or not one was running; `desk daemon restart` is stop, then start detached.

## Target files

- `embed.go`
- `cmd/desk/**`
- `internal/version/**`
- `internal/model/**`
- `internal/secretscan/**`
- `internal/config/**`
- `internal/store/**`
- `internal/journal/**`
- `internal/api/**`
- `internal/daemon/**`
- `internal/backup/**`
- `internal/testutil/**`
- `internal/setup/**`
- `internal/cli/**`
- `profiles/claude-code/skills/**` — the skill text
- `profiles/claude-code/.claude-plugin/**`
- `profiles/claude-code/hooks/**`
- `.claude-plugin/marketplace.json`
- `herdr-plugin.toml`
- `scripts/fetch-or-build.sh`
- `scripts/open-pane.sh`
- `scripts/coverage.sh`
- `scripts/e2e/**` — the hand-test scripts
- `.goreleaser.yml`
- `.github/workflows/**`
- `README.md`
- `THIRD_PARTY_NOTICES.md`
- `.claude/build-steps.toml` — the checks gain the coverage gate and shellcheck
- `.claude/build/notes.md` — the builders, hand-tester, and test-author sections

## Why the parts wait

A part's `files:` names its files one by one, because a test file sits in the same folder as the code it tests and belongs to a slice. A builder that needs one more non-test file in its own packages adds it and says so in its report.

P5 waits for P4 because `setup.ClientAdd` calls `api.Client.Status`. P2 and P7 wait for P1 so their trees hold the notes sections they read first.

## Parts

- P1 · the hand-test scripts and the repo's build notes
  - model: session — the brief fixes the command line, so each script is written from it before any code exists
  - files: scripts/e2e/**, .claude/build/notes.md
  - test files: none
  - deliverables: 1, 2
  - after: none
  - tests: none
- P2 · the foundation: model, secret scan, config (with the token and the roots), store
  - model: opus — Design 1, 3, 4, and 7 bind these packages
  - files: internal/version/version.go, internal/model/model.go, internal/model/event.go, internal/model/refusal.go, internal/secretscan/scan.go, internal/config/paths.go, internal/config/config.go, internal/config/token.go, internal/store/store.go, internal/store/schema.go, internal/store/tasks.go, internal/store/events.go, internal/store/queries.go
  - test files: none
  - deliverables: 3, 4, 5, 6, 7, 8, 9
  - after: P1
  - tests: W1, W2, W3
- P3 · the journal view
  - model: sonnet — deliverable 10 states every rule; no choice is left open
  - files: internal/journal/view.go, internal/journal/markdown.go
  - test files: none
  - deliverables: 10
  - after: P2
  - tests: W4
- P4 · the API, the daemon, the backup, and the test home
  - model: opus — Design 2 and 8 bind the API, and it holds the token check
  - files: internal/api/wire.go, internal/api/server.go, internal/api/client.go, internal/api/offline.go, internal/daemon/daemon.go, internal/daemon/spawn.go, internal/backup/backup.go, internal/testutil/testutil.go
  - test files: none
  - deliverables: 11, 12, 13, 14, 15
  - after: P2
  - tests: W5, W6, W7
- P5 · setup, the herdr keys, joining a home, and the skill
  - model: sonnet — the deliverables name each file written and each rule of the key edit
  - files: internal/setup/setup.go, internal/setup/herdrkeys.go, embed.go, profiles/claude-code/skills/desk/SKILL.md
  - test files: none
  - deliverables: 16, 17, 18, 19
  - after: P4
  - tests: W8
- P6 · the command line, with the session hook
  - model: opus — Design 6 binds the CLI, and it is the surface every claim drives
  - files: cmd/desk/main.go, internal/cli/cli.go, internal/cli/tasks.go, internal/cli/board.go, internal/cli/journal.go, internal/cli/admin.go, internal/cli/hook.go, THIRD_PARTY_NOTICES.md
  - test files: none
  - deliverables: 20, 21, 22, 23, 24
  - after: P3, P5
  - tests: W9, W10
- P7 · packaging: the herdr bridge, the Claude Code plugin, the release config, the docs
  - model: sonnet — static files whose content the deliverables state
  - files: herdr-plugin.toml, scripts/fetch-or-build.sh, scripts/open-pane.sh, scripts/coverage.sh, profiles/claude-code/.claude-plugin/plugin.json, profiles/claude-code/hooks/hooks.json, .claude-plugin/marketplace.json, .goreleaser.yml, .github/workflows/ci.yml, .github/workflows/release.yml, README.md, .claude/build-steps.toml
  - test files: scripts/fetch_or_build_test.go
  - deliverables: 25, 26, 27, 28, 29, 30
  - after: P1
  - tests: builder

## Test slices

- W1 · plain · the store's task writes and reads
  - files: internal/store/tasks_test.go
  - covers: 5, 6
  - under test: Open, AddTask, SetTask, Step, ListTasks, GetTask, Filter.Match
- W2 · plain · the store's policy and journal events
  - files: internal/store/policy_test.go, internal/store/events_test.go
  - covers: 7, 8
  - under test: Note, Decide, Merged, Compacted, Continues, SessionEvents, ExportEvents
- W3 · plain · the model helpers, the secret scan, and the config with its token and roots
  - files: internal/model/model_test.go, internal/secretscan/scan_test.go, internal/config/config_test.go
  - covers: 3, 4, 9
  - under test: ParseStatus, AsRefusal, ValidSessionID, Builtin, Command, ResolvePaths, Load, Save, Validate, AddRoot, RemoveRoot, RotateToken
- W4 · plain · the journal's hide rules
  - files: internal/journal/view_test.go
  - covers: 10
  - under test: Build, Markdown
- W5 · plain · the API server: methods, status codes, the token
  - files: internal/api/server_test.go
  - covers: 11
  - under test: NewServer, Handler
- W6 · plain · the API client: transport, refusals, snapshot, outbox
  - files: internal/api/client_test.go
  - covers: 12, 15
  - under test: NewClient, ListTasks, Append, Flush, StartHome, NewClientMachine
- W7 · plain · the daemon's lifecycle and the backup
  - files: internal/daemon/daemon_test.go, internal/backup/backup_test.go
  - covers: 13, 14
  - under test: Start, Close, ReadInfo, Stop, backup.Run, backup.Due
- W8 · plain · setup, the herdr keys, joining a home, and the skill
  - files: internal/setup/setup_test.go, internal/setup/herdrkeys_test.go
  - covers: 16, 17, 18, 19
  - under test: setup.Run, WriteHerdrKeys, ClientAdd, Skill
- W9 · plain · the task commands (add, list, show, set, edit, steps, capture, bare desk) and the offline reads
  - files: internal/cli/tasks_test.go, internal/cli/offline_test.go
  - covers: 20
  - under test: cli.Run
- W10 · plain · the journal commands (note, decide, session), the admin commands (daemon, token, client, roots, setup, backup, version), and the hook
  - files: internal/cli/journal_test.go, internal/cli/admin_test.go, internal/cli/hook_test.go
  - covers: 21, 22, 23
  - under test: cli.Run (journal, admin, hook)

## How the hand test runs

Each claim is one script under `scripts/e2e/`. A script builds the binary into a temp dir, gives each machine its own XDG directories there, runs real `desk` processes against a real daemon, prints what it ran and saw, and ends with `E2E PASS` or a non-zero exit. "The client" is a second set of directories that reaches the home over TCP on 127.0.0.1 with the token.

## Hand test

- H1 · a task added on the home shows on a client in one call
  - run: `bash scripts/e2e/h01-add-list.sh`
  - pass: exit 0; the output holds `T1` from the home's add, a line `client sees: {"number":1,"title":"first task","status":"open"}`, and `E2E PASS`
- H2 · with the home down, a client's add exits 3 with home-unreachable and bare desk shows the offline banner
  - run: `bash scripts/e2e/h02-offline.sh`
  - pass: exit 0; the output holds `exit=3`, `home-unreachable`, a board first line with `offline (snapshot`, and `E2E PASS`
- H3 · a note written on an offline client succeeds and appears on the home after it returns
  - run: `bash scripts/e2e/h03-outbox.sh`
  - pass: exit 0; the output holds `queued`, then the home's session view holding the note's text, and `E2E PASS`
- H4 · an agent cannot set ready or done; a person can; agents_may_arm lifts ready only
  - run: `bash scripts/e2e/h04-arming.sh`
  - pass: exit 0; the output holds `not-allowed` three times before the line `config change: agents_may_arm = true`, then `agent ready allowed with agents_may_arm`, then `not-allowed` for done, and `E2E PASS`
- H5 · a note holding an AWS key is refused and the key is neither stored nor echoed
  - run: `bash scripts/e2e/h05-secrets.sh`
  - pass: exit 0; the output holds `secret-detected`, `aws-access-key`, `key not echoed`, `key not in store`, the external scanner case `scan-failed` with `exit=3`, the always-hit scanner case with `exit=1`, and `E2E PASS`
- H6 · a session's view hides and shows lines as the journal rules say
  - run: `bash scripts/e2e/h06-journal.sh`
  - pass: exit 0; the output holds `view after one compaction ok`, `view after two compactions ok`, `view after merge ok`, `--all shows hidden lines ok`, and `E2E PASS`
- H7 · the session hook prints the view's path on startup and resume, records a compaction, and does nothing when switched off
  - run: `bash scripts/e2e/h07-hook.sh`
  - pass: exit 0; the output holds `startup ok`, `resume ok`, `compact ok`, `off ok`, `bad id refused`, and `E2E PASS`
- H8 · the install script installs a checksum-verified prebuilt binary, refuses a bad checksum, and builds from source when no release matches
  - run: `bash scripts/e2e/h08-install.sh`
  - pass: exit 0; the output holds `prebuilt ok`, `checksum mismatch fell back ok`, `source build ok`, `no go, no release: clear error ok`, and `E2E PASS`
- H9 · the release config builds the four binaries and their checksums
  - run: `bash scripts/e2e/h09-release.sh`
  - pass: exit 0; the output lists archives for darwin and linux on arm64 and amd64, `checksums.txt`, a rendered formula, and `E2E PASS`
- H10 · one daemon per home: a second start is a clean no-op, a command starts the daemon when it is down, stop removes the socket
  - run: `bash scripts/e2e/h10-daemon.sh`
  - pass: exit 0; the output holds `already running`, `stop ok`, `autostart ok`, `restart ok`, `client no-op ok`, and `E2E PASS`
- H11 · the TCP listener refuses a missing token, a wrong token, and a rotated-out token, and never serves the backup method
  - run: `bash scripts/e2e/h11-token.sh`
  - pass: exit 0; the output holds `no token 401`, `wrong token 401`, `good token 200`, `old token 401 after rotate`, `backup.run not served on tcp`, `wildcard listen refused`, and `E2E PASS`
- H12 · setup writes the config with its warning, the scratch root, and the herdr keys only where they are free
  - run: `bash scripts/e2e/h12-setup.sh`
  - pass: exit 0; the output holds `config 0600 ok`, `warning comment ok`, `scratch root ok`, `keys written ok`, `taken keys left ok`, `force replaced ok`, `backup file ok`, `no ctrl+d ok`, `profile templates ok`, and `E2E PASS`
- H13 · a backup exports every event and pushes it to the configured remote
  - run: `bash scripts/e2e/h13-backup.sh`
  - pass: exit 0; the output holds `backup-off refused ok`, `pushed ok`, the remote's `events.jsonl` line count equal to the event count, `second run unchanged ok`, and `E2E PASS`
- H14 · the Claude Code plugin and its marketplace file pass Claude Code's own validator
  - run: `bash scripts/e2e/h14-claude-plugin.sh`
  - pass: exit 0; the output holds `plugin valid`, `marketplace valid`, `skill matches the embedded text`, and `E2E PASS`
- H15 · herdr accepts the plugin manifest
  - run: `bash scripts/e2e/h15-herdr-manifest.sh`
  - pass: exit 0; the output holds the linked plugin's id `desk` with its three actions, `unlinked ok`, and `E2E PASS`
- H16 · a real Claude Code session with the plugin loaded gets the journal path at start
  - run: `bash scripts/e2e/h16-claude-session.sh`
  - pass: exit 0; the output holds `hook ran: view file exists`, `session knows the path`, and `E2E PASS`

## Deliverables

Before, for every line: the file or behaviour does not exist.

1. `scripts/e2e/lib.sh` and `scripts/e2e/h01-add-list.sh` … `h16-claude-session.sh` exist, one per hand-test claim, each ending `E2E PASS` only when its checks hold; `shellcheck scripts/e2e/*.sh` is clean. (§Hand test)
2. `.claude/build/notes.md` has `## builders`, `## hand-tester`, and `## test-author` sections saying how this repo is built, hand-tested, and tested. (§Parts P1)
3. `internal/model` holds the types, constants, and helpers of `## Public surface`, with no import outside the standard library; `internal/version.Version` is `"dev"` unless set at link time. (spec: Store; §Public surface)
4. `internal/secretscan`: `Scanner` is a func type; `Builtin()` finds each of the five patterns and returns the pattern's name, never the match; `Command(argv)` passes the text on stdin and maps exit 0 to clean, 1 to the pattern `command`, anything else or a failed start to an error; `FromConfig` picks between them. (spec: Store; §Corrections 9)
5. `internal/store.Open` creates the database (parent 0700, file 0600, WAL) with the five tables `events`, `tasks`, `steps`, `runs`, `sessions` as the spec's Store block writes them, changed only by corrections 11 and 12, through a numbered migration; opening an existing file changes nothing. (spec: Store)
6. `AddTask`, `SetTask`, and `Step` each write one event and update `tasks`/`steps` in the same transaction; task numbers count up from 1; a patch that changes nothing writes no event and returns the task; step ids are `s1`, `s2`, … and are never reused; `ListTasks` returns the tasks `Filter.Match` accepts, and `Filter.Live` is true only for a filter that narrows the live board; `GetTask` returns the task and its events; `unknown-task`, `unknown-step`, `empty-title`, `unknown-project` are returned as `*model.Refusal`; a known project is a project path some existing task carries, a `Project` that is not an absolute path is a bare name, and it resolves to the one known project with that base name, with none or several being `unknown-project`. (spec: Store, CLI)
7. Policy in the write path: an agent actor setting or adding `ready` or `done` gets `not-allowed`, and with `AgentsMayArm` only `ready` is allowed; `review` with `Merged` writes the `OnMerged` status; the text fields of a write (title, notes, thread, step text, note and decision text, ref, tags) are joined and scanned once before the insert, a hit returns `secret-detected` naming the pattern and writes nothing, a scanner error returns `scan-failed` and writes nothing. (spec: The model, Store; Design 4)
8. Journal events and reads: `Note`, `Decide`, `Merged`, `Compacted`, `Continues` each append one event with the actor's session, who, run, and tags; `Continues` also records the link in the `sessions` table; `Decide` with `Replaces` naming no decision event returns `unknown-event`; `SessionEvents` returns the chain's events (cycle-guarded) plus every `merged` event, by id, and each task the chain created with `Created`, `DoneAt`, and `Tags`; `ListRuns` returns the `runs` rows; `ExportEvents` writes every event as one JSON line, by id. (spec: Store, Journal)
9. `internal/config`: `ResolvePaths` follows the XDG variables with the HOME fallbacks; `Load` of a missing file is `Default()`; `Save` writes 0600 through a rename and the saved text keeps a comment starting `WARNING:` directly above `agents_may_arm`; `Validate` refuses a wildcard `listen` host (empty, `0.0.0.0`, `::`), a bad `on_merged`, and a bad isolation; `AddRoot` and `RemoveRoot` edit the roots list; `ReadToken`, `WriteToken`, and `RotateToken` keep the token file at 0600. (spec: Config, Daemon and API)
10. `internal/journal.Build` and `View.Markdown` render Work log, Todo, and Decisions by the rules in §Journal rules below, and each of the nine ported tests has a Go test with the same claim. (spec: Journal; §Corrections 5)
11. `internal/api` server: the ten methods over `POST /v1/<method>` with the status codes of `## Public surface`, through one shared decode, call, encode adapter; on the untrusted handler a request without the exact token (constant-time compare, token read per request) gets 401 and `backup.run` is not served; bodies over 1 MiB get 413; an `AppendRequest` whose set field does not match its kind gets 400; who is derived from the actor's session and no `who` field is read. (spec: Daemon and API; Design 2, 3)
12. `internal/api.Client`: uses the unix socket on a home and TCP with the token on a client; returns a server refusal as `*model.Refusal`; returns `home-unreachable` when the home cannot be reached (after `Spawn` on a home, when set); `ListTasks` with a `Live()` filter asks the home for the zero `Filter`, writes the snapshot, and returns what the filter matches, and when the home is unreachable answers from the snapshot, marked `Offline` with its time; `Status` never starts a daemon; `Append` queues to the outbox when unreachable and `Flush` forwards in order, keeping what was not sent; every call forwards the outbox first. (spec: The model; Design 8; §Corrections 7)
13. `internal/daemon`: `Start` takes an exclusive `flock`, returns `ErrAlreadyRunning` when held and `ErrClient` on a client, removes a stale socket, serves the unix socket (0600) and the configured TCP address, writes the info file once, and each hour runs the backup when it is on and `backup.Due`; `Close` and a signal stop it cleanly and remove the socket and the info file; `Stop` signals only while the lock is held; `Spawn` behaves as `## Public surface` says; a socket path over 104 bytes is an error naming the path. (spec: Daemon and API; §Corrections 13)
14. `internal/backup.Run` writes `events.jsonl`, commits only when it changed, pushes to the remote's `main`, and records the run; `Due` is true with no recorded run or one over 24 hours old; with no remote configured the `backup.run` method refuses `backup-off`. (spec: Store "Backup"; §Corrections 8)
15. `internal/testutil` gives tests a real in-process home on short temp directories, a way to stop and restart it, and a second machine set up as its client. (§Public surface)
16. `setup.Run` writes or updates the config without losing an existing value, mints a 32-byte hex token (0600) when `Listen` is set and no token exists, creates the scratch root as a git repo, writes `<SkillDir>/desk/SKILL.md` when asked, with `Profile: "claude-code"` writes the spec's `router` and `worker` argv arrays and `session_env`, and when herdr has a config file writes `[notify] command` as herdr's notification argv unless one is set; it prints one line per thing written or skipped and never prompts. (spec: Profile, Config, herdr bridge)
17. `WriteHerdrKeys` adds a block fenced by `# >>> desk keys` and `# <<< desk keys` binding `prefix+t` to `desk.open-board` and `prefix+a` to `desk.capture`; a key another binding holds is skipped and reported; with `force` that binding is removed and ours written; a second run changes nothing; `ctrl+d` is never written; `setup.Run` copies the file to `config.toml.desk-bak-<time>` before changing it and does nothing when herdr has no config file. (spec: herdr bridge)
18. `profiles/claude-code/skills/desk/SKILL.md` is desk's own text with a `name: desk` front matter and these rules: agents set `review` or `blocked`, never `ready` or `done`; propose a task with `--thread agent`; read with `--json`; never retry blind (the exit table); `note --ref` for files and PRs; `decide --tag k:v`; `session <id> --md`. `desk.Skill()` returns it. (spec: CLI)
19. `setup.ClientAdd` asks the home for its status with the token, then writes `[client] home` and the token file; it writes nothing when the home does not answer with that token. (spec: Daemon and API, CLI)
20. The task commands (`add`, `list`, `show`, `set`, `edit`, `steps`, `capture`, bare `desk`) follow the command table: stdout shapes, `--json`, exit codes, the refusal line on stderr, the project default from the cwd's main checkout (a worktree resolves to its main checkout), and the offline reads. (spec: CLI)
21. The journal commands (`note`, `note --merged`, `decide`, `session`) follow the command table, including `queued` when the home is unreachable and the session resolution order. (spec: CLI, Journal)
22. The admin commands (`daemon`, `token`, `client add`, `roots`, `setup`, `backup`, `version`) follow the command table; `roots add` makes the path absolute from the cwd; a command on a home with no daemon starts one through `Env.Spawn`; `client add` never takes the token as an argument. (spec: CLI, Daemon and API)
23. `desk hook start --format claude-code` (`internal/cli/hook.go`): `DESK_HOOKS=off` does nothing; `source` `startup`, `resume`, `clear`, `fork` write the view to `<SessionsDir>/<id>.md` (0600) and print `desk journal for this session: <path>` as the first line, then two lines on how to record; `compact` first appends `compacted`; an unreachable home prints one line saying so and exits 0; a session id `model.ValidSessionID` rejects, or another `--format`, exits 2 and writes nothing. (spec: Profile; §Corrections 4, 14)
24. `cmd/desk/main.go` calls `cli.Run` with the process's streams and `daemon.Spawn`; `THIRD_PARTY_NOTICES.md` lists every module in `go.sum`'s build list with its license. (spec: Release)
25. `herdr-plugin.toml` at the repo root: `id = "desk"`, `min_herdr_version = "0.9.0"`, `platforms = ["linux","macos"]`, a `[[build]]` running `scripts/fetch-or-build.sh`, a `[[startup]]` running `desk daemon`, panes `board` (command `desk`) and `capture` (a popup running `desk capture`), and actions `open-board`, `capture`, `restart` (`desk daemon restart`); `scripts/open-pane.sh board` focuses the board pane when it is open and opens it otherwise, and `scripts/open-pane.sh capture` opens the capture popup, both through the herdr binary `HERDR_BIN_PATH` names. (spec: herdr bridge)
26. `scripts/fetch-or-build.sh` downloads the release archive for the manifest's version and this platform (darwin and linux, arm64 and amd64), verifies its SHA-256 against `checksums.txt`, and on any miss builds with `go build`; it installs to `~/.local/bin/desk` when no `desk` is on PATH and says so; the archive is `desk_<version>_<os>_<arch>.tar.gz` holding `desk`, under `<base>/v<version>/`, beside `checksums.txt`; `DESK_REPO_ROOT`, `DESK_VERSION`, `DESK_BASE_URL`, `DESK_OUT` (where the binary is placed), `DESK_INSTALL_DIR`, and `DESK_GO` (the go command) override its defaults; with no release and no go it exits 1 naming go; `scripts/fetch_or_build_test.go` drives the prebuilt path and the fallback path with a stubbed `curl` and `go`. (spec: herdr bridge, Release)
27. `profiles/claude-code/.claude-plugin/plugin.json` (name `desk`), `profiles/claude-code/hooks/hooks.json` (a `SessionStart` hook, matcher `startup|resume|clear|compact|fork`, command `desk hook start --format claude-code`), and `.claude-plugin/marketplace.json` (name `desk`, one plugin `desk` with source `./profiles/claude-code`). (spec: Profile)
28. `.goreleaser.yml` builds darwin and linux for arm64 and amd64 with `CGO_ENABLED=0`, sets `internal/version.Version`, writes `checksums.txt`, and renders a formula for `federbenjamin/homebrew-tap`; `.github/workflows/ci.yml` runs the repo's checks on pull requests and `release.yml` runs goreleaser on a `v*` tag. (spec: Release)
29. `README.md` covers what desk is, the three installs (herdr with Claude Code, herdr with another agent and the three `[agent]` values, no herdr), the home and client model, every command, the config file, and how to run the daemon by hand or under launchd. (spec: Release)
30. `scripts/coverage.sh` runs the tests with coverage and exits 1 when total statement coverage of `./internal/...` (without `internal/testutil`) is under 80% or `internal/store`, `internal/journal`, or `internal/secretscan` is under 90%; `.claude/build-steps.toml`'s `checks` is `go vet ./... && sh scripts/coverage.sh && test -z "$(gofmt -l .)" && shellcheck scripts/*.sh scripts/e2e/*.sh`, and its `exit_checks` is the same line with `go test ./...` in place of `sh scripts/coverage.sh`: a builder's tree holds its part alone, so the coverage gate can only pass once every part and every slice is merged; `ci.yml` runs the `checks` line. (operator instruction; spec: Release)

## Journal rules

`Build` works on ids. Let C be the ids of the chain's `compacted` events and M(b) the ids of every `merged` event for branch b. A line's scope is its `branch:<b>` tag's branch, else `session`.

- **Work log** holds notes, `compacted` markers, and `merged` lines, by id. A note renders `- HH:MMZ [<scope>] <text>`, with `T<n>: ` before the text when it names a task and ` (<ref>)` after it when it has a ref. A marker renders `- HH:MMZ [session] compacted`. A merged event renders `- HH:MMZ [<branch>] merged #<pr> (<sha>)`, leaving out the part it lacks.
- **Todo** holds the chain's tasks, by number: `- [ ] [<scope>] T<n> <title>`, `[x]` when done, and ` (<status>)` after the title when the status is neither `open` nor `done`.
- **Decisions** holds decisions, by id: `- YYYY-MM-DD [<who>] <text>`, then ` #<tag>` per tag, then ` — replaces e<id>` when it replaces one, then ` · e<id>`.
- A `merged` event is shown only when a chain session wrote it or the chain has a note or task on that branch. It is never hidden.
- A note tagged `question` is never hidden. A note on branch b is hidden when some id in M(b) is greater than its id. A session note, and a `compacted` marker, is hidden when two or more ids in C are greater than its id.
- A task that is not done is never hidden. A done task on branch b is hidden when some id in M(b) is greater than its `DoneAt`. A done task with no branch is hidden when some id in C is greater than its `DoneAt`, `question` tag or not.
- A decision that is the last one carrying a given `tunable:<k>` tag is never hidden. Otherwise a decision replaced by a later decision r is hidden when some shown `merged` id or some id in C is greater than r's id. No other decision is ever hidden.
- With `all`, nothing is left out; a line the rules hide is in its own section, in order, with `Hidden` true.
- `Markdown` prints `# Session <id>`, then `## Work log`, `## Todo`, `## Decisions`, each followed by a blank line and its lines; an empty section keeps its heading.

The nine ported tests, by their line in `~/.agents/src/runtime/build/__tests__/sessionLogPrune.test.ts` and the claim each Go test makes:

- `:58` a branch's notes and done tasks are hidden after its merge; another branch's lines and open tasks stay.
- `:74` an open question task and a `question` note are never hidden; a done question task is hidden with its branch.
- `:84` the last decision per `tunable:<k>` is never hidden, even when a later decision replaces it; an earlier one for the same key is hidden once replaced and a merge follows.
- `:95` a task on a branch stays while open or while another branch merges, and is hidden once done and its own branch merges.
- `:130` the merged line appears once, in the Work log, and is never hidden.
- `:141` with `all`, every hidden line appears under its own section, in order.
- `:205` a session note with two later compactions is hidden; one after the previous marker, and every branch note, stays.
- `:219` an older `compacted` marker is hidden like any session note; the newest two stay.
- `:230` with one compaction no session note is hidden, every done task with no branch is (answered questions too), and open tasks, open questions, and decisions never are.

Plus: `replaces` by id (a replaced decision is hidden after the next merge or compaction, and a decision naming a missing id is refused at write), and `continues` (a view includes the sessions it continues, and a cycle ends).

```yaml
description: desk U1 — store, daemon, API, CLI, journal, claude-code profile, herdr bridge, release config
deliverables:
  - name: e2e hand-test scripts, one per claim, shellcheck clean
    covered_by: [judgment]
  - name: build notes gain builders, hand-tester, test-author sections
    covered_by: [judgment]
  - name: internal/model types and helpers, internal/version
    covered_by: [judgment]
  - name: secretscan Scanner func, builtin patterns, command scanner exit mapping
    covered_by: [judgment]
  - name: store.Open creates the five tables through a numbered migration, 0600, WAL
    covered_by: [judgment]
  - name: task writes are one event plus state in one transaction; numbering, no-op patches, step ids, refusals
    covered_by: [judgment]
  - name: write-path policy — not-allowed, agents_may_arm, on_merged, secret scan before insert
    covered_by: [judgment]
  - name: journal events, sessions table, SessionEvents chain, ListRuns, ExportEvents
    covered_by: [judgment]
  - name: config paths, load, save with the WARNING comment, validate, roots, token
    covered_by: [judgment]
  - name: journal Build and Markdown by the hide rules; nine ported tests
    covered_by: [judgment]
  - name: API server — ten methods, status codes, token check, body limit, who derived
    covered_by: [judgment]
  - name: API client — transport, refusals, home-unreachable, snapshot rule in ListTasks, outbox
    covered_by: [judgment]
  - name: daemon — lock, info file, socket, TCP, backup tick, stop, spawn
    covered_by: [judgment]
  - name: backup export, commit when changed, push, Due; backup-off
    covered_by: [judgment]
  - name: testutil in-process home and client machine
    covered_by: [judgment]
  - name: setup.Run — config, token, scratch root, skill, claude-code profile templates
    covered_by: [judgment]
  - name: herdr keys block — only when free, force, idempotent, backup file, never ctrl+d
    covered_by: [judgment]
  - name: the skill text and desk.Skill()
    covered_by: [judgment]
  - name: setup.ClientAdd joins a home only when it answers
    covered_by: [judgment]
  - name: task commands follow the command table, including offline reads
    covered_by: [judgment]
  - name: journal commands follow the command table, including queued
    covered_by: [judgment]
  - name: admin commands follow the command table; autostart; token never an argument
    covered_by: [judgment]
  - name: desk hook start for claude-code — off switch, view file, compact, unreachable home, id check
    covered_by: [judgment]
  - name: cmd/desk main and THIRD_PARTY_NOTICES.md
    covered_by: [judgment]
  - name: herdr-plugin.toml and scripts/open-pane.sh
    covered_by: [judgment]
  - name: fetch-or-build.sh with its hermetic test
    covered_by: [judgment]
  - name: Claude Code plugin files and marketplace.json
    covered_by: [judgment]
  - name: goreleaser config and the two workflows
    covered_by: [judgment]
  - name: README
    covered_by: [judgment]
  - name: coverage gate script and the repo's checks
    covered_by: [judgment]
```

--- brief complete ---
