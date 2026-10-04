class: R1 — agent (unconfirmed), 2026-10-04
model: opus — `## Design` entries name P1's files (`internal/board/`)

# desk U3: the board (bubbletea), the capture popup

Unit 3 of the plan `~/.claude/plans/task-runner-plugin.md` (v3, approved 2026-10-03). U1 (store, daemon, API, CLI, journal) is on `main`; its brief, `docs/build/briefs/quick-u1-core.md`, is the spec of what exists. U2 (runner and router) is built by another session at the same time, from the same base, and is not on `main`.

Branch contract: commits on `quick/u3-board` (builders: on the harness-named branch of your own tree). No push and no PR from a builder; the session pushes at SHIP.

Operator instruction for the whole plan (2026-10-03): "Make sure there is very thorough hand testing and code coverage". It is why this brief carries ten hand-test claims, seven test slices, and a coverage floor for the new package.

## Spec

The plan's U3 section, verbatim:

> ## U3. Board (bubbletea)
>
> Two pages. **Board**: NEEDS YOU (`blocked`, `review`), IN MOTION (`started`: root, isolation, model, elapsed, last note), ON DECK (`ready`; `#agent · queued` when armed; then inbox `open`, proposals shown with `#agent`), done drawer; header `runner ● on · 2/3 · home` or `offline (snapshot 12m)`; keys `+ n s b r x a f k P / p t d ?` as in the mockup; `ctrl+d` never bound; `x` on `review` asks nothing. **Task page**: notes, steps, HISTORY across sessions (claim, router reason, notes with refs, decisions, hand-backs), refs with `o` → herdr-file-viewer when installed; `e` notes, `n` re-arm (after a `note`), fields root/isolation/model editable. **Capture**: popup, title line with `#thread` `@project`. Colours from the terminal's ANSI palette. Narrow < 78 cols one surface; ≥ 110 board beside task.
>
> Acceptance: `prefix+t` opens then focuses; `prefix+a` lands a task; offline client shows the banner and refuses `n`; every mockup key does what the mockup says.

The plan's lines this unit rests on, verbatim:

> - **One home per desk.** The home machine runs the daemon, owns the SQLite store, and is the only writer. […] A client with the home unreachable refuses writes with `home-unreachable` (exit 3) and opens the board read-only from its last snapshot with an "offline" banner.
> - **Arming** is the user's act: a task runs only when a `who=user` event set `status=ready` with thread `agent`. Agents may add tasks and set the `agent` thread (a proposal lands in the inbox with `#agent`), never `ready` or `done`.
> - `edit --notes` replaces the body (the board's "answer a blocked task" flow writes a `note`, never a replace).
> - `desk runs kill T<n>` (board `k`) kills the pane's process group via `herdr pane process-info` then closes the pane, writes `blocked`. […] Re-arming a `blocked` task (the user adds a `note` with the answer and presses `n`) starts a new run whose first message includes the history.
> - Judgment calls: The M1 cannot focus a home pane (`f` disabled off-home). Capture popup is desk's own, not a herdr quick-capture plugin.

The mockups (`task-runner-system.html`, v2). Its note: "the Runs page was folded into the board (its `f`/`k`/`P` keys and runner header) and the task page (the timeline is the task's HISTORY)". Ids are `T<n>` in desk, and the machine names are examples.

```
 <name>  Quirk ▾   thread: all ▾              runner ● on  2/3 running   sync 12s ago
 ──────────────────────────────────────────────────────────────────────────────
 NEEDS YOU
 ▸ A14  blocked  Migrate push tokens to the new table            M5 · 2m ago
        ↳ "Two writers found. Keep the devtools path or drop it?"
   A11  review   Fix login timeout on cold start                PR #1601 merged
 IN MOTION
   A12  started  Investigate ~/.agents hook ordering            M5 · in-place · sonnet · 4m
        ↳ last note: "reading session-todo-list.sh"
   B03  started  Rank tickets by Jev score                       M1 · worktree · opus · 31m
 ON DECK
   A15  ready    Add gitleaks to the data repo             #agent queued · picked up in ≤30s
   A09  ready    Write the M1 iTerm profile notes
   inbox
   A16  open     Clean /etc/shells on the M5
 ──────────────────────────────────────────────────────────────────────────────
 + add   n ready   s start   b blocked   r review   x done   a #agent   d done drawer   ? keys
```

Runs page footer (folded into the board): `f focus pane   Enter task   p pause runner   k kill run (task → blocked)   l log`.

```
 A14  blocked  Migrate push tokens to the new table                 Quirk · #agent
 ──────────────────────────────────────────────────────────────────────────────
 NOTES
 Ask: move push_token writers to notification_tokens, keep the old column
 readable until the backfill lands.

 STEPS  2/4
 [x] find every writer          [x] add the new table
 [ ] switch writers             [ ] backfill + drop

 HISTORY  across 2 sessions
 10-03 08:12  you      created · ready · #agent
 10-03 08:14  runner   claimed on M5 · routed: Quirk, worktree, opus
 10-03 08:15  agent    note  "9 writers: 7 prod, 1 migration, 1 devtools"   [a3f9c1e]
 10-03 08:20  agent    decision  worktree per writer group over one branch — principle 15
 10-03 08:23  agent    blocked  "Two writers found. Keep the devtools path or drop it?"

 FILES
 docs/agent-docs/Quirk-a14/writers.md                                  o open
 ──────────────────────────────────────────────────────────────────────────────
 e edit notes   t steps   n ready (answer in notes first)   x done   f focus pane   Esc back
```

Two defects this unit closes (found by hand on a live install, 2026-10-04):

1. The herdr board pane closes the moment it opens. `herdr-plugin.toml`'s `[[panes]] id = "board"` runs bare `desk`, which prints a static board and exits, and herdr closes a pane whose command has ended. So `scripts/open-pane.sh board` can never focus an open board.
2. A capture popup whose line is refused (`@nosuch` → `unknown-project`) must show the refusal and keep the line; the popup is desk's own.

Operator rulings that bind this unit: `ctrl+d` is never bound. Bare `desk` with stdout not a terminal keeps printing the static text board: scripts and `scripts/e2e/` read it.

## Corrections to the spec

1. **`k` is kill, so it is not "up".** The mockup page says `j/k move`; the plan's key list gives `k` to kill. Movement is `↓`/`j` down, `↑` up, `g` first, `G` last.
2. **`P` (pause runner) has no endpoint in the plan.** This unit names one, `runner.pause`, and builds only its client side. U2 serves it (agreed with the U2 build session, 2026-10-04).
3. **`runs.kill` and `runner.pause` are not served on `main`.** A home without U2 answers `HTTP 400: unknown method`. The board shows that answer on its status line. No special case.
4. **The header has no `sync 12s ago`.** The plan's header is `runner ● on · 2/3 · home` or `offline (snapshot 12m)`.
5. **`herdr-plugin.toml` does not change.** Its board pane runs `desk`; in a pane both stdin and stdout are terminals, so it is now the interactive board and stays open.
6. **The mockup's `1`, `2`, and `l` keys are not built.** The Runs page is folded into the board; a run's log is the worker's pane, which `f` focuses.

## Prior art

The plan's table, verbatim:

| artifact | verdict |
|---|---|
| statuses, exit contract, skill rules, board sections | shapes from tsk (MIT), text rewritten; no notice needed |
| install script | reuse herdr-file-viewer's fetch-or-build shape |
| daemon start | reuse auto-title's `[[startup]]` + `restart` |
| dual herdr + Claude Code plugin layout | reuse clauth's |
| journal rules | reuse `sessionLogPrune.ts` as spec |
| spawn | reuse `cl`'s `workspace create --env`; `exec` per the pane-run finding |

This unit's new artifacts:

| artifact | verdict | evidence |
|---|---|---|
| `internal/board` package (State, Run, Capture) | justified-new | no TUI exists in the repo (`.claude/build/notes.md`: "No runtime UI in this repo until the board (U3)") |
| `board.Sections` | generalize | `internal/cli/board.go:14` `boardSections` is the static board's copy; it moves to `internal/board` and the static board reads it |
| `board.Age` | generalize | `internal/cli/board.go:74` `snapshotAge` holds the same thresholds with an ` old` suffix; the rule moves to `internal/board` and the static board adds the suffix |
| `model.ParseCapture` | generalize | `internal/cli/tasks.go:515` `parseCapture`; the board's `+` line and the popup need it and `internal/board` cannot import `internal/cli` |
| `Client.KillRun`, `Client.PauseRunner` | reuse | the shape of `Client.SetTask` (`internal/api/client.go:243`): one request type, one `c.call` |
| focus a pane by id | reuse | herdr focuses by id only a pane a plugin owns (`plugin pane focus`, which `scripts/open-pane.sh` uses for the board); for a run's pane, `pane zoom <id> --on` then `--off` focuses it |
| `Options.Exec` | reuse | the shape of `cli.Env.Spawn` (`internal/cli/cli.go:31`): a func the process sets and a test replaces |
| e2e terminal driver | justified-new | `scripts/e2e/lib.sh` has no terminal; the helpers are added there, beside `run` and `wait_for` |

## Design

**Chosen shape.** `internal/board` is a pure core with a thin program around it.

- `State` is a value. `State.Update(msg)` returns the next `State` and a list of `Effect` values. It does no I/O, starts no goroutine, and reads the clock only through `Config.Now`. `State.Text()` is the screen as plain text; `State.Render()` is the same screen with styles. One view function builds both from a palette; the plain palette has no styles.
- An `Effect` is data that names one piece of I/O (`SetTask{…}`, `KillRun{…}`, `OpenRef{…}`). `board.Run` owns a bubbletea program whose model holds a `State`, turns each `Effect` into a `tea.Cmd` that calls `Home` or runs an argv, and feeds the answer back as a message. One unexported executor runs effects for both `Run` and `Capture`, so `AddTask` is run in one place.
- Only `State` asks for a refresh. After a write succeeds, the executor feeds `Tick{}` back, and `State` answers a `Tick` with the `Refresh` it needs. `Run` never builds a refresh itself.
- `Home` is an interface in `internal/board` holding the ten client calls the board makes. `*api.Client` satisfies it, and `board.go` holds `var _ Home = (*api.Client)(nil)` so the two cannot drift. Tests pass a fake.
- `CaptureState` is the one-line add box. `board.Capture` shows it as the popup, and the board's `+` key shows the same `CaptureState` with the prompt `add: `, so a refused line is kept in both.
- The board and the popup write as the user: `store.Actor{}`. The plan says "`user` for the TUI and a human's shell". `Options` carries no actor. The line reader of `desk capture` keeps the caller's actor.

Rejected:

- A bubbletea model whose `Update` returns `tea.Cmd` closures that call the client. A test would have to run each closure and a real home to see what a key does.
- Golden-file tests through `teatest`. It is an experimental module, and the notes allow the standard `testing` package only.

**Decisions.**

1. **bubbletea v2**: `charm.land/bubbletea/v2 v2.0.10`, `charm.land/lipgloss/v2 v2.0.6`, `charm.land/bubbles/v2 v2.2.1`. Source: `go doc charm.land/bubbletea/v2` at v2.0.10 — `Model.View()` returns `tea.View` (fields `Content string`, `AltScreen bool`); `tea.KeyPressMsg` is a `tea.Key` (`Text string`, `Mod KeyMod`, `Code rune`) and a test can build one. `lipgloss.Black … lipgloss.BrightWhite` are `ansi.BasicColor`, the 16 palette colours.
2. **Interactive only when stdin and stdout are both terminals and `--json` is absent.** Bare `desk` otherwise prints the static board, byte for byte as today. `desk capture` otherwise reads lines as today.
3. **The API additions are client side only**: `MethodRunsKill`, `MethodRunnerPause`, `Client.KillRun`, `Client.PauseRunner`, the `Status` fields `RunnerPaused`, `RunnerCap`, `RunnerState`, and the five `RunnerState…` constants. The board reads `RunnerState` and `RunnerOn`, never `RunnerPaused`: it is the same fact as `RunnerState == "paused"`, and it is on the wire only because U2 sets it. The server's `status` method fills `RunnerCap` from the config. Nothing else is served here. U2 adds the same names with the same text in `wire.go` and `client.go`; the PR that merges second drops its copy.
4. **What U2 writes, as agreed with its session (2026-10-04).** A run is live when `EndedTS` is the zero time (`State` is `routing`, `waiting`, or `running`). A `waiting` run has no `Workspace` and no `Pane`. `Workspace` and `Pane` hold herdr's ids. The runner's events carry `Run != 0` and `Session == ""`. `runs.kill` refuses with `no-run` or `not-allowed`. `Status.RunnerState` is one of `off`, `on`, `paused`, `no-router`, `no-herdr`, and is empty on a home without U2.
5. **Text entry uses bubbles**: `textinput` for the one-line prompts and `textarea` for the notes. Rejected: `$EDITOR` through `tea.ExecProcess`, which leaves the pane and cannot run without a terminal.
6. **The last note under a row costs one `tasks.get` per `started` or `blocked` task on each refresh.** The refresh runs every 3 seconds and after each write; the calls of one refresh run at the same time, and a `Tick` starts no refresh while one is unanswered. Assumption: a desk holds few such tasks at once. Rejected: a new list endpoint that carries last notes, which would change the server while U2 changes it.
7. **Opening a ref.** An `http` or `https` URL with a host goes to the OS opener (`open` on darwin, `xdg-open` elsewhere). Anything else is a file path: made absolute against the task's project, it must exist, and it opens in the herdr plugin `herdr-file-viewer` when herdr lists that plugin (`herdr plugin pane open --plugin herdr-file-viewer --entrypoint file-viewer --env HERDR_FILE_VIEWER_OPEN=<abs path> --focus`; source: that plugin's CHANGELOG, "Launch open target … `HERDR_FILE_VIEWER_OPEN`"). With no viewer the status line says so. A ref is text an agent wrote: it reaches an argv only as one whole argument that starts with `http://`, `https://`, or `/`, never through a shell.
8. **`n` on a `blocked` task asks for the answer.** It opens an `answer:` line. Enter with text appends a note on the task, then sets `ready`. Enter on an empty line sets `ready` alone (the answer is already in a note). This is the plan's "writes a `note`, never a replace".
9. **The hand test drives a real terminal with tmux**, on a private server (`tmux -L`), so no pane opens on anyone's screen. One claim (H25) uses the running herdr; it links a temp copy of the manifest under the id `desk-e2e`, so it never touches an installed `desk` plugin, its binary, or its home. Operator ruling, 2026-10-04: H25 runs once, in the run's last hand-test run, and opens its panes without taking focus; H26 proves the logic of `scripts/open-pane.sh` against a stub `herdr` on every run.
10. **`internal/board` joins the 90% packages in `scripts/coverage.sh`.**

## Rules every part follows

- Nothing in the repo names the author's machines, paths, agents, or harness (`AGENTS.md`). Example values are `127.0.0.1` and `example`.
- No task text, note text, or ref is ever passed through a shell. Child processes get argv arrays.
- `ctrl+d` is bound to nothing, on every surface and in every prompt.
- No sleeping in tests to wait for something: poll with a deadline.
- Tests use the standard `testing` package only, in the external package (`package board_test`).
- A test that needs a home uses `internal/testutil`. It never reads or writes the real XDG folders.
- Check each bubbletea, lipgloss, and bubbles call against the installed version (`go doc`, or context7) before writing it.
- `sh scripts/checks.sh --no-coverage` is green before a part hands off. Shell scripts pass `shellcheck -S info`.

## Substrate sweep

- `internal/cli/tasks.go:502` — `parseCapture` call in `captureLine` → `model.ParseCapture`
- `internal/cli/tasks.go:514-530` — `parseCapture` definition → deleted
- `internal/cli/board.go:13-21` — `boardSections` definition → deleted; `board.Sections()`
- `internal/cli/board.go:51` — `boardSections` loop in the static board → `board.Sections()`
- `internal/cli/board.go:42` — `snapshotAge` call in the static board's head line → keeps its text
- `internal/cli/board.go:72-88` — `snapshotAge` definition → its thresholds come from `board.Age`; `nil` still gives `of unknown age`, and every other value ends in ` old`

No test, script, or doc names these three.

## Public surface

Frozen. A test written against this block compiles unchanged. A needed rename or move is a STOP.

`internal/api/wire.go` — added to the existing blocks:

```go
const (
	MethodRunsKill    = "runs.kill"
	MethodRunnerPause = "runner.pause"
)

// The values of Status.RunnerState.
const (
	RunnerStateOff      = "off"
	RunnerStateOn       = "on"
	RunnerStatePaused   = "paused"
	RunnerStateNoRouter = "no-router"
	RunnerStateNoHerdr  = "no-herdr"
)

// Added to Status, after RunnerOn:
	// RunnerPaused is true while the runner starts no new run. RunnerCap is runner.cap. RunnerState is one of
	// the RunnerState constants, and "" on a home that has no runner.
	RunnerPaused bool   `json:"runner_paused"`
	RunnerCap    int    `json:"runner_cap"`
	RunnerState  string `json:"runner_state"`

// Added to the request bodies:
	killRequest struct {
		Actor store.Actor `json:"actor"`
		Task  int         `json:"task"`
	}
	pauseRequest struct {
		Actor  store.Actor `json:"actor"`
		Paused bool        `json:"paused"`
	}
```

`internal/api/client.go`:

```go
// KillRun kills the task's live run; the home sets the task blocked and returns it.
func (c *Client) KillRun(ctx context.Context, a store.Actor, task int) (model.Task, error)

// PauseRunner pauses or resumes the runner and returns the home's status.
func (c *Client) PauseRunner(ctx context.Context, a store.Actor, paused bool) (Status, error)
```

`internal/api/server.go`: the `status` method sets `RunnerCap: o.Config.Runner.Cap`. No new method is served.

`internal/model/capture.go`:

```go
// ParseCapture splits a captured line: a word starting # sets the thread, a word starting @ the project, and
// the rest is the title. A bare "#" or "@" is a title word.
func ParseCapture(line string) TaskData
```

`internal/board` (package `board`):

```go
// Section is one board section: its title and the statuses it lists, in order.
type Section struct {
	Title    string
	Statuses []model.Status
}

// Sections returns the board's three sections: NEEDS YOU (blocked, review), IN MOTION (started),
// ON DECK (ready, open).
func Sections() []Section

// Age prints a duration as the board does: seconds under a minute, minutes under an hour, hours under
// 48 hours, then days: 40s, 5m, 30h, 2d. A negative duration prints 0s.
func Age(d time.Duration) string

// Home is what the board asks of the home. *api.Client satisfies it.
type Home interface {
	ListTasks(ctx context.Context, f store.Filter) (api.TaskList, error)
	GetTask(ctx context.Context, number int) (store.TaskDetail, error)
	AddTask(ctx context.Context, a store.Actor, in store.AddTaskInput) (model.Task, error)
	SetTask(ctx context.Context, a store.Actor, number int, p model.Patch) (model.Task, error)
	Step(ctx context.Context, a store.Actor, number int, op model.StepOp) (model.Task, error)
	Append(ctx context.Context, r api.AppendRequest) (ev model.Event, queued bool, err error)
	ListRuns(ctx context.Context) ([]model.Run, error)
	Status(ctx context.Context) (api.Status, error)
	KillRun(ctx context.Context, a store.Actor, task int) (model.Task, error)
	PauseRunner(ctx context.Context, a store.Actor, paused bool) (api.Status, error)
}

// Config is what a State is made from.
type Config struct {
	IsHome   bool             // this machine is the home; false on a client
	CanFocus bool             // f can reach a worker's pane: the home, with herdr
	Now      func() time.Time // nil → time.Now
}

// State is the board: both pages, the open prompt, the filters, and the last data. The zero State is not usable.
type State struct{ /* unexported */ }

// NewState returns the board page with no data, 80 columns by 24 rows.
func NewState(c Config) State

// Update applies one message and returns the next State and the I/O it asks for. It does no I/O.
// It takes tea.KeyPressMsg, tea.PasteMsg, tea.WindowSizeMsg, Loaded, TaskLoaded, Added, Failed, and Tick; any
// other message changes nothing.
func (s State) Update(msg tea.Msg) (State, []Effect)

// Text is the screen as plain text: no escape byte, lines joined by "\n", no line wider than the width.
func (s State) Text() string

// Render is the screen with styles, using only the terminal's 16 palette colours.
func (s State) Render() string

// Data is one refresh of what the home holds.
type Data struct {
	Tasks      []model.Task      // the live tasks
	Done       []model.Task      // the done tasks; set only when the Refresh asked for them
	Runs       []model.Run
	Status     api.Status
	Offline    bool              // Tasks came from the snapshot; nothing else is set but SnapshotTS
	SnapshotTS *time.Time
	Notes      map[int]string    // task number → the text of its last note; started and blocked tasks only
}

// Loaded carries a Refresh's answer.
type Loaded struct{ Data Data }

// TaskLoaded carries a LoadTask's answer. A State drops one whose task is not the task it shows.
type TaskLoaded struct{ Detail store.TaskDetail }

// Added says an AddTask landed.
type Added struct{ Task model.Task }

// Failed carries the error of an effect that failed. The status line shows Err.Error() until the next key.
type Failed struct{ Err error }

// Tick asks the State to refresh: the timer firing, or a write that succeeded.
type Tick struct{}

// Effect is one piece of I/O the State asks for.
type Effect interface{ effect() }

type (
	// Refresh loads Data. Done asks for the done tasks too.
	Refresh struct{ Done bool }
	// LoadTask loads one task with its history.
	LoadTask struct{ Task int }
	// SetTask patches a task.
	SetTask struct {
		Task  int
		Patch model.Patch
	}
	// AddTask creates a task.
	AddTask struct{ Data model.TaskData }
	// StepTask changes a task's steps.
	StepTask struct {
		Task int
		Op   model.StepOp
	}
	// Rearm appends Answer as a note on the task when it is not empty, then sets the task ready.
	Rearm struct {
		Task   int
		Answer string
	}
	// KillRun kills the task's live run.
	KillRun struct{ Task int }
	// PauseRunner pauses or resumes the runner.
	PauseRunner struct{ Paused bool }
	// FocusRun focuses the run's pane.
	FocusRun struct{ Run model.Run }
	// OpenRef opens a ref of a task whose project is Dir.
	OpenRef struct {
		Ref string
		Dir string
	}
	// Quit ends the program.
	Quit struct{}
)

// CaptureState is the capture popup: one line, and the last refusal.
type CaptureState struct{ /* unexported */ }

// NewCapture returns an empty add box whose line starts with prompt ("capture: " in the popup, "add: " on the board).
func NewCapture(prompt string) CaptureState

// Update takes tea.KeyPressMsg, tea.PasteMsg, tea.WindowSizeMsg, Added, and Failed. Its effects are AddTask
// and Quit.
func (c CaptureState) Update(msg tea.Msg) (CaptureState, []Effect)

// Text is the popup as plain text; Render is the same with styles.
func (c CaptureState) Text() string
func (c CaptureState) Render() string

// Options is what Run and Capture need.
type Options struct {
	Home    Home
	IsHome  bool          // false on a client
	Herdr   string        // the herdr binary; "" → no herdr: f and the file viewer are off
	In      io.Reader     // the terminal
	Out     io.Writer
	Refresh time.Duration // 0 → 3 seconds
	// Exec runs one argv and returns its combined output. nil → os/exec with a 10 second timeout.
	Exec func(ctx context.Context, argv []string) ([]byte, error)
}

// Run shows the board on the alternate screen until the user quits or ctx ends. Its writes are the user's.
func Run(ctx context.Context, o Options) error

// Capture shows the popup until one task lands or the user cancels. It returns the task, or nil on a cancel.
func Capture(ctx context.Context, o Options) (*model.Task, error)
```

`internal/cli/cli.go` — `Env` gains one field, after `StdinTTY`:

```go
	StdoutTTY bool
```

No other exported name changes. `cli.Env` gains no `Exec` field here (U2 adds one of its own).

### Behaviour

Everything below is `State`'s contract. Strings in backticks are exact.

**Screen, board page.** Line 1 is the header. Left: `desk  <project> ▾  thread: <thread> ▾`, where `<project>` is `all`, a project's base name, or `no project`, and `<thread>` is `all` or a thread. Right, online: `runner ● on · <live>/<cap> · home`, `runner ◐ paused · <live>/<cap> · home`, or `runner ○ off · home`; on a client the last word is `client`. `<live>` counts the runs whose `EndedTS` is zero; with `RunnerCap` 0 the count stands alone (`runner ● on · 2 · home`). The word after the dot is `Status.RunnerState` when it is set, else `on` or `off` from `RunnerOn`; `●` for `on`, `◐` for `paused`, `○` for anything else. Right, offline: `offline (snapshot <Age>)`. Then a rule line, the three sections, a rule line, the footer, and the status line.

Each section prints its title even when empty. A row is `▸ ` (selected) or two spaces, `T<n>`, the status padded to 7, the title, and a right-hand detail. ON DECK lists `ready` tasks, then the line `inbox`, then `open` tasks. Rows inside one status keep the order of `Data.Tasks`.

- `blocked`, `review`, `ready`, `open` detail: `<project base> · <Age since UpdatedTS> ago`, without the project part when the task has none.
- `started` with a live run: `<root base> · <isolation> · <model> · <Age since the run's StartedTS>`, skipping unset fields. Without one: as above.
- A `ready` task whose thread is `agent`: `#agent · queued` before the detail. Any other task with a thread: `#<thread>` before the detail.
- A `blocked` task with a last note: a second line `↳ "<note>"`. A `started` task with one: `↳ last note: "<note>"`. One line each, cut with `…`.

The footer is `+ add  n ready  s start  b blocked  r review  x done  a #agent  f focus  k kill  P pause  / search  p project  t thread  d done  ? keys`. It wraps at its double-space groups when the width is less than its length, and is `? keys  q quit` under 78 columns. The selection follows its task across refreshes; when the task leaves the list, the row at the same index is selected. The list scrolls so the selected row is always on screen.

**Keys, board page** (no prompt open):

| key | does |
|---|---|
| `↓` `j` · `↑` · `g` · `G` | next row · previous row · first · last |
| `enter` | opens the selected task's page; emits `LoadTask{n}` |
| `+` | opens a `CaptureState` with the prompt `add: `, drawn in place of the footer. Its `AddTask` is emitted as it is; its `Quit` (an empty line, `esc`) closes the box and does not end the program. While it is open, `Failed` goes to it: the refusal shows under the line and the line is kept. `Added` closes it and emits the refresh |
| `n` | `SetTask{status ready}`; on a `blocked` task opens the `answer: ` prompt: Enter emits `Rearm{Task, Answer: <the trimmed line>}`, `esc` closes it |
| `s` · `b` · `r` | `SetTask` with status `started` · `blocked` · `review` |
| `x` | on a `review` task `SetTask{status done}` at once; on any other status the prompt `mark T<n> done? y/n`: `y` emits it, any other key closes the prompt |
| `a` | `SetTask{thread}`: `agent` when the task's thread is not `agent`, else `""` |
| `f` | with `CanFocus` and a live run that has a `Pane`: `FocusRun{run}`. Else the status line: `f works only on the home, with herdr`, `T<n> has no live run`, or `T<n>'s run has no pane yet` |
| `k` | with a live run: the prompt `kill T<n>'s run? y/n`; `y` emits `KillRun{n}`. Else `T<n> has no live run` |
| `P` | with `RunnerState` `on`, or empty while `RunnerOn` is true (the header's own fallback): `PauseRunner{Paused: true}`; with `paused`: `PauseRunner{Paused: false}`; with anything else the status line `the runner is <RunnerState>`, or `the runner is off` when it is empty and `RunnerOn` is false |
| `/` | opens the `search: ` prompt; rows are filtered as the line changes to tasks whose title or `T<n>` holds the text, any case. Enter keeps the filter, `esc` clears it |
| `p` | next project filter: `all`, each project among the tasks by base name, `no project` (only when a task has none), back to `all` |
| `t` | next thread filter: `all`, each thread among the tasks in order, back to `all` |
| `d` | opens or closes the done drawer: a `DONE` section under ON DECK with the done tasks, newest `UpdatedTS` first, 20 at most, then `+<n> more`. Opening emits `Refresh{Done: true}` |
| `?` | opens or closes the `KEYS` overlay: one line per key on this page |
| `esc` | closes the overlay, else the drawer, else clears the search filter |
| `q` · `ctrl+c` | `Quit` |

With no row selected (an empty board) a key that needs a task does nothing.

**Offline** (`Data.Offline`). Every key that writes (`+ n s b r x a k P` here; `e n s b r x a k P R I M` and the step keys on the task page) emits nothing and sets the status line to `offline: <key> needs the home`. `d` sets `offline: the done drawer needs the home`. `enter` opens the task page with what the snapshot holds: no history, and no `LoadTask`. Moving, filters, `?`, and `q` work.

**Task page.** Head line: `T<n>  <status>  <title>`, right `<project base> · #<thread>`. Then a rule, and:

- `root <r> · isolation <i> · model <m>`, `-` for an unset field.
- `NOTES`, then the notes, wrapped.
- `STEPS  <done>/<all>`, then `[x] <text>` or `[ ] <text>` per step. Absent when the task has no step.
- `HISTORY  across <n> sessions` (distinct non-empty `Session` values; `across 1 session`), then one line per event, oldest first: local `MM-DD HH:MM`, who, text. Who is `runner` when `Run != 0` and `Session == ""`, `agent` when `Who` is `agent`, else `you`. Text by kind: `task` → `created · <status>` plus ` · #<thread>` when set; `set` → the changed fields in the order of `model.Patch` (`<status>`, `title`, `notes`, `thread #<t>`, `root <r>`, `isolation <i>`, `model <m>`, and `archived` or, for a set to false, `unarchived`), joined by ` · `, plus ` · merged` and ` [<ref>]` when set; a `set` to `started` by the runner whose run is in `Data.Runs` → `claimed · routed: <root base>, <isolation>, <model>` plus ` — <reason>` when the run has one; `step` → `step <op> <text or short id>`; `note` → `note "<text>"` plus ` [<ref>]`; `decision` → `decision <text>`; `merged` → `merged <branch>`.
- `FILES`, then each distinct ref of the history's notes and sets, in order. Absent when there is none.
- The footer: `e notes  t steps  n ready  x done  o open  R root  I isolation  M model  f focus  esc back`.

Keys: `↓` `j` and `↑` scroll. `n s b r x a f k P ? q ctrl+c` do what they do on the board, for this task. And:

| key | does |
|---|---|
| `esc` | back to the board page |
| `e` | opens the notes editor holding the task's notes. `ctrl+s` emits `SetTask{notes}` and closes it; `esc` closes it unsaved |
| `t` | steps mode: `↓` `j` `↑` move over the steps; `space` or `enter` emits `StepTask{toggle}`; `a` opens `step: ` and Enter emits `StepTask{add}`; `r` opens `step: ` holding the step's text and Enter emits `StepTask{rename}`; `x` emits `StepTask{remove}`; `esc` leaves the mode. A task with no step enters the mode and takes `a` |
| `R` · `M` | opens `root: ` · `model: ` holding the field; Enter emits `SetTask` with it (an empty line clears the field) |
| `I` | emits `SetTask{isolation}` with the next of `""`, `self`, `worktree`, `in-place` |
| `o` | with one ref: `OpenRef{Ref, Dir: the task's project}`. With several: a pick list (`↓` `j` `↑`, `enter` opens, `esc` closes). With none: `T<n> has no ref` |

**Layouts.** Under 78 columns: one surface at a time, and board rows carry no right-hand detail. From 78 to 109: one surface, full rows. From 110: the board on the left and the selected task's page on the right, always; a move of the selection emits `LoadTask` for the new task (and nothing else); `enter` gives the keys to the task side and `esc` gives them back.

**Data.** `Tick` emits `Refresh{Done}` with `Done` as the drawer stands, plus `LoadTask` for the task a page shows (the open page, or at 110 columns the selection) unless the data is offline. While a `Refresh` is unanswered (no `Loaded` or `Failed` since), a `Tick` emits nothing and is remembered; the `Loaded` or `Failed` that answers then emits, once, what a `Tick` emits (the `Refresh`, and the shown task's `LoadTask`). A `tea.PasteMsg` goes to the open text input (a prompt, the add box, the notes editor) and changes nothing when none is open. `Loaded` replaces the data. `TaskLoaded` replaces the shown task's detail, and is dropped when its task is not the one shown. `Added` acts as a `Tick`. `Failed` sets the status line to `Err.Error()`; the next key press clears it.

**Colours.** `blocked` red, `review` yellow, `started` green, `ready` cyan, `open` and details bright black, section titles bold yellow, ids blue, `#thread` magenta. `Render()` holds no `38;2;`, `48;2;`, `38;5;`, or `48;5;` sequence.

**Run.** Starts by feeding `Tick{}`, feeds one every `Options.Refresh`, and draws `State.Render()` on the alternate screen. Every write is made as `store.Actor{}`. Per effect:

- `Refresh`: `ListTasks(Filter{})`. Offline → `Loaded` with `Tasks`, `Offline`, `SnapshotTS` only. Else `Status`, `ListRuns`, one `GetTask` for each `started` or `blocked` task, all at the same time (the last `note` event's text goes in `Notes`), and the done list (`Filter{Statuses: done}`) when asked. Any error → `Failed`.
- `LoadTask`: one `GetTask` → `TaskLoaded`. An error → `Failed`.
- `AddTask`: the `Home` call → `Added`. An error → `Failed`.
- `SetTask`, `StepTask`, `KillRun`, `PauseRunner`: the `Home` call → `Tick{}`. An error → `Failed`.
- `Rearm`: with an answer, `Append` a note on the task, then `SetTask{status ready}` → `Tick{}`. A queued note (the home did not answer) → `Failed`, and the status is not set.
- `FocusRun`: `[herdr, "workspace", "focus", <Workspace>]`, then `[herdr, "pane", "zoom", <Pane>, "--on"]`, then the same with `--off`. An id that does not match `^[A-Za-z0-9][A-Za-z0-9:_.-]*$` → `Failed`, and nothing runs.
- `OpenRef`: Design decision 7. Whether the viewer is there is asked once per program: `[herdr, "plugin", "list", "--plugin", "herdr-file-viewer", "--json"]`, read as `.result.plugins` holding at least one entry.
- `Quit`: the program ends and `Run` returns nil.

**Capture box** (`CaptureState`). Line 1 the prompt (`capture: ` in the popup) and the line. Line 2 `#thread  @project  ·  enter adds  ·  esc cancels`. Line 3 the last refusal, when there is one. Enter with text emits `AddTask{model.ParseCapture(line)}`; `Added` emits `Quit`; `Failed` shows `Err.Error()` and keeps the line as typed; Enter on an empty line, `esc`, or `ctrl+c` emits `Quit` with no task. `board.Capture` does not use the alternate screen.

**CLI.**

- Bare `desk`: with `StdinTTY`, `StdoutTTY`, and no `--json`, it runs `board.Run` with `Home` the client, `IsHome` `!cfg.IsClient()`, `Herdr` the value of `HERDR_BIN_PATH` when set, else `herdr` found on `PATH`, else `""`, and `In`/`Out` the `Env`'s streams. It prints no offline warning on stderr; the banner says it. Otherwise it prints the static board, with the text it prints today.
- `desk capture`: with `StdinTTY`, `StdoutTTY`, and no `--json`, it runs `board.Capture`, and prints `T<n>` when a task lands. Otherwise it reads lines as it does today.

### Notes for the test writers

- W1: this branch serves neither method. Test the calls against a fake home: an `httptest` server on 127.0.0.1 that the client reaches as a client machine (`[client] home`, a token), which checks the path (`/v1/runs.kill`, `/v1/runner.pause`) and the body, and answers a task, a status, or a 409 refusal. `Client.Status` against `testutil.StartHome` carries `runner_cap` from the config.
- W2, W3, W4: build a `State` with `NewState`, feed it `tea.WindowSizeMsg`, `Loaded`, and `tea.KeyPressMsg` values, and assert on the effects (`reflect.DeepEqual`) and on `Text()`. A letter key is `tea.KeyPressMsg{Code: 'n', Text: "n"}`; a named key is `tea.KeyPressMsg{Code: tea.KeyEnter}`; `ctrl+d` is `tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}`. Cover every row of the key tables, a refused `+` line that stays in its box, every offline refusal, `ctrl+d` on every surface and in every prompt, and the three widths 60, 90, 120.
- W5, W6: `Options.In` is a pipe the test writes keys to; `Options.Out` is a buffer. Poll the fake home's recorded calls with a deadline.
- W7: `cli.Run` with `StdinTTY` and `StdoutTTY` set and `Stdin` holding `q` runs the board and exits 0; with either unset it prints the static board.

## Target files

- internal/board/board.go — Home and its `*api.Client` assertion, Options, Run, Capture, and the one effect executor
- internal/board/state.go — State, Config, Data, the messages, the effects, Update for the board page
- internal/board/view.go — Sections, Age, Text, Render, the palette, the layouts
- internal/board/task.go — the task page: its keys and its view
- internal/board/capture.go — CaptureState
- internal/board/open.go — the argv of FocusRun and OpenRef, and their guards
- internal/api/wire.go — the two methods, the Status fields, the RunnerState constants, the two request bodies
- internal/api/client.go — KillRun, PauseRunner
- internal/api/server.go — status reports RunnerCap
- internal/model/capture.go — ParseCapture
- go.mod — the three charm modules
- go.sum
- THIRD_PARTY_NOTICES.md — the modules the binary now links
- cmd/desk/main.go — StdoutTTY
- internal/cli/cli.go — Env.StdoutTTY
- internal/cli/board.go — bare desk picks the board or the static text
- internal/cli/tasks.go — capture picks the popup or the line reader; parseCapture goes
- scripts/coverage.sh — internal/board joins the 90% packages
- scripts/e2e/lib.sh — the terminal helpers
- scripts/e2e/h17-board-open.sh
- scripts/e2e/h18-board-keys.sh
- scripts/e2e/h19-board-filters.sh
- scripts/e2e/h20-task-page.sh
- scripts/e2e/h21-board-offline.sh
- scripts/e2e/h22-board-layouts.sh
- scripts/e2e/h23-capture-popup.sh
- scripts/e2e/h24-runner-keys.sh
- scripts/e2e/h25-herdr-panes.sh
- scripts/e2e/h26-open-pane-stub.sh
- scripts/open-pane.sh — finds the board pane by a field a rename leaves alone; herdr 0.9.1's busy-popup answer
- README.md — the board, its keys, the capture popup
- profiles/claude-code/skills/desk/SKILL.md — an agent never opens the board
- .claude/build/notes.md — the builders and hand-tester sections

## Why the parts wait

P2 calls `board.Run`, `board.Capture`, `board.Sections`, `board.Age`, and `model.ParseCapture`, and its scripts drive the real board. It cannot compile or run before P1 is merged. P1 is planned near `PART_MAX_LINES` and is not cut: the two pages, the prompts, and the layouts are one state machine over one `State`, and a cut would freeze an interface between two halves that nothing else needs. P3 was added after P2 reported (operator ruling, 2026-10-04) and edits a script P2 wrote, so it waits for P2.

## Parts

- P1 · the board package, the API client calls, ParseCapture
  - model: opus — `## Design` names its files: the State and Effect shape, the Home interface, the ref rules
  - files: internal/board/board.go, internal/board/state.go, internal/board/view.go, internal/board/task.go, internal/board/capture.go, internal/board/open.go, internal/api/wire.go, internal/api/client.go, internal/api/server.go, internal/model/capture.go, go.mod, go.sum, THIRD_PARTY_NOTICES.md
  - test files: none
  - deliverables: 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20
  - after: none
  - tests: W1, W2, W3, W4, W5, W6
- P2 · the CLI wiring, the hand-test scripts, the docs
  - model: sonnet — the brief names every file and what each change must do
  - files: cmd/desk/main.go, internal/cli/cli.go, internal/cli/board.go, internal/cli/tasks.go, scripts/coverage.sh, scripts/e2e/lib.sh, scripts/e2e/h17-board-open.sh, scripts/e2e/h18-board-keys.sh, scripts/e2e/h19-board-filters.sh, scripts/e2e/h20-task-page.sh, scripts/e2e/h21-board-offline.sh, scripts/e2e/h22-board-layouts.sh, scripts/e2e/h23-capture-popup.sh, scripts/e2e/h24-runner-keys.sh, scripts/e2e/h25-herdr-panes.sh, README.md, profiles/claude-code/skills/desk/SKILL.md, .claude/build/notes.md
  - test files: none
  - deliverables: 21, 22, 23, 24, 25, 26, 27, 28
  - after: P1
  - tests: W7
- P3 · open-pane.sh survives a renamed pane, and the stub-herdr proof of it
  - model: opus — `## Design` 9 names its files: how the herdr claims run
  - files: scripts/open-pane.sh, scripts/e2e/h26-open-pane-stub.sh, scripts/e2e/h25-herdr-panes.sh
  - test files: none
  - deliverables: 29, 30
  - after: P2
  - tests: none

## Test slices

- W1 · plain · the API client's two calls and ParseCapture
  - files: internal/api/runner_test.go, internal/model/capture_test.go
  - covers: 1, 2
  - under test: Client.KillRun, Client.PauseRunner, Client.Status, ParseCapture
- W2 · plain · what each board-page key asks for
  - files: internal/board/keys_test.go
  - covers: 5, 6, 7, 8, 11
  - under test: NewState, State.Update
- W3 · plain · what the board page draws
  - files: internal/board/view_test.go
  - covers: 3, 4, 9, 10, 15, 16
  - under test: Sections, Age, State.Text, State.Render
- W4 · plain · the task page
  - files: internal/board/task_test.go
  - covers: 12, 13, 14
  - under test: State.Update, State.Text
- W5 · plain · Run against a fake home and a fake Exec
  - files: internal/board/run_test.go
  - covers: 17, 18
  - under test: Run
- W6 · plain · the capture popup
  - files: internal/board/capture_test.go
  - covers: 19
  - under test: NewCapture, CaptureState.Update, CaptureState.Text, Capture
- W7 · plain · bare desk and capture pick the terminal path only on a terminal
  - files: internal/cli/boardtty_test.go
  - covers: 21, 22, 23
  - under test: Run

## How the hand test runs

Each claim is one script under `scripts/e2e/`, as in U1. H17 to H24 run the board in a detached tmux session on a private server (`tmux -L`), with temp XDG directories, send keys with `tmux send-keys`, read the screen with `tmux capture-pane -p`, and read the store with `desk list --json` and `desk show --json`. Nothing opens on a screen. H24 seeds one run row with `sqlite3` while the daemon is stopped, and puts a stub `herdr` in `HERDR_BIN_PATH` that logs its argv. H25 uses the running herdr: it builds `desk` into a temp folder, copies `herdr-plugin.toml` and `scripts/` there with the id changed to `desk-e2e`, the `[[build]]` and `[[startup]]` blocks removed, and each pane command wrapped as `env XDG_…=<temp> <temp>/desk …`, links that copy, invokes its actions, and on exit closes the panes it opened and unlinks `desk-e2e`. It never touches an installed `desk` plugin, binary, or home. H25 opens every pane without taking focus (its temp copy of `open-pane.sh` opens with `--no-focus`); only the second `open-board`, the one that must focus the board, moves focus, and the script gives focus back. herdr gives a popup no pane id to type into or read, so H25 opens the manifest's capture entrypoint as a split pane, which runs the same `desk capture`; its `ok:` lines still say popup. H25 runs once, in the run's last hand-test run. H26 runs `scripts/open-pane.sh` itself against a stub `herdr` on `HERDR_BIN_PATH` that logs its argv and answers `pane list` from fixtures recorded from a real herdr; it opens nothing on any screen. Each script prints `ok: <fact>` as it proves a fact, and `E2E PASS` last.

## Hand test

- H17 · bare desk on a terminal is the board and stays open; piped, it is the static text
  - run: `bash scripts/e2e/h17-board-open.sh`
  - pass: exit 0; the output holds `ok: the screen shows NEEDS YOU, IN MOTION, ON DECK`, `ok: the header shows runner ○ off · home`, `ok: the board is still running after 2 s`, `ok: piped, the first line is desk · home · runner off`, `ok: q ends it with exit 0`, and `E2E PASS`
- H18 · every status key on the board page writes what the mockup says
  - run: `bash scripts/e2e/h18-board-keys.sh`
  - pass: exit 0; the output holds one `ok:` line each for `+ adds a task with its #thread`, `n sets ready`, `s sets started`, `b sets blocked`, `r sets review`, `x on review sets done and asks nothing`, `x on open asks, and y sets done`, `x then another key leaves the task as it was`, `a sets the thread agent and the row shows #agent · queued`, `n on a blocked task asks for the answer, writes the note, and sets ready`, `ctrl+d changes nothing and the board is still running`, and `E2E PASS`
- H19 · search, the project and thread filters, the done drawer, and the keys overlay
  - run: `bash scripts/e2e/h19-board-filters.sh`
  - pass: exit 0; the output holds one `ok:` line each for `/ hides the rows that do not match`, `esc clears the search`, `p shows one project's tasks`, `t shows one thread's tasks`, `d shows DONE with the done task`, `d again hides it`, `? shows KEYS`, and `E2E PASS`
- H20 · the task page shows notes, steps, history, and files, and its keys edit the task
  - run: `bash scripts/e2e/h20-task-page.sh`
  - pass: exit 0; the output holds one `ok:` line each for `enter shows NOTES, STEPS 1/2, HISTORY, FILES`, `the history names you and agent and holds the note and the decision`, `e then ctrl+s replaces the notes`, `t then space toggles a step`, `t then a adds a step`, `R sets the root`, `I sets the isolation`, `M sets the model`, `esc returns to the board`, and `E2E PASS`
- H21 · with the home down, a client's board shows the offline banner and refuses n
  - run: `bash scripts/e2e/h21-board-offline.sh`
  - pass: exit 0; the output holds `ok: the header shows offline (snapshot`, `ok: n shows offline: n needs the home`, `ok: after the home returns the task is still open`, `ok: the banner clears and n sets ready`, and `E2E PASS`
- H22 · under 78 columns one surface, from 110 the board beside the task
  - run: `bash scripts/e2e/h22-board-layouts.sh`
  - pass: exit 0; the output holds `ok: at 70 columns no line is wider than 70 and the task page replaces the board`, `ok: at 100 columns the rows carry their detail`, `ok: at 120 columns NEEDS YOU and HISTORY are on one screen`, and `E2E PASS`
- H23 · the capture popup lands a task, and shows a refused line and keeps it
  - run: `bash scripts/e2e/h23-capture-popup.sh`
  - pass: exit 0; the output holds `ok: a line with #tour lands a task with the thread tour and the popup ends`, `ok: a line with @nosuch shows unknown-project and the popup is still open with the line`, `ok: esc ends it with no task`, and `E2E PASS`
- H24 · a live run shows in IN MOTION and the header, and k, P, f, and o make their calls
  - run: `bash scripts/e2e/h24-runner-keys.sh`
  - pass: exit 0; the output holds `ok: the row shows the run's isolation and model`, `ok: the header counts 1 live run`, `ok: k asks before it kills, and another key cancels`, `ok: k then y calls runs.kill`, `ok: P calls runner.pause or says the runner is off`, `ok: f ran herdr workspace focus and pane zoom`, `ok: o ran the file viewer with HERDR_FILE_VIEWER_OPEN`, and `E2E PASS`
- H25 · in the running herdr, open-board opens the board pane and a second press focuses it; capture lands a task and shows a refusal
  - run: `bash scripts/e2e/h25-herdr-panes.sh`
  - pass: exit 0; the output holds `ok: open-board opened one pane titled desk and it is still open after 2 s`, `ok: a second open-board left one pane and it is focused`, `ok: the capture popup landed a task`, `ok: a refused line shows unknown-project in the popup`, `ok: every pane this script opened is closed and desk-e2e is unlinked`, and `E2E PASS`
- H26 · open-pane.sh focuses the open board pane even after a title plugin renamed it, opens one when none is open, and takes a busy popup as done — against a stub herdr
  - run: `bash scripts/e2e/h26-open-pane-stub.sh`
  - pass: exit 0; the output holds `ok: with no board pane, board opens one`, `ok: with a board pane whose label was renamed, board focuses it and opens none`, `ok: capture opens the popup`, `ok: a busy popup exits 0`, `ok: an unknown pane name exits 2`, and `E2E PASS`

## Deliverables

1. `internal/api` gains `MethodRunsKill`, `MethodRunnerPause`, `Client.KillRun`, `Client.PauseRunner`, `Status.RunnerPaused`, `RunnerCap`, `RunnerState`, and the five `RunnerState…` constants; the `status` method reports `runner.cap`. Before: no client call for either method, and no cap in the status. After: each call posts its request body and returns the home's answer or refusal. (§Public surface; §Design 3)
2. `model.ParseCapture` exists with the behaviour of `cli`'s `parseCapture`. Before: the rule lived in `internal/cli`. After: one definition, in `internal/model`. (§Prior art)
3. `board.Sections` and `board.Age` are the one definition of the sections and of the age text. (§Prior art)
4. The board page draws the header, the three sections with their rows, details, last-note lines, the `inbox` line, the footer, and the status line, as §Behaviour gives them. Before: no interactive board. (spec)
5. Movement, selection, `enter`, `esc`, `q`, and `ctrl+c` do what §Behaviour says; `ctrl+d` emits nothing and changes nothing on every surface and in every prompt. (spec; operator ruling)
6. `n`, `s`, `b`, `r`, `x`, and `a` emit the `SetTask` §Behaviour names; `x` asks unless the task is in `review`. (spec)
7. `+` opens the add box and emits `AddTask` with the parsed line; a refused line stays in the box with its refusal, and `Added` closes it. `n` on a `blocked` task opens the answer prompt and emits `Rearm`. (spec; defect 2; §Design 8)
8. `f`, `k`, and `P` emit `FocusRun`, `KillRun` after `y`, and `PauseRunner`, or set the status line §Behaviour names. (spec; §Corrections 2)
9. `/`, `p`, and `t` filter the rows; `d` opens the done drawer; `?` opens the keys overlay. (spec)
10. Offline: the header shows `offline (snapshot <age>)`, every writing key emits nothing and names the home on the status line, and reading keys work. (spec: Acceptance)
11. `Tick` asks for the refresh and the shown task's `LoadTask` that §Behaviour names, and for nothing while a refresh is unanswered; `TaskLoaded` for a task not shown is dropped; `Failed` shows its error until the next key. (§Design)
12. The task page draws the head, the fields line, NOTES, STEPS, HISTORY with who and text per kind, and FILES. (spec)
13. On the task page `e`, `t` and the step keys, `R`, `I`, and `M` emit the effects §Behaviour names. (spec)
14. `o` emits `OpenRef` for the one ref, or opens the pick list for several. (spec)
15. The three layouts: under 78 one surface without row details, 78 to 109 one surface, from 110 the board beside the task. No line of `Text()` is wider than the width. (spec)
16. `Render()` uses only the 16 palette colours; `Text()` holds no escape byte. (spec)
17. `board.Run` runs each effect against `Home` as `store.Actor{}` through the one executor `Capture` shares, feeds `Tick` on its timer and after each write, and ends on `Quit`; `var _ Home = (*api.Client)(nil)` compiles. (§Behaviour: Run; §Design)
18. `FocusRun` and `OpenRef` run only the argv §Behaviour names; a pane or workspace id outside the pattern, a ref that is neither an `http(s)` URL with a host nor an existing file, and a missing viewer each give `Failed` and run nothing. (§Design 7; `.claude/build/notes.md` §all readers)
19. `CaptureState` and `board.Capture`: a line lands a task and ends the popup; a refusal is shown and the line kept; an empty line, `esc`, or `ctrl+c` ends it with no task. Before: a refused line in a popup was lost with the pane. (spec; defect 2)
20. `go.mod` and `go.sum` carry the three charm modules at the versions §Design 1 names, `go mod tidy` leaves them unchanged, the build works with `CGO_ENABLED=0`, and `THIRD_PARTY_NOTICES.md` lists every module the binary now links, in the file's existing form. (§Design 1; plan §Release)
21. `cli.Env.StdoutTTY` exists and `cmd/desk/main.go` sets it from stdout. Bare `desk` runs `board.Run` when stdin and stdout are terminals and `--json` is absent. Before: it always printed the static board and exited, so the herdr pane closed. After: on a terminal it stays open. (defect 1; §Design 2)
22. Bare `desk` off a terminal, or with `--json`, prints what it prints today, from `board.Sections` and `board.Age`; `boardSections` is gone from `internal/cli`. (operator ruling; §Substrate sweep)
23. `desk capture` runs `board.Capture` when stdin and stdout are terminals and `--json` is absent, and prints `T<n>`; otherwise it reads lines as today, as the caller's actor. `parseCapture` is gone from `internal/cli`. (defect 2; §Substrate sweep)
24. `scripts/e2e/lib.sh` gains the terminal helpers, and `scripts/e2e/h17` to `h25` exist, one per claim, each printing the `ok:` lines its claim names. All pass `shellcheck -S info`. (operator instruction)
25. `scripts/coverage.sh` holds `internal/board` in its 90% list. (§Design 10)
26. `README.md`: the intro no longer says the board comes later; the `desk` row describes the board on a terminal and the static text off one; a section lists every key of both pages; the `desk capture` row describes the popup. (`.claude/build/notes.md` §builders: Docs)
27. `profiles/claude-code/skills/desk/SKILL.md` says an agent never runs bare `desk` or `desk capture` on a terminal: the board is the user's. (`.claude/build/notes.md` §builders: Docs)
28. `.claude/build/notes.md`: the builders line "No runtime UI in this repo until the board (U3)" becomes the rule that board logic lives in `State.Update` and is tested there; the hand-tester section names `tmux` and `sqlite3` among the tools and says H25 opens and closes panes in the running herdr under the id `desk-e2e`. (§Design 9)
29. `scripts/open-pane.sh` finds the open board pane by a field of herdr's pane list that a rename of the pane leaves alone, and takes herdr 0.9.1's `a popup pane is already open` answer as done (exit 0). Before: it matched the label `desk`, which a title plugin rewrites seconds after the pane opens, so a second press opened a second pane; and it matched `popup already open`, a text herdr 0.9.1 does not print. After: a second press focuses the one board pane, renamed or not, and a second capture press exits 0. (defect 1; P2 builder's report)
30. `scripts/e2e/h26-open-pane-stub.sh` exists: it runs `scripts/open-pane.sh` against a stub `herdr` whose `pane list` answers are fixtures recorded from a real herdr, prints the `ok:` lines its claim names, opens nothing on a screen, and passes `shellcheck -S info`. `scripts/e2e/h25-herdr-panes.sh` still passes against the changed `open-pane.sh`. (operator ruling, 2026-10-04)

```yaml
description: desk U3 — the bubbletea board, the capture popup, the client calls for kill and pause
deliverables:
  - name: api gains runs.kill and runner.pause client calls and the Status runner fields
    covered_by: [judgment]
  - name: model.ParseCapture is the one capture-line rule
    covered_by: [judgment]
  - name: board.Sections and board.Age are the one definition of sections and age text
    covered_by: [judgment]
  - name: the board page draws header, sections, rows, details, footer, status line
    covered_by: [judgment]
  - name: movement, enter, esc, quit; ctrl+d bound to nothing anywhere
    covered_by: [judgment]
  - name: n s b r x a emit their SetTask; x asks unless review
    covered_by: [judgment]
  - name: the add box emits AddTask and keeps a refused line; n on blocked emits Rearm
    covered_by: [judgment]
  - name: f k P emit FocusRun, KillRun after y, PauseRunner, or name why not
    covered_by: [judgment]
  - name: search, project and thread filters, done drawer, keys overlay
    covered_by: [judgment]
  - name: offline banner; writing keys refuse and name the home
    covered_by: [judgment]
  - name: Tick asks for the refresh and LoadTask, never while one is unanswered; Failed shows until the next key
    covered_by: [judgment]
  - name: the task page draws head, fields, NOTES, STEPS, HISTORY, FILES
    covered_by: [judgment]
  - name: task-page e, steps mode, R, I, M emit their effects
    covered_by: [judgment]
  - name: o emits OpenRef or opens the pick list
    covered_by: [judgment]
  - name: three layouts by width; no line wider than the width
    covered_by: [judgment]
  - name: Render uses only the 16 palette colours; Text has no escape byte
    covered_by: [judgment]
  - name: board.Run runs each effect against Home, refreshes, ends on Quit
    covered_by: [judgment]
  - name: FocusRun and OpenRef run only the named argv; guards give Failed
    covered_by: [judgment]
  - name: CaptureState and board.Capture land a task, show a refusal, keep the line
    covered_by: [judgment]
  - name: go.mod, go.sum, THIRD_PARTY_NOTICES carry the charm modules
    covered_by: [judgment]
  - name: Env.StdoutTTY; bare desk runs the board on a terminal
    covered_by: [judgment]
  - name: bare desk off a terminal prints today's static text from board.Sections and board.Age
    covered_by: [judgment]
  - name: desk capture runs the popup on a terminal; parseCapture gone from cli
    covered_by: [judgment]
  - name: e2e terminal helpers and scripts h17 to h25, shellcheck -S info clean
    covered_by: [judgment]
  - name: coverage.sh holds internal/board at 90 percent
    covered_by: [judgment]
  - name: README documents the board, its keys, and the popup
    covered_by: [judgment]
  - name: SKILL.md says an agent never opens the board
    covered_by: [judgment]
  - name: build notes updated for the board and the hand tester
    covered_by: [judgment]
  - name: open-pane.sh finds a renamed board pane and takes the busy-popup answer as done
    covered_by: [judgment]
  - name: h26 proves open-pane.sh against a stub herdr; h25 still passes
    covered_by: [judgment]
```

--- brief complete ---
