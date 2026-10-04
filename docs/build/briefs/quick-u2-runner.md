class: R2 — agent (unconfirmed), 2026-10-04
model: opus — the store's arming policy and the runner's spawn path carry `## Design` entries (P1, P3)

# desk U2: the runner and the router

Unit 2 of the plan `~/.claude/plans/task-runner-plugin.md` (v3, approved 2026-10-03). U1 is merged: `docs/build/briefs/quick-u1-core.md` is the spec of what exists, and its `## Rules every part follows` holds here too. A sibling unit, U3 (the board), is built at the same time from the same base and builds no code in this unit's files except where `## Shared with U3` says.

Branch contract: commits on `quick/u2-runner` (builders: on the harness-named branch of your own tree). No push and no PR from a builder; the session pushes at SHIP.

Operator instruction for the whole plan (2026-10-03): "Make sure there is very thorough hand testing and code coverage". It is why this brief carries fourteen hand-test claims and twelve test slices.

## Spec

The plan's U2 section, verbatim.

> ## U2. Runner and router (home only)
>
> Loop every `poll_seconds`: for each armed task (oldest first) while live runs (by `runs.state`) < `cap` and today's runs < `max_runs_per_day`: create the run row (state `routing`), set the task `started` with `run` id; **route** unless the task's `root`/`isolation`/`model` fields are all set: run the `router` argv with the system prompt file and schema from `router/` (overridable in config), task JSON on stdin; schema output `{root: enum(listed roots), isolation: enum, model: enum(config list), reason}`; any invalid output or non-zero exit → task `blocked`, note "router: <reason>", run `failed`. **Spawn**: optional `git worktree add <root>/../<repo>-T<n>` (branch `desk/T<n>-<slug>`, reused if present); `herdr workspace create --cwd <dir> --no-focus --env DESK_TASK=T<n> --env DESK_SESSION=<uuid>`; `herdr pane run <pane> "exec desk worker"`; `desk worker` loads the run, builds the first message in code (title, notes, steps, the task's HISTORY rendered, and "finish with `desk set T<n> review --ref …` or `blocked`"), and execs the `worker` argv with `--session-id <uuid>`. A note records workspace and pane; notify. **Watch** every poll, this home only: find the pane by `agent_session.value == uuid` in `herdr pane list`; `agent_status` `done`/`idle` for two polls → `review` ("session ended without reporting" if the worker wrote nothing); `blocked` → `blocked`; pane gone → `review`; past `max_run_minutes` → `blocked`; every write from a run carries the run id and the daemon ignores status writes from a run that is not the task's current one. `desk runs kill T<n>` (board `k`) kills the pane's process group via `herdr pane process-info` then closes the pane, writes `blocked`. A root with `isolation=self` is exempt from the one-in-place-run-per-root wait. Re-arming a `blocked` task (the user adds a `note` with the answer and presses `n`) starts a new run whose first message includes the history.
>
> No-fire: not armed by a user (unless `agents_may_arm`); cap or daily cap reached; runner disabled; router binary missing (`daemon.json` status `no-router`, notify once); a task whose current run is still live.
>
> Acceptance: an armed task spawns within `2 × poll_seconds` with `DESK_TASK` set and IN MOTION shows it; a `thread=me` task never spawns; a worker that stops working flips to `review` within two polls; `k` leaves no process from that pane alive; the 4th concurrent task waits; a root with `isolation=self` runs two at once; a desk-only project task routes to `scratch`.

From the plan's model section, the lines this unit rests on:

> - **One runner per desk**, on the home. Two machines that should each run their own tasks are two desks.
> - **Arming** is the user's act: a task runs only when a `who=user` event set `status=ready` with thread `agent`. Agents may add tasks and set the `agent` thread (a proposal lands in the inbox with `#agent`), never `ready` or `done`. Config `runner.agents_may_arm = true` lifts this, with a warning comment above it.
> - **Hand-back**: the worker sets `review` or `blocked`; `on_merged = "review" | "done"` (default `review`; the operator sets `done`) decides what a merged PR does when the worker reports it with `--ref <pr-url> --merged`.

From the plan's profile section:

> `setup --profile claude-code` writes `[agent]` templates: `router = ["claude","-p","--safe-mode","--tools","","--system-prompt-file","{system}","--json-schema","{schema}","--max-budget-usd","0.10","--no-session-persistence","--output-format","json"]` (task on stdin; read `structured_output`), `worker = ["claude","--model","{model}","--permission-mode","auto","--session-id","{session}","--","{message}"]`, `session_env = "CLAUDE_CODE_SESSION_ID"`. Templates are argv arrays, never shell; `DESK_HOOKS=off` in the router's env makes `desk hook` a no-op.

From the plan's risks:

> - **Unattended runs spend quota on text nobody reviewed.** Bound: user-only arming by default; sealed router (`--safe-mode`, enum output, no model-written prompt); caps per day and per run; visible pane; notify per spawn.
> - **A stale run overwrites the task.** Bound: run id on every write; the daemon ignores an old run's status writes.

## Corrections to the spec

Each line is a place where this brief departs from the plan text above, or settles what it leaves open. Where the two differ, this section wins.

1. `{schema}` in the router template is the schema's JSON text, not a file path. `claude --json-schema` takes the JSON itself (`claude --help`, 2.1.289). `{system}` is a file path.
2. The model list is `[agent] models` (the claude-code profile writes `["sonnet", "opus"]`). The router's system prompt and schema are replaced through a new table, `[router] system` and `[router] schema`, each a file path, empty for the built-in.
3. The route is saved on the task: after a good route the runner writes the task's `root`, `isolation`, and `model` fields. A re-armed task therefore runs where it ran before and pays for no second router call, and a person can change the fields on the task.
4. The router is skipped when each field is already decided: `root` by the task's field; `isolation` by the task's field or by the configured isolation of that root; `model` by the task's field or because `[agent] models` is empty. A root's configured isolation always wins over the router's pick. The router may pick only `worktree` or `in-place`; `self` comes from a root's config or a task's field.
5. Run states: `routing`, `waiting`, `running` (live: each holds a slot of `cap`) and `ended`, `failed`, `killed` (each sets `ended_ts`). `waiting` is a routed run whose root already has a live in-place run; it starts when that run ends.
6. Past `max_run_minutes` the runner kills the pane's processes and closes the pane, as `desk runs kill` does, then writes `blocked`. Marking the task alone would leave the session spending.
7. `runner.pause` is a new API method with `desk runner pause` and `desk runner resume` (the board's `P` key; agreed with the U3 session). A paused runner starts no run; live runs go on and are still watched. The pause is a state file, so it survives a restart.
8. A second no-fire state, `no-herdr`, when no `herdr` binary is on PATH. The runner's states are `off`, `paused`, `no-herdr`, `no-router`, `on`, in that order of precedence.
9. The pane gets `DESK_RUN=<run id>` beside `DESK_TASK` and `DESK_SESSION`, and the four XDG variables of the daemon's own folders (`Paths.Env()`), so the `desk` in the pane talks to the daemon that started it whatever the pane's shell would set.
10. The pane's command is `exec <absolute path of the running desk binary> worker`, not `exec desk worker`: the pane's shell may have another `desk`, or none, on its PATH.
11. A status write from a run that is not the task's newest run is refused with `stale-run` (exit 1) and writes nothing. The plan says "ignores"; a refusal tells the stale session.
12. Armed means: status `ready`, thread `agent`, not archived, and, unless `agents_may_arm`, the newest event that set the task `ready` was written by a user. To keep that true, an agent may not set the thread `agent` on a task that is `ready` (`not-allowed`) unless `agents_may_arm`.
13. Any change of a task's status away from `started`, by anyone, ends its live run in the same transaction. So a live run always belongs to a `started` task, and "a task whose current run is still live" never needs its own check.
14. `done`/`idle` is counted only on a pane found by its agent session. A pane found only by its pane id has not started its agent yet; it is left alone until the time limit.
15. The runner writes as the user with the run's id and no session (U1 has two writers, `user` and `agent`). A runner-written note carries the tag `runner`; the note that records a route also carries `router`, and the worker's first message leaves out every event tagged `router`, so no router-written text reaches a worker's prompt.
16. `desk runs kill` and `runner.pause` are a person's acts: an actor with a session gets `not-allowed`.
17. The watch runs whether the runner is enabled or not; only starting runs is gated. A run that was live when the runner was switched off is still handed back.
18. `runner.cap`, `runner.max_runs_per_day`, `runner.max_run_minutes`, and `runner.poll_seconds` must each be at least 1; `config.Validate` refuses anything less.
19. The scratch root is always listed last with isolation `in-place`: it has no commit, so a worktree cannot be cut from it.
20. Not built: a notification when a task comes back to `review` or `blocked`. The plan names two notifications (a spawn, and `no-router` once) and both are built.

## Shared with U3

U3 adds, client side only, `api.MethodRunsKill`, `api.MethodRunnerPause`, `(*api.Client).KillRun`, `(*api.Client).PauseRunner`, and the `api.Status` fields `RunnerPaused` and `RunnerCap`. This unit adds the same names with the same signatures and tags (its CLI calls them) and serves them. Whichever PR merges second drops its duplicate. Do not rename them.

## Prior art

The plan's table, with where each reference sits. Reference paths outside the repo are for reading only; no path, host, or name from them may appear in the repo.

| artifact | verdict | reference |
|---|---|---|
| spawn | reuse `cl`'s `workspace create --env`; `exec` per the pane-run finding | `herdr workspace create --help`, `herdr pane run --help` (0.9.1) |
| `internal/herdr` (new) | justified-new: nothing in the repo calls herdr with arguments and reads its JSON. `scripts/open-pane.sh` calls it from shell | `scripts/open-pane.sh` |
| `internal/runner` (new) | justified-new: the daemon has one tick, the backup's (`internal/daemon/daemon.go` `tick`); the runner's loop has its own period and state | `internal/daemon/daemon.go:178` |
| running a configured argv with text on stdin | reuse the shape of `secretscan.Command` (argv from config, stdin, exit code mapped) | `internal/secretscan/scan.go` |
| every `git` call | reuse `gitcmd.Run` (timeout, `GIT_TERMINAL_PROMPT=0`) | `internal/gitcmd/gitcmd.go:52` |
| run rows | reuse `model.Run`, the `runs` table, `store.ListRuns`, `api.MethodRunsList` | `internal/model/model.go:66`, `internal/store/schema.go:49`, `internal/store/queries.go:306` |
| the run id on a write | reuse `store.Actor.Run` and the CLI's `DESK_RUN` | `internal/store/store.go:95`, `internal/cli/cli.go:233` |
| embedding a text file | reuse `embed.go` | `embed.go` |
| atomic 0600 file writes | reuse `config.WriteFileAtomic` | `internal/config/config.go:132` |
| e2e scaffolding | reuse `scripts/e2e/lib.sh` (`on`, `as_agent`, `run`, `wait_for`, `write_config`, `start_daemon`) | `scripts/e2e/lib.sh` |

## Design

Chosen shape, one entry per choice. Each names the files it binds.

1. **The runner is one package behind one port.** `internal/runner` holds the loop, the router call, the spawn, the watch, and the kill. It reaches herdr only through the `runner.Herdr` interface, which `*herdr.Client` satisfies and which tests replace with `herdrtest.Herdr`. The router, the notifier, and git are real child processes named by config, so tests set them by setting config. One unexported helper runs a configured argv with text on stdin and a timeout (the shape of `secretscan.Command`); the router and the notifier both go through it. Binds `internal/runner/**`.
   - Rejected: the loop inside `internal/daemon`. The daemon would grow three times over and its tests would need herdr.
   - Rejected: a port per collaborator (router, notifier, git). Each is already an argv in config; a script in a temp folder is the test double.
2. **Run rows are state, not history.** `runs` rows are written directly, outside the event log; what a person should see of a run (started, routed, handed back, killed) is an ordinary `set` or `note` event that carries the run's id. Every change of a run's state is conditional on the state it leaves (`UpdateRun(id, from, …)`), so a kill and a tick that race cannot both win, and neither needs a lock around a router call. Binds `internal/store/runs.go`, `internal/runner/**`.
   - Rejected: a `run` event kind with the table rebuilt from events. Nothing reads a run's history, and U1's journal would have to learn to skip the kind.
   - Rejected: one runner mutex held across a tick. A router call can take two minutes, and a kill must not wait for it.
3. **Arming and staleness are the store's rules.** `Armed`, the `stale-run` refusal, the thread refusal, and "leaving `started` ends the run" live in the store's write path and its one armed query, where no client can skip them (U1 Design 4). Binds `internal/store/runs.go`, `internal/store/tasks.go`.
4. **The route is data on the task.** The router's answer is checked in code against the listed roots and models, then written to the task's fields; the spawn reads only the run row. The schema is a hint to the router, never the check. Binds `internal/runner/route.go`.
   - Rejected: trusting the schema-constrained output. A router template without schema support would then pick any path.
5. **The worker's prompt is built in code from the task, and every argv template is expanded by one function.** `worker.FirstMessage` renders the title, notes, steps, and history, in a leaf package (`internal/worker`) so the command line does not import the runner. `config.Expand` substitutes a template's placeholders, each element in one pass; the router's, the worker's, and the notifier's argv all go through it, so text in a task can never become a flag or a second placeholder. Binds `internal/worker/**`, `internal/config/config.go`.
6. **A pane is killed by what herdr reports for it.** The kill asks herdr for the pane's foreground process group and processes, signals them (TERM, then KILL after a grace), then closes the pane. The pane is the one whose agent session is the run's; failing that, the one whose pane id and workspace id are both the run's. It never signals pid 0 or 1, the daemon's own pid, or the daemon's own process group. Binds `internal/runner/kill.go`.
   - Rejected: killing by the pane's tty or by a pid desk recorded at spawn. desk never sees the worker's pid; herdr owns the pane.

Assumptions the design rests on:

- herdr 0.9.1's command line, checked with each command's `--help` and `herdr api schema`: `workspace create [--cwd] [--label] [--env K=V]… [--no-focus]` answers `{"result":{"type":"workspace_created","workspace":{"workspace_id":…},"root_pane":{"pane_id":…}}}`; `pane run <PANE_ID> <COMMAND>…`; `pane list` answers `{"result":{"panes":[{"pane_id","workspace_id","agent_status","agent_session":{"value"}|null}]}}` with `agent_status` one of `idle`, `working`, `blocked`, `done`, `unknown`; `pane process-info --pane <ID>` answers `{"result":{"process_info":{"foreground_process_group_id","foreground_processes":[{"pid","name"}],"shell_pid"}}}`; `pane close <pane_id>`; a pane's processes get `HERDR_PANE_ID`.
- claude 2.1.289's flags, checked with `claude --help`: `--safe-mode`, `--tools ""`, `--system-prompt-file <file>`, `--json-schema <schema JSON>`, `--max-budget-usd`, `--no-session-persistence`, `--output-format json`, `--session-id <uuid>`, `--permission-mode auto`, `--model`. `--tools` takes several values, so the template keeps a flag after it. `--system-prompt-file` has no help line of its own (the `--bare` text names `--system-prompt[-file]`); U1's template already uses it and H12 proves the whole argv.
- A router's stdout is one JSON object holding the route either at its top level or under `structured_output` (what `claude -p --output-format json` prints).
- A pane closes when the process `exec`'d into its shell exits. Whether herdr types `pane run`'s words through the pane's shell is not documented; the command is passed as one argument and H11 proves it on the real herdr.
- herdr must recognise the worker as an agent (it reports `agent_session` and `agent_status`). A worker herdr does not know is never seen as idle and is stopped only by the time limit or a kill.
- One daemon, one runner: `Tick` is called from one goroutine. `Kill` and `Pause` may be called from any.

Libraries: none new. No new module in `go.mod`.

Design pass (simplifier, 2026-10-04): `internal/worker`, `config.Expand` in place of three substitutions, `Paths.Env()` in place of a getenv option, the stand-in at `internal/herdr/herdrtest` with a contract test that runs it and `fake-herdr.py` through one table, `Resolve` split from `Apply`, no system-prompt option, and one scan for run rows come from it. One leftover it named is not this unit's: `api.Status.RunnerOn` can be dropped once the board reads `runner_state`.

## Rules every part follows

U1's rules hold (`docs/build/briefs/quick-u1-core.md` §Rules every part follows). In addition:

- Task text (title, notes, steps, notes' text) is never part of a shell command line. It reaches a child process only as one argv element or on stdin.
- The only text typed into a pane is `exec <path> worker`; `<path>` is single-quoted when it holds anything outside `[A-Za-z0-9_./-]`, and a path holding a single quote or a control character is a spawn failure.
- Every child process has a timeout: herdr 10 s, git 30 s, notify 10 s, the router 2 minutes.
- A failure note names what failed and never quotes more than 80 characters of a child's output.
- Tests never call the real `herdr` or the real `claude`, and never signal a process they did not start.
- `go.mod` and `go.sum` belong to no part: no part adds a module.

## Public surface

Frozen. Test writers author against these paths, names, and signatures before the code exists. A needed rename or move is a STOP. `ctx` is `context.Context` throughout.

### `embed.go` (package `desk`, repo root)

```go
// RouterSystem returns the built-in router system prompt (router/system.md).
func RouterSystem() string
```

### `internal/model`

```go
// The run states. A run in the first three is live: it holds one of the runner's slots.
const (
	RunRouting = "routing" // the router is choosing where the task runs
	RunWaiting = "waiting" // routed; its root already has a live in-place run
	RunRunning = "running" // a pane was started for it
	RunEnded   = "ended"   // its task left started
	RunFailed  = "failed"  // the router or the spawn failed
	RunKilled  = "killed"  // killed by a person or by the time limit
)

// RunLive reports whether state is routing, waiting, or running.
func RunLive(state string) bool

// The tags the runner writes.
const (
	TagRunner = "runner" // on every note the runner writes
	TagRouter = "router" // also on the note that records a route or a router failure
)

// Added to the stable refusal codes (both exit 1).
const (
	CodeStaleRun = "stale-run" // a status write from a run that is not the task's newest
	CodeNoRun    = "no-run"    // the task has no live run to kill, or desk worker's run is not running
)
```

### `internal/config`

```go
// Agent gains Models.
type Agent struct {
	Router     []string `toml:"router"`
	Worker     []string `toml:"worker"`
	SessionEnv string   `toml:"session_env"`
	Models     []string `toml:"models"` // the models the router may pick; the worker's {model}
}

// RouterFiles replaces the built-in router prompt and schema; "" keeps the built-in.
type RouterFiles struct {
	System string `toml:"system"` // a file path
	Schema string `toml:"schema"` // a file path
}

// Config gains: Router RouterFiles `toml:"router"`, between Agent and Notify.

// RouterSystemFile is StateDir/router-system.md: where the built-in system prompt is written for the router to read.
func (p Paths) RouterSystemFile() string
// RunnerPause is StateDir/runner-paused: the runner is paused while this file exists.
func (p Paths) RunnerPause() string
// Env returns the four XDG variables that resolve to these paths, as KEY=VALUE in the order CONFIG, STATE, DATA,
// CACHE: the inverse of ResolvePaths.
func (p Paths) Env() []string

// Expand substitutes {name} in each element of an argv template with vars[name], each element in one pass: a
// substituted value is never scanned again. A placeholder vars does not hold is left as written.
func Expand(template []string, vars map[string]string) []string
```

`Validate` also refuses `runner.cap`, `runner.max_runs_per_day`, `runner.max_run_minutes`, or `runner.poll_seconds` under 1, naming the key.

### `internal/store` (`runs.go`, new; `tasks.go` and `queries.go`, changed)

```go
// ErrNotArmed is StartRun's answer when the task is no longer armed.
var ErrNotArmed = errors.New("the task is not armed")

// Armed returns the tasks the runner may start, in the order they were armed: status ready, thread "agent",
// not archived, and, unless AgentsMayArm, the newest event that set the task ready was written by a user.
func (s *Store) Armed(ctx context.Context) ([]model.Task, error)

// StartRun creates the task's run row in state routing and sets the task started, in one transaction. The set
// event carries the run's id and no session. It returns ErrNotArmed when the task is not armed any more.
func (s *Store) StartRun(ctx context.Context, task int) (model.Run, error)

// RunUpdate is a change to a run row. A zero field is left as it is.
type RunUpdate struct {
	State     string
	Root      string
	Isolation string
	Model     string
	Reason    string
	Session   string
	Workspace string
	Pane      string
}

// UpdateRun applies u to the run while its state is from, and reports whether it did. A State of ended,
// failed, or killed also sets ended_ts.
func (s *Store) UpdateRun(ctx context.Context, id int64, from string, u RunUpdate) (bool, error)

// LiveRuns returns the runs in state routing, waiting, or running, by id.
func (s *Store) LiveRuns(ctx context.Context) ([]model.Run, error)

// RunsSince counts the runs started at or after t.
func (s *Store) RunsSince(ctx context.Context, t time.Time) (int, error)

// CurrentRun returns the task's newest run; ok is false when it has none.
func (s *Store) CurrentRun(ctx context.Context, task int) (run model.Run, ok bool, err error)

// RunWrote reports whether any event carries this run's id and a session: whether its worker wrote anything.
func (s *Store) RunWrote(ctx context.Context, run int64) (bool, error)
```

`ListRuns` moves from `queries.go` to `runs.go`, unchanged in what it returns; every read of a run row goes through one column list and one scan helper.

`SetTask` gains three rules, each checked inside the write's transaction:

- `a.Run != 0`, the patch sets a status, and `a.Run` is not the id of the task's newest run → `stale-run`, nothing written.
- A status change from `started` to any other status sets the task's live run, when it has one, to `ended` with `ended_ts`.
- An agent actor, a patch that sets the thread to `agent`, the task's status is `ready`, and not `AgentsMayArm` → `not-allowed`, nothing written.

### `internal/herdr` (new)

```go
// Package herdr drives the herdr terminal workspace manager through its command line.
package herdr

// Find returns the path of the herdr binary on PATH.
func Find() (string, error)

// Client runs herdr commands.
type Client struct {
	Bin     string        // "" → "herdr"
	Timeout time.Duration // 0 → 10s, per command
}

// Created is a new workspace and its one pane.
type Created struct{ Workspace, Pane string }

// Pane is one pane as `herdr pane list` reports it.
type Pane struct {
	ID        string // pane_id
	Workspace string // workspace_id
	Session   string // agent_session.value; "" when herdr knows no agent session for the pane
	Status    string // agent_status: idle | working | blocked | done | unknown
}

// Processes is what runs in a pane.
type Processes struct {
	Group int   // the foreground process group; 0 when herdr reports none
	PIDs  []int // the foreground processes, then the shell when herdr reports one; no duplicates
}

// CreateWorkspace runs `herdr workspace create --cwd <cwd> --label <label> --no-focus --env <e>…`; env entries are KEY=VALUE.
func (c *Client) CreateWorkspace(ctx context.Context, cwd, label string, env []string) (Created, error)
// Run runs `herdr pane run <pane> <command>`, the command as one argument.
func (c *Client) Run(ctx context.Context, pane, command string) error
// Panes runs `herdr pane list`.
func (c *Client) Panes(ctx context.Context) ([]Pane, error)
// Processes runs `herdr pane process-info --pane <pane>`.
func (c *Client) Processes(ctx context.Context, pane string) (Processes, error)
// ClosePane runs `herdr pane close <pane>`.
func (c *Client) ClosePane(ctx context.Context, pane string) error
```

A command that exits non-zero, times out, or prints something that is not the expected JSON is an error naming the subcommand, with at most 300 bytes of herdr's stderr. `Run`, `Processes`, and `ClosePane` on a pane herdr does not know are errors.

### `internal/herdr/herdrtest` (new; imported only by tests)

```go
// Package herdrtest gives tests a herdr that lives in memory.
package herdrtest

// Workspace is one workspace created through the stand-in.
type Workspace struct {
	ID, Pane, Cwd, Label string
	Env                  []string // KEY=VALUE, as given
	Command              string   // what Run was given; "" until then
}

// Herdr has the five methods of herdr.Client, so it satisfies runner.Herdr. It is safe for concurrent use. A new
// pane reports status "unknown" and no session. ClosePane removes the pane; Run, Processes, and ClosePane on an
// unknown pane are errors.
type Herdr struct{ /* unexported */ }

func NewHerdr() *Herdr
func (h *Herdr) Workspaces() []Workspace                     // every workspace created, in order
func (h *Herdr) Set(pane, session, status string)            // what Panes reports for the pane from now on
func (h *Herdr) Remove(pane string)                          // the pane is gone from Panes
func (h *Herdr) SetProcesses(pane string, p herdr.Processes) // what Processes reports for the pane
func (h *Herdr) Closed() []string                            // the panes ClosePane closed, in order
func (h *Herdr) Fail(method string, err error)               // CreateWorkspace | Run | Panes | Processes | ClosePane; nil clears
```

### `internal/runner` (new)

```go
// Package runner starts armed tasks in herdr panes, watches them, and stops them.
package runner

// Herdr is what the runner needs from herdr. *herdr.Client satisfies it.
type Herdr interface {
	CreateWorkspace(ctx context.Context, cwd, label string, env []string) (herdr.Created, error)
	Run(ctx context.Context, pane, command string) error
	Panes(ctx context.Context) ([]herdr.Pane, error)
	Processes(ctx context.Context, pane string) (herdr.Processes, error)
	ClosePane(ctx context.Context, pane string) error
}

// The runner's states, as State returns them and daemon.json and the status method show them.
const (
	StateOff      = "off"       // runner.enabled is false
	StatePaused   = "paused"    // the pause file exists
	StateNoHerdr  = "no-herdr"  // no herdr binary on PATH
	StateNoRouter = "no-router" // [agent] router is empty or its first word is not an executable
	StateOn       = "on"
)

// Options configures a Runner.
type Options struct {
	Store         *store.Store
	Config        config.Config
	Paths         config.Paths
	Herdr         Herdr                            // nil → a *herdr.Client when herdr.Find succeeds, checked at each tick
	Exe           string                           // the desk binary a pane runs; "" → os.Executable()
	Now           func() time.Time                 // nil → time.Now
	OnState       func(state string)               // called from New, and from Tick or Pause when the state changed; calls never overlap and arrive in order
	Logf          func(format string, args ...any) // nil → log.Printf
	RouterTimeout time.Duration                    // 0 → 2 minutes
	KillGrace     time.Duration                    // 0 → 2 seconds between TERM and KILL
}

// Runner is one desk's runner.
type Runner struct{ /* unexported */ }

func New(o Options) *Runner
// Tick is one poll: watch every live run, then start runs for armed tasks while the state and the caps allow.
func (r *Runner) Tick(ctx context.Context)
// Loop calls Tick, then again every runner.poll_seconds, until ctx ends.
func (r *Runner) Loop(ctx context.Context)
// State returns the runner's state now.
func (r *Runner) State() string
// Paused reports whether the pause file exists.
func (r *Runner) Paused() bool
// Pause writes or removes the pause file. An actor with a session gets not-allowed.
func (r *Runner) Pause(ctx context.Context, a store.Actor, paused bool) error
// Kill stops the task's live run: it kills the pane's processes, closes the pane, sets the run killed and the
// task blocked. It returns the task. No live run is no-run; an actor with a session gets not-allowed.
func (r *Runner) Kill(ctx context.Context, a store.Actor, task int) (model.Task, error)

// Route is where and how a task runs.
type Route struct {
	Root      string `json:"root"`
	Isolation string `json:"isolation"`
	Model     string `json:"model"`
	Reason    string `json:"reason"`
}

// Roots returns the roots a task may run in: the config's roots in order, then the scratch root with isolation in-place.
func Roots(c config.Config, p config.Paths) []config.Root
// Schema returns the JSON schema of a route: root an enum of the roots' paths, isolation an enum of worktree and
// in-place, model an enum of models (left out, with its requirement, when models is empty), reason a string.
func Schema(roots []config.Root, models []string) []byte
// RouterInput returns the router's stdin: {"task":{number,title,notes,project,steps},"roots":[{path,about,isolation}],"models":[…]}.
func RouterInput(t model.Task, roots []config.Root, models []string) []byte
// ParseRoute reads a router's stdout: one JSON object holding the route at its top level or under
// "structured_output". The reason is trimmed and cut to 500 characters. It does not check the values.
func ParseRoute(out []byte) (Route, error)
// Resolve returns what needs no router and whether the router is needed. A field needs no router when the task's
// own field sets it; isolation also when the decided root has a configured isolation; model also when models is
// empty. When the router is not needed, the route is checked as Apply checks it.
func Resolve(t model.Task, roots []config.Root, models []string) (decided Route, needsRouter bool, err error)
// Apply fills the fields decided leaves open from the router's pick (a root's configured isolation wins over the
// pick) and checks the result: the root is a listed root; the isolation is self, worktree, or in-place, and never
// self by the router's own pick; the model is in models when models is not empty. A failed check is an error
// that names the field.
func Apply(decided, picked Route, roots []config.Root, models []string) (Route, error)

// Slug turns a title into a branch-name part: lower-case letters and digits kept, every other run of characters
// one "-", none at either end, at most 40 characters. It may be empty.
func Slug(title string) string
```

### `internal/worker` (new; imports only `internal/model` and `internal/store`)

```go
// Package worker builds what a worker session is started with.
package worker

// FirstMessage returns the worker's first message for a task: the title, the notes, the steps, the history, and
// how to hand the task back. Events tagged router and step events are left out. It is at most 64 KiB: the
// oldest history lines are dropped first and a line says how many.
func FirstMessage(d store.TaskDetail) string
```

`FirstMessage`'s shape, exactly (a section with nothing in it is left out with its blank line):

```
You are working on desk task T<n>: <title>

<notes>

Steps:
- [ ] s1 <text>
- [x] s2 <text>

History (oldest first):
- 2026-10-04 14:02Z user: created
- 2026-10-04 14:03Z user: status ready
- 2026-10-04 14:03Z runner: status started
- 2026-10-04 14:20Z agent: note: <text> (<ref>)
- 2026-10-04 14:21Z agent: status blocked (<ref>)
- 2026-10-04 14:30Z user: decided: <text>

When the work is finished, hand the task back: run `desk set T<n> review --ref <a file or PR that shows the work>`. Add `--merged` when that PR is merged. If you cannot finish, record what you need with `desk note --task T<n> "<what you need>"`, then run `desk set T<n> blocked`. Never set ready or done.
```

A history line's writer is `runner` when the event has a run id and no session, `agent` when its who is agent, else `user`. A `set` event that changes no status prints `changed <field names>`. `(<ref>)` is left out when the event has no ref.

### `internal/api`

```go
const (
	MethodRunsKill    = "runs.kill"
	MethodRunnerPause = "runner.pause"
)

// Status gains three fields.
//	RunnerState  string `json:"runner_state"`  // off | paused | no-herdr | no-router | on
//	RunnerPaused bool   `json:"runner_paused"`
//	RunnerCap    int    `json:"runner_cap"`    // runner.cap

// RunnerControl is what the server needs from the runner. *runner.Runner satisfies it.
type RunnerControl interface {
	State() string
	Paused() bool
	Pause(ctx context.Context, a store.Actor, paused bool) error
	Kill(ctx context.Context, a store.Actor, task int) (model.Task, error)
}

// ServerOptions gains: Runner RunnerControl // nil → runs.kill and runner.pause are unknown methods (400); runner_state is "off"

func (c *Client) KillRun(ctx context.Context, a store.Actor, task int) (model.Task, error)
func (c *Client) PauseRunner(ctx context.Context, a store.Actor, paused bool) (Status, error)
```

`runs.kill` takes `{"actor":…,"task":n}` and answers the task. `runner.pause` takes `{"actor":…,"paused":bool}` and answers the `Status`. Both are served on the socket and on TCP.

### `internal/daemon`

```go
// Info gains: Runner string `json:"runner"` // the runner's state; the file is rewritten when it changes
```

`Start`'s signature does not change. It always makes a runner (`runner.New`, with `OnState` rewriting the info file with the state it is given), passes it to the server, and runs `Loop` until `Close`.

### `internal/cli`

```go
// Env gains:
//	Exec func(path string, argv []string) error // replaces the process; nil → syscall.Exec with the process's environment
```

### The command line

| command | stdout on success | exits |
|---|---|---|
| `desk runs [--all] [--json]` | one line per live run, oldest first: `run <id>  T<n>  <state>  <root>  <isolation>  <model>  <elapsed>`; `no live runs` when none. `--all` lists every run. `--json` prints the array of runs | 0 |
| `desk runs kill <task>` | `T<n> blocked` | 0; 1 `no-run`; 1 `not-allowed` for an agent; 1 `unknown-task` |
| `desk runner [status]` | `runner <state>` and, when the state is `on` or `paused`, ` · <live>/<cap> live` | 0 |
| `desk runner pause` · `desk runner resume` | `runner <state>` | 0; 1 `not-allowed` for an agent |
| `desk worker` | none: the process becomes the worker | 2 when `DESK_RUN` or `DESK_TASK` is unset or malformed; 1 `no-run`; 3 when the worker cannot be started |

`desk worker` reads `DESK_RUN`, `DESK_TASK`, and the caller's session. It asks the home for the runs and the task. The run with that id must be `running`, on that task, with that session; anything else is `no-run`. It builds `worker.FirstMessage`, expands `[agent] worker` with `config.Expand` (`model` the run's model, `session` the session, `message` the message), finds the first word on PATH, and calls `Env.Exec`. `internal/cli` imports neither `internal/runner` nor `internal/herdr`. When the template is empty or its first word is not on PATH, it writes a note on the task (`worker: cannot start <first word>`), sets the task `blocked`, prints the reason on stderr, and exits 3.

## Target files

- `embed.go`
- `router/system.md` — the built-in router system prompt
- `internal/model/model.go`
- `internal/model/refusal.go`
- `internal/config/config.go`
- `internal/config/paths.go`
- `internal/store/runs.go`
- `internal/store/queries.go` — `ListRuns` moves out
- `internal/store/tasks.go`
- `internal/herdr/**`
- `internal/runner/**`
- `internal/worker/**`
- `internal/api/wire.go`
- `internal/api/server.go`
- `internal/api/client.go`
- `internal/daemon/daemon.go`
- `internal/cli/cli.go`
- `internal/cli/runs.go`
- `internal/cli/worker.go`
- `internal/setup/setup.go`
- `README.md`
- `profiles/claude-code/skills/desk/SKILL.md`
- `scripts/e2e/**` — the fake herdr, the stubs, and the runner's hand-test scripts
- `.claude/build/notes.md` — the hand-tester section

## Why the parts wait

P3 waits for P1 (it calls the store's run methods and `config.Expand`) and for P2 (its port is written against `herdr.Client`'s types). P4 waits for P3 (the daemon, the API, and `desk worker` call the runner). P5 waits for P4: its scripts drive the finished command line, and its builder runs each one. A builder that needs one more non-test file in its own packages adds it and says so in its report. `internal/config/paths.go` is being changed on main by another PR (the stale-config check); P1 adds its three methods at the end of the file and touches nothing else there.

## Parts

- P1 · the foundation: run states and codes, the config keys, the store's run rows, arming, and the stale-run rule
  - model: opus — Design 2 and 3 bind the store's files
  - files: internal/model/model.go, internal/model/refusal.go, internal/config/config.go, internal/config/paths.go, internal/store/runs.go, internal/store/tasks.go, internal/store/queries.go
  - test files: none
  - deliverables: 1, 2, 3, 4, 5
  - after: none
  - tests: W1, W2, W3
- P2 · the herdr client and its two stand-ins
  - model: sonnet — the brief names each command, each JSON path, and each behaviour of the fake
  - files: internal/herdr/herdr.go, internal/herdr/herdrtest/herdrtest.go, scripts/e2e/fake-herdr.py
  - test files: none
  - deliverables: 6, 7, 15
  - after: none
  - tests: W4
- P3 · the runner (route, spawn, watch, kill, pause) and the worker's message
  - model: opus — Design 1, 2, 4, 5, and 6 bind these files; it is the spawn path
  - files: internal/runner/runner.go, internal/runner/route.go, internal/runner/spawn.go, internal/runner/watch.go, internal/runner/kill.go, internal/worker/message.go, router/system.md, embed.go
  - test files: none
  - deliverables: 8, 9, 10, 11, 12, 13, 14
  - after: P1, P2
  - tests: W5, W6, W7, W8, W9
- P4 · the wiring: API, daemon, command line, profile, docs
  - model: sonnet — the public surface and the command table name every method, field, line, and exit code
  - files: internal/api/wire.go, internal/api/server.go, internal/api/client.go, internal/daemon/daemon.go, internal/cli/cli.go, internal/cli/runs.go, internal/cli/worker.go, internal/setup/setup.go, README.md, profiles/claude-code/skills/desk/SKILL.md
  - test files: none
  - deliverables: 16, 17, 18, 19, 20, 21
  - after: P3
  - tests: W10, W11, W12
- P5 · the hand-test scripts and the notes
  - model: sonnet — each claim's `pass:` line names what its script prints
  - files: scripts/e2e/runner-lib.sh, scripts/e2e/stub-router.sh, scripts/e2e/stub-worker.sh, scripts/e2e/r*.sh, scripts/e2e/h12-setup.sh, .claude/build/notes.md
  - test files: none
  - deliverables: 22, 23
  - after: P4
  - tests: none

## Test slices

- W1 · plain · arming and starting a run
  - files: internal/store/runs_test.go
  - covers: 3, 4
  - under test: Armed, StartRun, CurrentRun, RunWrote
- W2 · plain · run row updates and the write-path rules
  - files: internal/store/runpolicy_test.go
  - covers: 3, 5
  - under test: UpdateRun, LiveRuns, RunsSince, SetTask
- W3 · plain · the run states, the new config keys, the limits check, the template expander, and the paths' env
  - files: internal/model/run_test.go, internal/config/runner_test.go
  - covers: 1, 2
  - under test: RunLive, Load, Validate, Expand, Paths.Env
- W4 · plain · the herdr client against the fake herdr, and one contract table over both stand-ins
  - files: internal/herdr/herdr_test.go, internal/herdr/contract_test.go
  - covers: 6, 7, 15
  - under test: CreateWorkspace, Run, Panes, Processes, ClosePane
- W5 · plain · the route: roots, schema, input, parse, resolve, apply
  - files: internal/runner/route_test.go
  - covers: 8
  - under test: Roots, Schema, RouterInput, ParseRoute, Resolve, Apply
- W6 · plain · the worker's message and the branch slug
  - files: internal/worker/message_test.go, internal/runner/slug_test.go
  - covers: 9
  - under test: FirstMessage, Slug
- W7 · plain · what starts a run and what does not
  - files: internal/runner/fire_test.go
  - covers: 10, 14
  - under test: Tick, State, Pause, Paused
- W8 · plain · the spawn: worktree, workspace, pane command, failures
  - files: internal/runner/spawn_test.go
  - covers: 11
  - under test: Tick
- W9 · plain · the watch and the kill
  - files: internal/runner/watch_test.go, internal/runner/kill_test.go
  - covers: 12, 13
  - under test: Tick, Kill
- W10 · plain · the API's run methods and status fields
  - files: internal/api/runs_test.go
  - covers: 16
  - under test: NewServer, Handler, KillRun, PauseRunner
- W11 · plain · the runs, runner, and worker commands
  - files: internal/cli/runs_test.go, internal/cli/worker_test.go
  - covers: 18, 19
  - under test: cli.Run
- W12 · plain · the daemon runs the runner; the profile writes the models
  - files: internal/daemon/runner_test.go, internal/setup/models_test.go
  - covers: 17, 20
  - under test: Start, ReadInfo, setup.Run

## How the tests are built

A runner test opens a real store on a `testutil.NewMachine` folder, uses `herdrtest.Herdr`, a fixed `Now`, and a router that is a shell script the test writes into a temp folder (`Config.Agent.Router = []string{script, "{system}", "{schema}"}`); it calls `Tick` directly and never waits for `Loop`. A kill test starts its own child processes (`sleep`) in their own process group and hands their pids to `herdrtest.Herdr.SetProcesses`. W4 runs `scripts/e2e/fake-herdr.py` through `herdr.Client{Bin: …}` with `FAKE_HERDR_DIR` set to a temp folder; its contract test runs one table over the five methods against both `herdrtest.Herdr` and that client: a new pane reports `unknown` and no session, `ClosePane` removes the pane from `Panes`, and `Run`, `Processes`, and `ClosePane` on an unknown pane are errors on both. W10, W11, and W12 reach a runner through `testutil.StartHome` with a fake `herdr` first on PATH (`t.Setenv`), a copy of or link to `scripts/e2e/fake-herdr.py`.

## How the hand test runs

Each claim is one script under `scripts/e2e/`, built on `lib.sh` and `runner-lib.sh`. H1 to H10 are hermetic: `herdr` on PATH is `fake-herdr.py`, the router is `stub-router.sh`, the worker is `stub-worker.sh`, and `poll_seconds` is 1 (H1 uses 2). H11 uses the real herdr on the machine with the stubs: it opens a workspace without taking focus and closes everything it opened. H12 uses the real herdr and the real `claude` for one router run and one worker run, which spend real quota. H13 and H14 re-run two U1 claims over code this unit changes. Every script uses its own temp desk home; none reads or writes the real one.

## Hand test

- H1 · an armed task is running in a pane within two polls, with the task, session, and run in the pane's environment
  - run: `bash scripts/e2e/r01-spawn.sh`
  - pass: exit 0; the output holds `spawned within 2 polls ok`, `DESK_TASK=T1 ok`, `DESK_SESSION is a uuid ok`, `DESK_RUN=1 ok`, `pane command is exec <desk> worker ok`, `runs shows T1 running ok`, `note records workspace and pane ok`, `notify ran ok`, and `E2E PASS`
- H2 · nothing starts a task the user did not arm: thread me, an agent's proposal, a disabled runner, a paused runner
  - run: `bash scripts/e2e/r02-no-fire.sh`
  - pass: exit 0; the output holds `thread me never spawns ok`, `agent proposal never spawns ok`, `agent ready refused ok`, `agent thread flip on a ready task refused ok`, `runner off never spawns ok`, `paused never spawns ok`, `resume spawns ok`, `agent pause refused ok`, and `E2E PASS`
- H3 · a worker that stops, asks, closes, or hands back moves its task, and its run ends
  - run: `bash scripts/e2e/r03-watch.sh`
  - pass: exit 0; the output holds `idle two polls → review ok`, `session ended without reporting ok`, `one idle poll changes nothing ok`, `blocked → blocked ok`, `pane gone → review ok`, `worker hand-back → review, run ended ok`, and `E2E PASS`
- H4 · `desk runs kill` leaves no process from the pane alive
  - run: `bash scripts/e2e/r04-kill.sh`
  - pass: exit 0; the output holds `worker and its child alive before ok`, `T1 blocked`, `no process left ok`, `pane closed ok`, `run killed ok`, `no-run` with `exit=1`, `agent kill not-allowed ok`, and `E2E PASS`
- H5 · the caps hold: the fourth task waits, the daily cap stops the next, a self root runs two at once, an in-place root runs one
  - run: `bash scripts/e2e/r05-caps.sh`
  - pass: exit 0; the output holds `three running, fourth ready ok`, `fourth starts when one ends ok`, `daily cap holds ok`, `self root runs two ok`, `in-place second waits ok`, `waiting run starts when the first ends ok`, and `E2E PASS`
- H6 · the router is given the task, the roots, and the models, and only a valid answer routes
  - run: `bash scripts/e2e/r06-router.sh`
  - pass: exit 0; the output holds `stdin holds task, roots, models ok`, `schema holds the root and model enums ok`, `DESK_HOOKS=off ok`, `route saved on the task ok`, `not JSON → blocked ok`, `unknown root → blocked ok`, `exit 1 → blocked ok`, `run failed ok`, `fields set: router not called ok`, `no-router in daemon.json and status ok`, `no-router notified once ok`, and `E2E PASS`
- H7 · worktree isolation cuts `<repo>-T<n>` on `desk/T<n>-<slug>` beside the root and uses it again on a re-arm
  - run: `bash scripts/e2e/r07-worktree.sh`
  - pass: exit 0; the output holds `worktree created ok`, `branch desk/T1-fix-the-login-page ok`, `worker cwd is the worktree ok`, `re-arm reused the worktree ok`, `not a git repo → blocked ok`, and `E2E PASS`
- H8 · a re-armed task starts a new run whose first message holds the history, and the old run can no longer set the status
  - run: `bash scripts/e2e/r08-rearm.sh`
  - pass: exit 0; the output holds `first message holds title, notes, steps ok`, `second message holds the answer note ok`, `router reason not in the message ok`, `run 2 running ok`, `stale-run` with `exit=1`, `new run sets review ok`, and `E2E PASS`
- H9 · a run past the time limit is killed and its task blocked
  - run: `bash scripts/e2e/r09-limit.sh`
  - pass: exit 0; the output holds `still running before the limit ok`, `blocked after the limit ok`, `no process left ok`, `run killed ok`, and `E2E PASS`
- H10 · a live run survives a daemon restart, a pause survives it too, and the pane's desk talks to the home that started it
  - run: `bash scripts/e2e/r10-restart.sh`
  - pass: exit 0; the output holds `XDG variables in the pane ok`, `run still watched after restart ok`, `pause kept across restart ok`, `stale routing run failed ok`, and `E2E PASS`
- H11 · on the real herdr: a workspace opens without taking focus, the pane is found by its session, an idle worker goes to review, and a kill leaves nothing
  - run: `bash scripts/e2e/r11-real-herdr.sh`
  - pass: exit 0; the output holds `focus unchanged ok`, `pane found by agent session ok`, `idle → review ok`, `kill: no process left ok`, `pane gone from herdr ok`, `every workspace this test opened is closed ok`, and `E2E PASS`
- H12 · with the real router and the real worker: a task with no project routes to the scratch root and the worker hands it back
  - run: `bash scripts/e2e/r12-real-claude.sh`
  - pass: exit 0; the output holds `routed to the scratch root ok`, `worker session known to herdr ok`, `T1 review`, `real runs: 1 router, 1 worker`, `every workspace this test opened is closed ok`, and `E2E PASS`
- H13 · U1's arming claim still passes over the changed store
  - run: `bash scripts/e2e/h04-arming.sh`
  - pass: exit 0; the output holds `E2E PASS`
- H14 · setup's profile writes the models beside the templates
  - run: `bash scripts/e2e/h12-setup.sh`
  - pass: exit 0; the output holds `profile templates ok`, `profile models ok`, and `E2E PASS`

## Deliverables

Before, for every line: the file or behaviour does not exist, except where the line says what changes.

1. `internal/model` holds the six run states, `RunLive`, `TagRunner`, `TagRouter`, `CodeStaleRun`, and `CodeNoRun` as `## Public surface` writes them; both codes exit 1 in the CLI. (§Public surface; §Corrections 5, 11)
2. `internal/config`: `Agent.Models` and the `[router]` table load and save; `Paths.RouterSystemFile` and `Paths.RunnerPause` return `StateDir/router-system.md` and `StateDir/runner-paused`; `Paths.Env` returns the four XDG variables that `ResolvePaths` turns back into the same paths; `Expand` substitutes `{name}` placeholders one pass per element, so a value holding `{other}` stays as written; `Validate` refuses a `runner.cap`, `max_runs_per_day`, `max_run_minutes`, or `poll_seconds` under 1, naming the key. Before: `Validate` accepts any limit. (§Corrections 2, 18)
3. `internal/store/runs.go`: `StartRun` inserts a `routing` run and sets the task `started` in one transaction, the set event carrying the run id and no session, and returns `ErrNotArmed` for a task that is not armed; `UpdateRun` changes a run only while its state is `from`, reports whether it did, and sets `ended_ts` with a terminal state; `LiveRuns`, `RunsSince`, `CurrentRun`, and `RunWrote` answer as `## Public surface` says; `ListRuns` lives in `runs.go` and every run read shares one column list and one scan helper. Before: `ListRuns` is in `queries.go` with its own scan. (spec; Design 2)
4. `Store.Armed` returns exactly the tasks that are `ready`, on thread `agent`, not archived, and, unless `AgentsMayArm`, whose newest ready-setting event was written by a user, ordered by that event's id. (spec: Arming; §Corrections 12)
5. `Store.SetTask`: a status write whose actor carries a run id that is not the task's newest run is refused `stale-run` and writes nothing; a status change away from `started` ends the task's live run in the same transaction; an agent setting the thread `agent` on a `ready` task is refused `not-allowed` unless `AgentsMayArm`. Before: none of the three. (spec; §Corrections 11, 12, 13; Design 3)
6. `internal/herdr`: `Find` and the five `Client` methods run the commands `## Public surface` names, each with its timeout, and read the JSON paths `## Design`'s assumptions list; a failed command is an error naming the subcommand with at most 300 bytes of stderr. (spec: Spawn, Watch)
7. `scripts/e2e/fake-herdr.py` is a stand-in `herdr` for tests, keeping its state under `$FAKE_HERDR_DIR`: `workspace create` records cwd, label, and env and prints herdr's JSON with new ids; `pane run` starts the command with `sh -c` in its own session with the workspace's cwd and env plus `HERDR_PANE_ID`, output to `<dir>/<pane>.log`; `pane list` prints every open pane, and a pane whose command has exited is gone; `pane report-agent` and `pane report-agent-session` set the pane's `agent_status` and `agent_session` with herdr's flags; `pane process-info --pane` prints the process group and its live pids; `pane close` kills the group and removes the pane; `notification show` appends `<title>\t<body>` to `<dir>/notifications.log`; `FAKE_HERDR_FAIL=<subcommand words joined by ->` makes that subcommand exit 1. (§How the hand test runs)
8. The route (`internal/runner/route.go`, `router/system.md`, `embed.go`): `Roots`, `Schema`, `RouterInput`, `ParseRoute`, `Resolve`, and `Apply` behave as `## Public surface` says; `router/system.md` tells the router to answer with only the JSON object, to pick the listed root whose path is or contains the task's project, to send a task with no project or one under no listed root to the last root listed, and to pick `worktree` for work that changes a repository's files unless the root says otherwise; `desk.RouterSystem()` returns it. (spec: route; §Corrections 1, 2, 4, 19; Design 4)
9. `internal/worker.FirstMessage` renders the exact shape in `## Public surface`, leaves out events tagged `router` and step events, and keeps to 64 KiB by dropping the oldest history lines; the package imports only `internal/model` and `internal/store`; `runner.Slug` follows its rule. (spec: Spawn; Design 5)
10. Starting runs (`Tick`): when the state is `on`, for each armed task in order while live runs are under `runner.cap` and the runs started since local midnight are under `runner.max_runs_per_day`, it calls `StartRun`; when `Resolve` says the router is needed it takes the system prompt (the built-in, `desk.RouterSystem()`, written to `RouterSystemFile()` at 0600, or the `[router] system` file as it is) and the schema (`Schema`, or the text of the `[router] schema` file), runs `config.Expand` of `[agent] router` with `system` the prompt's path and `schema` the schema's text, with `RouterInput` on stdin and `DESK_HOOKS=off` added to its environment, and passes the answer through `ParseRoute` and `Apply`; a good route is written to the task's `root`, `isolation`, and `model` fields and to the run row, and, when the router ran, as one note `routed to <root> (<isolation>, <model>): <reason>` (`(<isolation>)` alone when no models are configured) tagged `runner` and `router`; a router that exits non-zero, times out, prints something `ParseRoute` or `Apply` rejects, and a task whose own fields `Resolve` rejects, each set the run `failed` and the task `blocked` with a note `router: <reason>` tagged `runner` and `router`; an in-place route whose root already has a `running` in-place run sets the run `waiting`, and a `waiting` run is spawned by the first later tick that finds its root free; `self` and `worktree` routes never wait; a `routing` run found at the start of a tick is set `failed` and its task `blocked` with a note saying the daemon restarted. (spec: Loop, route, No-fire; §Corrections 3, 5)
11. The spawn: for `worktree` it uses `<parent of root>/<base of root>-T<n>`, as it is when it is already a git work tree, else `git worktree add` of the branch `desk/T<n>-<slug>` (`desk/T<n>` with an empty slug), created with `-b` only when it does not exist; for the scratch root it makes the folder when missing; it mints a random UUID, sets the run `running` with the session, creates the workspace (recording its workspace and pane on the run at once, so a kill that arrives before the command runs finds the pane) with the label `desk T<n>`, cwd, `--no-focus`, and the env `DESK_TASK=T<n>`, `DESK_SESSION=<uuid>`, `DESK_RUN=<id>`, and the four of `Paths.Env()`, runs `exec <Exe> worker` in the pane by the quoting rule, writes a note `run <id>: workspace <w>, pane <p>` tagged `runner`, and runs the notify command; a failure at any step sets the run `failed` and the task `blocked` with a note `spawn: <reason>`, and closes a pane it had opened. (spec: Spawn; §Corrections 9, 10)
12. The watch (`Tick`, every state but with no herdr): one `Panes` call per tick when a run is `running`; a run's pane is the one whose session is the run's, else the one whose id and workspace are the run's; on a pane found by session, `blocked` sets the task `blocked` with a note, and `done` or `idle` on two ticks in a row sets the task `review` with the note `session ended without reporting` when `RunWrote` is false and `session went idle without handing back` when it is true; `working` or `unknown` resets the count; a pane found only by id changes nothing; no pane sets the task `review` with the note `the pane closed without a hand-back`; for every flip the runner first ends the run with a conditional `UpdateRun` and writes the note and the status, both with the run's id, only when that update applied, so a status a person set at the same moment is never overwritten; a `Panes` error is logged and changes nothing. (spec: Watch; §Corrections 14, 17)
13. `Kill` and the time limit: `Kill` refuses an actor with a session (`not-allowed`) and a task with no live run (`no-run`); for a `running` run it finds the pane as the watch does, signals the foreground group and every reported pid with TERM, waits up to `KillGrace` for them to exit, signals what is left with KILL, closes the pane, sets the run `killed`, sets the task `blocked` as the caller, and writes a note; when herdr cannot be asked about a `running` run (no herdr on PATH, or `Panes` fails) it returns an error and writes nothing; a `routing` or `waiting` run is set `killed` and the task `blocked` with no herdr call; it never signals pid 0 or 1, its own pid, or its own process group; a run `running` for more than `runner.max_run_minutes` is stopped the same way by the watch, with the note `stopped after <n> minutes (runner.max_run_minutes)` tagged `runner`. (spec: kill, Watch; §Corrections 6, 16; Design 6)
14. `State`, `Pause`, `Paused`, notify, `OnState`: `State` returns the first that holds of `off`, `paused`, `no-herdr`, `no-router`, else `on`; `Pause` writes or removes `Paths.RunnerPause()` (0600) and refuses an actor with a session; the notify command is `config.Expand` of `[notify] command` with `title` and `body`, run with a 10 s timeout, its failure logged and nothing else; it runs once per spawn (`desk: T<n> started`, the task's title) and once each time the state becomes `no-router`; `OnState` is called from `New` and whenever the state changed, never two calls at once and always in order, so the last call holds the state that stands. (spec: No-fire; §Corrections 7, 8)
15. `internal/herdr/herdrtest.Herdr` is an in-memory herdr with the methods `## Public surface` lists; it satisfies `runner.Herdr`, and it and `fake-herdr.py` answer one contract the same way: a new pane is `unknown` with no session, `ClosePane` removes the pane, and `Run`, `Processes`, and `ClosePane` on an unknown pane are errors. (Design 1)
16. `internal/api`: `runs.kill` and `runner.pause` are served on both handlers through the shared adapter, a refusal is a 409 with its code, and with a nil `Runner` both are unknown methods; `status` carries `runner_state`, `runner_paused`, and `runner_cap`; `Client.KillRun` and `Client.PauseRunner` call them. Before: `status` has `runner_on` only. (spec: API; §Corrections 7; §Shared with U3)
17. `internal/daemon.Start` makes the runner, hands it to the server, runs its `Loop` until `Close`, and writes `runner` into `daemon.json`, rewriting the file when the state changes. Before: the file is written once and no loop runs. (spec: Loop, No-fire)
18. `desk runs`, `desk runs kill`, and `desk runner [status|pause|resume]` follow the command table. (spec: kill; §Corrections 7)
19. `desk worker` follows the command table and the paragraph under it, through `Env.Exec`; `internal/cli` imports neither `internal/runner` nor `internal/herdr`. (spec: Spawn; Design 5)
20. `setup.Run` with the `claude-code` profile also writes `[agent] models = ["sonnet", "opus"]` unless models are set, with its own `agent.models: …` line. (§Corrections 2)
21. `README.md` documents the runner (arming, the route, the caps, the watch, the states, `[agent] models`, `[router]` and that a `[router] schema` file replaces the built-in enums of roots and models, what a worker template gets), `desk runs`, `desk runner`, and `desk worker`; `profiles/claude-code/skills/desk/SKILL.md` says how a worker hands back, that `stale-run` means a newer run owns the task, and that an agent never kills a run or pauses the runner. (notes §builders: Docs)
22. `scripts/e2e/runner-lib.sh`, `stub-router.sh`, `stub-worker.sh`, and `r01-spawn.sh` … `r12-real-claude.sh` exist, one script per claim H1 to H12, each ending `E2E PASS` only when its checks hold and printing the lines its `pass:` names; `h12-setup.sh` also prints `profile models ok`; H11 and H12 close every workspace they opened, on failure too, and never close another; `shellcheck -S info scripts/e2e/*.sh` is clean. (§Hand test)
23. `.claude/build/notes.md` `## hand-tester` says which runner claims need the real `herdr` (H11, H12) and the real `claude` (H12), that H12 spends one router run and one worker run, and that H9 takes over a minute. (§How the hand test runs)

```yaml
description: desk U2 — the runner and the router
deliverables:
  - name: model — run states, RunLive, runner tags, stale-run and no-run codes
    covered_by: [judgment]
  - name: config — agent.models, the router table, RouterSystemFile, RunnerPause, Paths.Env, Expand, limits at least 1
    covered_by: [judgment]
  - name: store run rows — StartRun, UpdateRun, LiveRuns, RunsSince, CurrentRun, RunWrote
    covered_by: [judgment]
  - name: store Armed — ready, thread agent, armed by a user, in arming order
    covered_by: [judgment]
  - name: SetTask — stale-run refusal, leaving started ends the run, the thread refusal
    covered_by: [judgment]
  - name: herdr client — five commands, timeouts, JSON paths, errors
    covered_by: [judgment]
  - name: fake herdr for tests and the hermetic hand-test claims
    covered_by: [judgment]
  - name: the route — roots, schema, input, argv, parse, resolve, the built-in system prompt
    covered_by: [judgment]
  - name: the worker's first message and the branch slug
    covered_by: [judgment]
  - name: starting runs — state gate, caps, order, routing outcomes, waiting, stale routing runs
    covered_by: [judgment]
  - name: the spawn — worktree, uuid, workspace env, pane command, note, notify, failures
    covered_by: [judgment]
  - name: the watch — pane lookup, blocked, idle twice, pane gone, errors
    covered_by: [judgment]
  - name: kill and the time limit
    covered_by: [judgment]
  - name: state, pause, notify, OnState
    covered_by: [judgment]
  - name: herdrtest in-memory herdr and the contract it shares with the fake herdr
    covered_by: [judgment]
  - name: API — runs.kill, runner.pause, status fields, client methods
    covered_by: [judgment]
  - name: daemon — makes the runner, runs its loop, writes its state to daemon.json
    covered_by: [judgment]
  - name: desk runs, desk runs kill, desk runner
    covered_by: [judgment]
  - name: desk worker
    covered_by: [judgment]
  - name: the claude-code profile writes agent.models
    covered_by: [judgment]
  - name: README and the skill
    covered_by: [judgment]
  - name: the runner's e2e scripts, stubs, and library
    covered_by: [judgment]
  - name: build notes — the hand-tester section names the runner claims' needs
    covered_by: [judgment]
```

--- brief complete ---
