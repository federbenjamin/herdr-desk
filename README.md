# herdr-desk

A task board for you and your agents, with a coordinator agent that turns what you ask for into tasks
and runs, and a journal that gives every agent session a memory.

Status: pre-release, under construction. This version has the store, the CLI, the journal, the
coordinator and the runner, the board, and the packaging.

## What it is

- **Tasks.** `herdr-desk add`, `list`, `show`, `set`, `edit`, `steps`. Statuses are `open`, `ready`,
  `started`, `blocked`, `review`, and `done`. `ready` means approved, not started: a person sets it,
  and a run starts only when someone calls `herdr-desk run start`. Agents may propose tasks and set
  `review`, `blocked`, or `done`; they set `ready` only when `[coordinator] start_runs` is `auto`.
- **A journal.** `herdr-desk note` and `herdr-desk decide` record facts from a session. `herdr-desk session <id> --md`
  renders the session's Work log, Todo, and Decisions, hiding what a merge or a compaction made
  stale. A Claude Code hook prints the view's path at the start of every session.
- **A coordinator.** `herdr-desk coordinator` opens one agent session per desk in its own herdr
  workspace. It talks with you, creates tasks, and starts runs by calling the binary. It never does the
  work. See [The coordinator](#the-coordinator).
- **Runs.** `herdr-desk run start T12` starts an agent on a task in a herdr workspace. herdr's own pane
  events track it and hand the task back to you. See [The runner](#the-runner).
- **One home per desk.** One machine owns the SQLite store. Every command on it opens the store, runs,
  and closes it; no background service, socket, listener, or token is involved. Every other machine is a client and
  sends its requests to the home over ssh.

## Install

herdr-desk is one static binary, `herdr-desk`. Pick the line that fits.

### herdr with Claude Code

```sh
herdr plugin install federbenjamin/herdr-desk
```

herdr runs `scripts/fetch-or-build.sh`: it downloads the release binary for your platform, checks
its SHA-256 against `checksums.txt`, and builds from source with Go when no release matches. When no
`herdr-desk` is on your PATH it copies the binary to `~/.local/bin/herdr-desk` and says so. A later install
replaces that copy and tells you to run `herdr-desk ticker stop`; herdr's next start runs the new ticker. A
`herdr-desk` from anywhere else (Homebrew, `go install`) is left alone.

Then set up the home and the Claude Code plugin:

```sh
herdr-desk setup --profile claude-code
```

In Claude Code:

```
/plugin marketplace add federbenjamin/herdr-desk
/plugin install herdr-desk@herdr-desk
```

Claude Code keeps a plugin by its version. A release changes the version, so `claude plugin update
herdr-desk@herdr-desk` picks up the new skill. A build from source between releases keeps the version, so
`claude plugin update` says it is already at the latest and keeps the old skill: remove the plugin and
install it again with `claude plugin uninstall herdr-desk@herdr-desk`, then
`claude plugin install herdr-desk@herdr-desk`.

The plugin registers a `SessionStart` hook (`herdr-desk hook start --format claude-code`) and the `herdr-desk`
skill. `herdr-desk setup` also writes the skill to `~/.claude/skills/herdr-desk/SKILL.md` when you pass
`--skill-dir ~/.claude/skills`. It binds `prefix+t` (open the board) and `prefix+a` (capture) in
herdr's `config.toml`, only for keys that are free; `--force` replaces a binding that holds them. It
also writes the block that adds herdr-desk's row to herdr's sidebar (see [The sidebar row](#the-sidebar-row)).
It copies the file to `config.toml.herdr-desk-bak-<time>` first and never binds `ctrl+d`. The board's
popup is a second entry, the action `open-popup`, and has no default key.

**Trust each root once.** `claude` asks "Is this a project you created or one you trust?" the first time
it starts in a folder it has not trusted, and herdr shows that pane as `blocked` with no session.
herdr-desk never answers that question: trusting a folder is your choice. A worktree of a trusted repo is
trusted, so the cost is one `claude` start per root, once: start `claude` in each root you list (see
`herdr-desk roots`) and in the scratch root (`$XDG_DATA_HOME/herdr-desk/scratch`, where the coordinator
also runs), and answer the question there. Until you do, a run in a root `claude` does not trust sets its
task `blocked` with a note naming the pane.

Homebrew installs the binary alone: `brew install federbenjamin/tap/herdr-desk`.

### herdr with another agent

Install the plugin as above and run `herdr-desk setup`. The profile is what teaches herdr-desk to start your
agent, so set the `[agent]` values in `~/.config/herdr-desk/config.toml` yourself:

```toml
[agent]
worker = ["my-agent", "--model", "{model}", "--session-id", "{session}", "--", "{message}"]
coordinator = ["my-agent", "--session-id", "{session}", "--system-prompt", "{prompt}"]
session_env = "MY_AGENT_SESSION_ID"   # the variable your agent sets to its session id
models = ["small", "large"]           # the models a run may use; the worker's {model}
```

Templates are argv arrays; herdr-desk never passes task text through a shell. `{prompt}` is the
coordinator skill's text (`herdr-desk skill coordinator` prints it). `session_env` is how herdr-desk
tells an agent's commands from yours: a caller with a session id is an agent. Only the
`claude-code` profile is tested.

### No herdr

Install the binary (`brew install federbenjamin/tap/herdr-desk`, a release archive from GitHub Releases,
or `go install github.com/federbenjamin/herdr-desk/cmd/herdr-desk@latest`), then:

```sh
herdr-desk setup
herdr-desk add -t "first task"
herdr-desk
```

Every command works with no ticker running; only the timed jobs wait (see
[Running the ticker](#running-the-ticker)). Runs and the coordinator need herdr.

## Home and clients

The home owns the store. By default it is the machine you ran `herdr-desk setup` on. Every
`herdr-desk` command on it opens the SQLite file, does its work, and closes it. Write transactions begin
`IMMEDIATE`, so two commands at once wait on the busy timeout instead of failing. A config edit takes
effect on the next command, with no restart.

To use the home from a second machine, make sure `ssh <home>` works without a prompt and finds
`herdr-desk` on the home's PATH in a non-interactive shell. Then, on the client:

```sh
herdr-desk client add <ssh target>      # for example you@home-host, or a Host from ~/.ssh/config
herdr-desk list
```

`client add` sends a `status` request to the home and saves `[client] home` only when the home answers.
A client sends each request as one JSON object on the stdin of its `[client] command`, which runs
`herdr-desk rpc` on the home and carries one JSON response back, refusal codes included. The default is

```toml
[client]
home = "you@home-host"
command = ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-o", "ControlMaster=auto",
           "-o", "ControlPath={control}", "-o", "ControlPersist=60", "{home}", "herdr-desk", "rpc"]
```

`{home}` and `{control}` are filled in; each element is one argv word, never run through a shell.
`{control}` is the ssh control socket `<state folder>/ssh-<8 hex>`, the 8 hex the start of the home's
SHA-256, so each home gets its own. ssh adds 17 bytes to it while it binds the socket, and macOS allows a
socket path of 103 bytes, so the state folder (`$XDG_STATE_HOME/herdr-desk`) may be up to 73 bytes. With a
longer one, every request fails before ssh runs, naming the length; set `XDG_STATE_HOME` to a shorter folder,
or set a `command` without `{control}`. ssh keeps one connection open for 60 seconds, so a second call
within a minute does not pay for a new one. Set `command` to use `tailscale ssh`, a jump host, or any
program that carries stdin to `herdr-desk rpc` on the home and its stdout back.

Each request carries the wire version of the binary that sent it. A home of another version refuses the
request with an error naming both versions, and a client keeps what it queued until it reaches a home of its
own version: install the same herdr-desk on both machines. A request with a field the home does not know is
refused whole.

A client never opens a store, runs a ticker, or tracks runs: `herdr-desk ticker` and `herdr-desk coordinator`
there print where to run them and exit 0, `herdr-desk rpc` exits 2, and `herdr-desk hook herdr-event`
exits 0 before any transport.

The transport failing (ssh exits 255, or the 5-second connect timeout passes) is `home-unreachable`
(exit 3). With the home unreachable, a client:

- answers bare `herdr-desk` and `herdr-desk list` (with no flag, `--ready`, `--open`, `-p`, or `--desk`) from the
  snapshot of its last read, marked offline;
- refuses every other read and every task write with `home-unreachable` (exit 3);
- accepts `note`, `decide`, and the hook's records into a local outbox and forwards them on the next
  successful call (`queued` on stdout, and stderr says why the home did not take them). The entries keep
  their original time. Delivery is at least once. A queued entry the home refuses is dropped and named
  on stderr; one it cannot take yet (`scan-failed`, a server error) stays queued, and the command that
  tried to forward it fails naming the outbox file.

The offline notice on stderr gives the snapshot's time in UTC, like every time herdr-desk prints:
`herdr-desk list: the home did not answer; showing the snapshot from 2026-10-04 19:29Z`.

The board on a client runs on the client and sends each refresh through the same `[client] command`;
with the home unreachable it shows the snapshot.

## Commands

Task ids: `T12`, `t12`, and `12` name the same task. Every command that reads or changes tasks
takes `--json`. A refusal prints `herdr-desk <command>: <code>: <message>` on stderr and nothing on
stdout.

| exit | meaning |
|---|---|
| 0 | done, or already true |
| 1 | the item was refused; stderr starts with a stable code |
| 2 | usage error, or the refusal `bad-input` |
| 3 | store or home I/O (`home-unreachable`, `scan-failed`, any failure to reach or read the home) |

Refusal codes: `unknown-task`, `unknown-step`, `unknown-project`, `unknown-event`, `empty-title`,
`empty-text`, `secret-detected`, `not-allowed` (an agent set `ready` while `start_runs` is `propose`, killed
a run, paused the runner, or started a run without being the coordinator; or `run start` named an
archived or `done` task), `stale-run` (a newer run owns the task), `stale` (a notes save read notes that
have since changed: nothing is written), `no-run` (the task has no live run to kill, or `herdr-desk worker`'s
run is not running), `runner-off`, `runner-paused`, `cap-reached` (`run start` once today's runs reach
`runner.max_runs_per_day`), `no-herdr`, `run-failed` (`run start` whose run failed to start in the same call;
its reason follows), `backup-off`, `bad-input` (exit 2), `home-unreachable` (exit 3),
`scan-failed` (exit 3). `herdr-desk ticker status` is the one command that exits 1 with no code on
stderr: it exits 1 when the home does not answer, so a script can ask whether it does.

| command | does |
|---|---|
| `herdr-desk` | on a terminal (stdin and stdout both terminals, no `--json`), the board, which stays open until `q`; anywhere else, and with `--json`, a static board: `NEEDS YOU` (blocked, review), `IN MOTION` (started), `ON DECK` (ready, then open); first line `herdr-desk · home · runner on\|off`, or `herdr-desk · offline (snapshot <age>)` |
| `herdr-desk add -t <title> [-n <notes>] [-p <project>\|--desk] [--thread <name>] [--status <s>] [--tag <t>]… [--branch <b>]` | creates a task and prints `T<n>`. With no `-p` or `--desk` the project is the main checkout of the git repo you are in. `-p` takes an absolute directory or the bare name of a known project. When git cannot run (not on your PATH, a timeout), the add stops with exit 3 rather than store a task without its project |
| `herdr-desk list [--ready\|--open\|--done\|--archived\|--all] [-p <project>\|--desk]` | lists tasks; default is the five live statuses. `-p` and `--desk` narrow every filter, `--all` too; a bare name no task's project carries is `unknown-project`, as for `add`. Offline the name is looked up in the whole snapshot, which holds live tasks only, so a bare name none of them carries lists nothing; a relative path such as `a/b` is `unknown-project` online and offline |
| `herdr-desk show <task>` | one task with its steps and history |
| `herdr-desk set <task> [<status>] [--thread <t>] [--root <r>] [--isolation <i>] [--model <m>] [--archive\|--unarchive] [--ref <ref>] [--merged]` | patches fields; `review --merged` writes the status that `runner.on_merged` names. Setting `done` ends the task's live run, an `idle` one included |
| `herdr-desk edit <task> [--title <t>] [--notes <n>] [--append-notes <text>]` | `--notes` replaces the notes. `--append-notes` adds `<text>` on a new line at the end: it writes only if nobody changed the notes since it read them, and when someone did it reads again and retries once; a second `stale` exits 1. Empty or whitespace-only `<text>` is refused `empty-text` and writes nothing |
| `herdr-desk steps <task> add <text>` · `toggle <id>` · `rename <id> <text>` · `remove <id>` | step ids are `s1`, `s2`, … per task, never reused |
| `herdr-desk note <text> [--task <task>] [--ref <ref>] [--branch <b>] [--tag <t>]…` | appends a note and prints `e<id>`, or `queued` when the home is unreachable (stderr says why the home did not answer) |
| `herdr-desk note --merged --branch <b> [--pr <n>] [--sha <sha>] [<text>]` | records that a branch merged |
| `herdr-desk decide <text> [--tag <k:v>]… [--replaces e<id>] [--task <task>]` | appends a decision |
| `herdr-desk session [<id>] [--md] [--all] [--continues <old-id>]` | prints the session's journal view; `--json` prints it with the keys `session`, `work`, `todo`, `decisions`; `--all` shows hidden lines; `--continues` first links the session to an older one |
| `herdr-desk capture` | on a terminal (stdin and stdout both terminals, no `--json`), the capture popup: one line (words starting `#` set the thread, `@` the project, the rest is the title); Enter adds the task and prints `T<n>`; a refused line shows its error under the line and stays there; an empty line, `esc`, or `ctrl+c` exits 0 with no task. While the home has not answered an `enter`, keys wait, and `esc` ends the popup once it answers. Anywhere else it reads lines on stdin: with stdin a terminal it prompts `capture: ` on stderr, and after a refusal prints the error and asks again; with stdin not a terminal it reads one line and exits with the code of its refusal, as every command does. An empty line ends it with exit 0 |
| `herdr-desk run start <task> [--root <r>] [--isolation <i>] [--model <m>] [--json]` | the one way to start a run; prints `run <id>  T<n>  <state>  <root>  <isolation>  <model>`. When the run fails to start in this call it prints the `failed` run, then `run-failed: run <id> failed: <reason>` on stderr, and exits 1. [The runner](#the-runner) says what it asks and refuses |
| `herdr-desk runs [--all] [--json]` | checks the live runs against herdr once, then lists them, oldest first: `run <id>  T<n>  <state>  <root>  <isolation>  <model>  <elapsed>` (`-` for a field not decided yet); `no live runs` when none. `--all` lists every run; `--json` prints the array |
| `herdr-desk runs kill <task>` | kills the processes in the task's pane, closes the pane, ends the run `killed`, and blocks the task; prints `T<n> blocked`. `no-run` when the task has no live run; an agent gets `not-allowed`. When the pane did not close, a process outlived the kill, or herdr could not say what ran in the pane (so nothing was signalled), the task is still blocked, the note on it says what is left, and the command exits 3; the ticker closes that pane again on its next tick |
| `herdr-desk runner [status]` · `pause` · `resume` | prints `runner <state>`, and ` · <live>/<cap> live` when the state is `on` or `paused`, then ` · no ticker: …` when no ticker runs (the board's header ends `· no ticker` too, and `context` prints the same line), since nothing then stops a run at `max_run_minutes`. `pause` starts no new runs, live runs go on, and the pause survives a restart; an agent gets `not-allowed` |
| `herdr-desk coordinator` | opens the desk's coordinator in its own herdr workspace, or focuses it when its pane is still open; prints `coordinator opened\|focused: workspace <id>, pane <id>`. The first call opens the workspace without focusing it. On a client it prints where to run it and exits 0; an agent session gets `not-allowed`; no herdr is `no-herdr` |
| `herdr-desk coordinator run` | what `coordinator` types in the pane: becomes the `[agent] coordinator` command with `{session}` and `{prompt}` filled in. Exit 2 when `$DESK_SESSION` is unset |
| `herdr-desk context [--json]` | prints the desk as the coordinator reads it: `start_runs`, the runner's state, the caps and today's count, the roots with their `about`, the models, the board by section, the live runs with their state, and what changed since this coordinator's last `context`. It checks the runs against herdr once |
| `herdr-desk skill coordinator` | prints the coordinator skill, which is the coordinator's system prompt |
| `herdr-desk worker` | what the runner types in the pane: loads the run in `$DESK_RUN` and becomes the `[agent] worker` command. Exit 2 when `$DESK_RUN` or `$DESK_TASK` is unset or malformed, 1 `no-run` when the run is not running for that task and session, 3 when the worker cannot be started (the task is then blocked with a note) |
| `herdr-desk ticker [run]` · `status` · `stop` | `run` (the default) is the ticker, one process that does the timed jobs once a minute; a second one prints `already running` and exits 0; on a client it prints that the ticker runs on the home and exits 0. `status` prints the home's status as JSON (`ticker` with `running`, `pid`, and `started_ts`; `runner_state`; the task counts per status; `backup_ts`, the last successful backup, `null` when none; `backup_error`, the error of a failed attempt since) and exits 1 when the home does not answer. `stop` stops it; no ticker is not an error |
| `herdr-desk rpc` | the home's end of a client's transport: reads one JSON request on stdin, runs it against the store, prints one JSON response, and exits 0 whenever it wrote one. Exit 2 on a client |
| `herdr-desk client add <ssh target>` | makes this machine a client of the home; saves `[client] home` once the home answers a `status` request |
| `herdr-desk roots [list]` · `add <path> [--about <a>] [--isolation <i>]` · `remove <path>` | edits `[[roots]]`. `add` takes an existing directory (anything else is a usage error, exit 2); on a path already listed it changes only the fields whose flags you pass, and `--about ""` clears one |
| `herdr-desk setup [--profile claude-code] [--runner on\|off] [--skill-dir <dir>] [--force] [--no-herdr]` | first-time setup; never prompts |
| `herdr-desk hook start --format claude-code` | the session hook: reads the hook's JSON on stdin, writes the session's journal view to `<state>/sessions/<id>.md`, and prints the path. After that, every `note`, `decide`, `add`, `set`, `edit`, `capture`, and `session --continues` that succeeds for that session rewrites the file, on the home and on a client alike (a write that was queued does not, until the next one). With the home unreachable the hook prints one line saying the journal is not loaded and exits 0 |
| `herdr-desk hook herdr-event` | herdr runs it on pane events (see [Tracking](#tracking)); it reads the pane id from the event and exits 0 on every path, writing errors to `<state>/herdr-desk.log`: an event with no pane id, a config that does not load, and a store of another schema version (a binary swap) are logged too |
| `herdr-desk backup` | runs the backup now; a failed run exits 3 naming the git step that failed, with the remote shown as `<remote>` |
| `herdr-desk version` | prints the version. A build made from source by the herdr plugin's install step carries the plugin manifest's version with `+src`, for example `0.1.0+src` |

The session is the first of `--session <id>`, `$DESK_SESSION`, and the variable named by
`[agent] session_env`. `DESK_HOOKS=off` makes `herdr-desk hook` do nothing.

A write whose text holds a secret (private keys, AWS, GitHub, Anthropic, and Slack tokens) is
refused with `secret-detected` and the pattern's name, never the match.

## The board

Bare `herdr-desk` on a terminal is the board, in a herdr pane, a herdr popup, or any terminal. The
split pane (the action `open-board`, `prefix+t`) and the popup (the action `open-popup`) show the same
board, each laid out to the size herdr gives it; under 10 rows it draws one line saying how many it
needs. It refreshes every 3 seconds and after each write, writes as you (never as an agent), and uses
only the terminal's 16 ANSI colours. `ctrl+d` is bound to nothing. `ctrl+c` and `q` quit.

The header is `herdr-desk  <project> ▾  thread: <thread> ▾` on the left and the runner on the right:
`runner ● on · 2/3 · home` (live runs and the cap), `runner ◐ paused`, `runner ○ off · home`, and
`client` in place of `home` on a client machine. With the home unreachable it reads
`offline (snapshot 12m)`: the board shows the last snapshot, and every key that writes refuses with
`offline: <key> needs the home`. A refusal or error from the home shows on the status line until the next key.

Below the header are the three sections, each with its title even when empty. `NEEDS YOU` lists
blocked and review tasks, `IN MOTION` started ones (and the last note under the row), `ON DECK` the ready
ones, then `inbox` and the open ones. `d` adds a `DONE` section. A row whose task has a live run, in any
section, shows the run: its state, root, isolation, model, and elapsed time
(`running · alpha · worktree · gpt · 1h`); a row without one shows its project and how long ago it changed.

Width decides the layout: under 78 columns one surface at a time and rows without their right-hand
detail; from 78 to 109 one surface with full rows; from 110 the board on the left and the selected
task's page on the right.

Board page keys:

| key | does |
|---|---|
| `↓` `j` · `↑` · `g` · `G` | next row · previous row · first · last (`k` is kill, not up) |
| `enter` | open the task's page |
| `+` | add a task: one line, `#thread` and `@project` as in `herdr-desk capture`; a refused line stays in the box with its error. Until the home answers an `enter`, the line takes no keys, and `esc` closes the box once it answers |
| `n` | set `ready`; on a blocked task it asks `answer:`, appends your answer as a note, then sets `ready` (an empty answer sets `ready` alone); when the note cannot be written the prompt opens again with your answer |
| `S` | start a run of the task, as `herdr-desk run start` with no flags does; a refusal shows on the status line |
| `s` · `b` · `r` | set `started` · `blocked` · `review` |
| `x` | set `done`; asks `y/n` unless the task is in `review` |
| `a` | toggle the thread `agent` |
| `f` | focus the run's herdr pane (the home, with herdr, a live run with a pane) |
| `k` | kill the task's live run after `y/n`; the home sets the task `blocked` |
| `P` | pause or resume the runner |
| `/` | search titles and ids as you type; `enter` keeps the filter, `esc` clears it |
| `p` · `t` | next project filter · next thread filter, then back to all |
| `d` | open or close the done drawer: the 20 most recently updated done tasks |
| `?` | the keys overlay |
| `esc` | close the overlay, else the drawer, else clear the search |
| `q` · `ctrl+c` | quit |

`P` with no runner state reported follows the header: `on` pauses, anything else says the runner is off.
A paste goes to the open text input (a prompt, the add box, the notes editor).

Task page keys (the board's `n S s b r x a f k P ? q ctrl+c` work here too, for this task):

| key | does |
|---|---|
| `↓` `j` · `↑` | scroll |
| `esc` | back to the board |
| `e` | edit the notes in an editor; `ctrl+s` saves, `esc` discards. A save that fails opens the editor again with your text. When someone else changed the notes while you edited, the save is refused `stale`: your text stays, the status line says the notes changed, and a second `ctrl+s` replaces them. When the board cannot read the home's notes at that moment, the status line says so and a second `ctrl+s` tries the save again |
| `t` | steps mode: `↓` `j` `↑` move, `space` or `enter` toggles, `a` adds, `r` renames, `x` removes, `esc` leaves |
| `R` · `M` | edit the root · the model (an empty line clears it) |
| `I` | next isolation: none, `self`, `worktree`, `in-place` |
| `o` | open a ref: with several, a pick list; with none, the status line says so |

The page shows the head line, `root · isolation · model`, `NOTES`, `STEPS`, `HISTORY across <n> sessions`
(run starts, notes with their refs, decisions, hand-backs, `archived` and `unarchived`), and
`FILES`, the refs of the history. `o` opens an `http` or `https` URL with the OS opener, and a file
(an absolute path, or one relative to the task's project) in the herdr plugin `herdr-file-viewer`
when herdr lists it; with no viewer the status line says so. A ref reaches a command only as one
argument, never through a shell.

## Config

`$XDG_CONFIG_HOME/herdr-desk/config.toml` (default `~/.config/herdr-desk/config.toml`), mode 0600:

```toml
[client]          # present on clients only
home = ""         # the ssh target of the home
command = []      # argv template; empty = the default in "Home and clients"

[runner]
enabled = false
cap = 1                # each of cap, max_runs_per_day, max_run_minutes is at least 1
max_runs_per_day = 20
max_run_minutes = 180
on_merged = "review"   # review | done

[coordinator]
# WARNING: auto lets the coordinator and any agent start runs that spend your quota unasked
start_runs = "propose" # propose | auto

[[roots]]
path = "~/code/example"
about = "the app; runs its own build pipeline"
isolation = "self"     # self | worktree | in-place; unset = worktree in a git work tree, else in-place

[agent]           # written by a profile; see the install section
worker = []
coordinator = []
session_env = ""
models = []       # the models a run may use; the first is the default; the worker template's {model}

[notify]
command = []      # argv; {title} and {body} are filled in

[secret_scan]
command = []      # e.g. ["gitleaks", "stdin"]; exit 0 clean, 1 a secret, anything else refuses the write

[backup]
git_remote = ""   # set to back up events.jsonl nightly to this git remote
```

The config is read afresh by every command and by each tick of the ticker, so an edit takes effect
on the next one. A config that does not load (an unknown key, a bad value) makes the command fail naming
the key.

**Upgrading.** The key and command names below were removed and the config no longer loads while it
holds one of the keys, so delete them. `herdr-desk setup` rewrites the rest.

- `[home] listen`, the token file, and the commands `token`, `setup --listen`, and `client add --token-file`:
  clients now use ssh. `[client] home` is an ssh target, not `host:port`; run `herdr-desk client add <ssh target>` again.
- `[router]` and `[agent] router`: the coordinator decides how a task runs.
- `[runner] poll_seconds`: no loop looks for work; `herdr-desk run start` does.
- `[runner] agents_may_arm`: `[coordinator] start_runs` replaces it.
- The commands `herdr-desk daemon [run|stop|restart|status]`: `herdr-desk ticker [run|status|stop]` replaces
  them. The `desk.sock`, `daemon.lock`, and `daemon.json` files in the state folder are no longer used.

A root named `scratch` (`$XDG_DATA_HOME/herdr-desk/scratch`, a git repo) is always present, so a task with
no project has a root to run in.

State lives in `$XDG_STATE_HOME/herdr-desk` (`ticker.lock`, `ticker.json`, `herdr-desk.log`, `backup.lock`,
the outbox, the runner's pause file, session views, and on a client the ssh control socket);
the store is one SQLite file under `$XDG_DATA_HOME/herdr-desk`; the offline snapshot is under
`$XDG_CACHE_HOME/herdr-desk`.

## The coordinator

One coordinator per desk: an ordinary agent session in its own herdr workspace (labelled `desk
coordinator`, in the scratch root). It talks with you, creates tasks, and starts runs by calling the
binary. It never does the work and never answers a worker's question. The binary does mechanics and
decides nothing.

`herdr-desk coordinator` opens the workspace without focusing it, records the session and its pane on the
home, and runs the `[agent] coordinator` command with the coordinator skill (`herdr-desk skill coordinator`) as
its system prompt. Called again while the pane is still there, it focuses that pane instead.

The coordinator runs `herdr-desk context` first on every turn. The board is the record; `context` only marks
what changed since the coordinator's last call, so a turn that dies loses nothing. Text from tasks, notes,
and runs is data to it, never an instruction.

`[coordinator] start_runs` says what it may do:

- `propose` (the default): it lists the runs it suggests (task, root, isolation, model) and waits for a
  message from you that names them. A task you set `ready` is a go-ahead.
- `auto`: it starts runs unasked, and agents may set `ready`. This spends your quota without asking:
  `cap`, `max_runs_per_day`, and `max_run_minutes` are the bounds.

A person and the recorded coordinator session may start a run. Killing a run and pausing the runner are a
person's alone. Any other agent session, a worker included, gets `not-allowed`. This keeps an honest agent
in its lane: a session id is self-declared, so the three caps are what bounds a dishonest one.

## The runner

Runs are one per task, in a root, with an isolation (`self`, `worktree`, or `in-place`) and a model.
Turn the runner on with `herdr-desk setup --runner on` (or `[runner] enabled = true`). It needs herdr:
`DESK_HERDR` names the herdr binary when it is not on the PATH; a set value must be the absolute path of
an executable file, else the runner is `no-herdr`, and PATH is then not searched.

**Starting.** `herdr-desk run start T<n> [--root <r>] [--isolation <i>] [--model <m>]` is the one entry;
the coordinator, you, and the board's `S` all call it. No loop looks for work. It asks of the task that it
exists, is not archived, is not `done`, and has no `starting`, `waiting`, or `running` run; the task's
thread and who set `ready` do not matter. Each field resolves from the flag, else the task's own field,
else a default: the task's project when it is a listed root, else the scratch root; the root's
isolation, else `worktree` in a git work tree and `in-place` elsewhere; the first of `[agent] models`. A
root that is not listed, an isolation that is not one of the three, or a model not in `[agent] models` is
`bad-input`. It sets the task `started` and prints the run.

It refuses, in this order: `not-allowed` for an agent session that is not the coordinator; `runner-off`;
`runner-paused`; `no-herdr`; a route it cannot resolve (`bad-input`); an archived or `done` task
(`not-allowed`); `cap-reached` once today's runs reach `max_runs_per_day`. Today's count and the new run are
written in one transaction, so starts at once from many processes never pass the cap. A task that already
has a `starting`, `waiting`, or `running` run prints that run and exits 0. A task whose run is `idle`
ends that run, closes its pane, and starts a new one; while that pane may still be open (herdr would not close it)
the new run fails, `run-failed`, and the ticker closes the pane again. Setting the task `done` ends an idle run too. An agent that sets
`ready` is refused unless `start_runs` is `auto`.

**States.** A run is `starting`, `waiting`, `running`, or `idle` while live, and `ended`, `failed`, or
`killed` after. `waiting` means the run cannot start now: its `in-place` root is busy (one `in-place`
run per root at a time, an `idle` one included) or `cap` runs are live; it starts by itself when one
frees, within seconds of the run that frees it ending, and the ticker tries too. `idle` means the agent
stopped without handing back; it holds its in-place root and does not count toward `cap`, so an idle agent
that resumes can put the desk one over `cap` until a run ends. A run left `starting` for over a minute is
failed. `herdr-desk runner` shows one of `off` (`runner.enabled` is false), `paused`, `no-herdr`, and `on`.
The state gates only starting runs, so a run that was live when the runner was switched off is still
handed back, checked by the ticker, and stopped at `max_run_minutes`. `runner pause`, `resume`, and `runs kill`
are a person's acts.

**The spawn.** `worktree` isolation uses `<parent of root>/<root>-T<n>` on the branch
`desk/T<n>-<slug>` (made on the first run, reused after). The runner creates a herdr workspace
labelled `desk T<n>` without taking focus, with `DESK_TASK`, `DESK_SESSION`, `DESK_RUN`, and the
four XDG variables of the home's folders, so the `herdr-desk` in the pane uses the same store. The pane runs
`exec <the herdr-desk binary> worker`. `herdr-desk worker` builds the worker's first message from the task
(title, notes, steps, history, and how to hand back) and replaces itself with the `[agent] worker` command:
`{model}` is the run's model, `{session}` its session id, `{message}` the first message. No task text ever
reaches a shell. A note records the workspace and pane, and `[notify] command` runs once per spawn. A worker
finishes with `review` or `blocked` (`herdr-desk set T<n> review` or `blocked`), which ends its run. Every
write it makes carries its run id, and a write from a run that is not the task's newest is refused with
`stale-run`.

### Tracking

herdr's events track runs. The plugin manifest runs `herdr-desk hook herdr-event` on `pane.agent_status_changed`,
`pane.closed` (a pane closed), and `pane.exited` (a pane whose process ended by itself, as a worker's does). The event is only a signal: the hook takes the pane id from it, looks for a live run on
that pane, and when there is one asks herdr for the pane's state now. A pane no run owns, a client machine,
and `DESK_HOOKS=off` exit 0 and write nothing. What the hook does with the pane's state:

| the pane now | the run | the task |
|---|---|---|
| gone | `ended` | `review`, unless the worker already handed back |
| `blocked`, and the session is the run's or the pane has none yet | `idle` | `blocked`, the note names the pane |
| `idle` or `done`, the session is the run's, the worker has not handed back | `idle` | `review`, with the note "went idle without handing back" |
| `working`, and the run is `idle` | `running` | `started` |

A repeated event changes nothing. A worker that stops at a question before it has an agent session, such
as the `claude` trust question above, shows as `blocked` with no session: the task stays `blocked` until a
person answers in the pane, and the worker, still the task's newest run, can then hand it back.

### The ticker's run jobs

Once a minute, and only while a run is live (with none and no pane left open, herdr is not asked), the
ticker:

1. checks every `running` and `idle` run against one `herdr pane list`, with the same table, for an event
   herdr never delivered;
2. stops a `running` run past `max_run_minutes`, counted from its spawn and not from a wait before it, as
   `herdr-desk runs kill` does, and blocks the task;
3. starts the oldest `waiting` run whose root is free and whose slot is open, and fails a run left
   `starting` for over a minute;
4. closes again a pane a kill could not close, and blocks a task left `started` after its newest run ended.

`herdr-desk runs` and `herdr-desk context` check the runs once when called; the board's refresh does not.
When the check cannot run (no herdr on the home, herdr not answering), they list the runs as the store has
them and say why the check did not run: `runs` on stderr, `context` under `live runs:`.

### The sidebar row

herdr-desk reports one token, `$desk`, for each run's pane and for the coordinator's pane, and one new row
in herdr's sidebar shows it; cards of panes that are not herdr-desk's keep their rows. A run's pane reads
the task id, then the state, then one detail, at most 28 characters (the detail is cut first):
`T21 running · since 14:05`, `T22 review · PR #12`, `T23 needs you · blocked`, `T24 review · went idle`,
`T22 done`. The coordinator's pane reads `2 need you · 2 running`, `2 running`, `2 running · 1 waiting`,
or `idle`. The text goes out when a run starts, on herdr's events, on a hand-back, and on a kill, never on
a timer, so a row shows when a run started, not how long it has run, and the card goes with its pane.

`herdr-desk setup` writes the herdr config block that shows the token, in a fenced block, backing the file
up first as it does for keys. When your herdr config already has its own `[ui.sidebar.agents]` table, setup
leaves it alone and prints the row to add by hand. A herdr config that does not parse as TOML is left alone too,
and setup says so.

## Running the ticker

herdr starts the ticker (`[[startup]]` in `herdr-plugin.toml`). It is one process per home, holds
`ticker.lock` so a second one exits 0, and once a minute does the timed jobs: the backup when one is due
(checked hourly), then the run jobs above, whatever `runner.enabled` says: switching the runner off stops new
runs, not the limits on live ones. A command on a home with no ticker still works; only the timed jobs wait.
With no herdr, run it yourself with `herdr-desk ticker &` or a service unit. Its errors, and every line the
runner logs in any process on the home (the ticker, a command, `herdr-desk rpc`, the event hook), go to
`<state>/herdr-desk.log`.

launchd, `~/Library/LaunchAgents/herdr-desk.ticker.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>herdr-desk.ticker</string>
  <key>ProgramArguments</key>
  <array><string>/usr/local/bin/herdr-desk</string><string>ticker</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
```

```sh
launchctl load ~/Library/LaunchAgents/herdr-desk.ticker.plist
```

systemd, `~/.config/systemd/user/herdr-desk.service`:

```ini
[Service]
ExecStart=%h/.local/bin/herdr-desk ticker
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user enable --now herdr-desk.service
```

## Releases

`goreleaser` builds darwin and linux for arm64 and amd64 and publishes the archives
`herdr-desk_<version>_<os>_<arch>.tar.gz` with `checksums.txt` and the Homebrew cask. A `v*` tag runs
it in CI. Locally: `go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish`.

## Develop

```sh
sh scripts/checks.sh               # vet, the coverage gate, gofmt, shellcheck; --no-coverage runs go test instead
bash scripts/e2e/h01-add-list.sh   # the hand-test claims, one script each
```

MIT licensed; see `LICENSE`.
