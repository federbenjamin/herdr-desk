# desk

A task board for you and your agents, with a runner that turns a task you arm into a working agent
in a herdr workspace, and a journal that gives every agent session a memory.

Status: pre-release, under construction. This version has the store, the daemon, the CLI, the
journal, the board, and the packaging. The runner (`desk worker`) comes later.

## What it is

- **Tasks.** `desk add`, `list`, `show`, `set`, `edit`, `steps`. Statuses are `open`, `ready`,
  `started`, `blocked`, `review`, and `done`. A task runs only when you set `ready`; agents may
  propose tasks and set `review` or `blocked`, never `ready` or `done`.
- **A journal.** `desk note` and `desk decide` record facts from a session. `desk session <id> --md`
  renders the session's Work log, Todo, and Decisions, hiding what a merge or a compaction made
  stale. A Claude Code hook prints the view's path at the start of every session.
- **One home per desk.** One machine runs the daemon and owns the SQLite store. Every other machine
  is a client.

## Install

desk is one static binary, `desk`. Pick the line that fits.

### herdr with Claude Code

```sh
herdr plugin install federbenjamin/desk
```

herdr runs `scripts/fetch-or-build.sh`: it downloads the release binary for your platform, checks
its SHA-256 against `checksums.txt`, and builds from source with Go when no release matches. When no
`desk` is on your PATH it copies the binary to `~/.local/bin/desk` and says so. A later install
replaces that copy and tells you to run `desk daemon restart`; a `desk` from anywhere else
(Homebrew, `go install`) is left alone.

Then set up the home and the Claude Code plugin:

```sh
desk setup --profile claude-code
```

In Claude Code:

```
/plugin marketplace add federbenjamin/desk
/plugin install desk@desk
```

The plugin registers a `SessionStart` hook (`desk hook start --format claude-code`) and the `desk`
skill. `desk setup` also writes the skill to `~/.claude/skills/desk/SKILL.md` when you pass
`--skill-dir ~/.claude/skills`. It binds `prefix+t` (open the board) and `prefix+a` (capture) in
herdr's `config.toml`, only for keys that are free; `--force` replaces a binding that holds them. It
copies the file to `config.toml.desk-bak-<time>` first and never binds `ctrl+d`.

Homebrew installs the binary alone: `brew install federbenjamin/tap/desk`.

### herdr with another agent

Install the plugin as above and run `desk setup`. The profile is what teaches desk to start your
agent, so set the three `[agent]` values in `~/.config/desk/config.toml` yourself:

```toml
[agent]
router = ["my-agent", "--print", "--system-prompt-file", "{system}"]   # reads the task on stdin, prints JSON
worker = ["my-agent", "--model", "{model}", "--session-id", "{session}", "--", "{message}"]
session_env = "MY_AGENT_SESSION_ID"   # the variable your agent sets to its session id
```

Templates are argv arrays; desk never passes task text through a shell. `session_env` is how desk
tells an agent's commands from yours: a caller with a session id is an agent. Only the
`claude-code` profile is tested.

### No herdr

Install the binary (`brew install federbenjamin/tap/desk`, a release archive from GitHub Releases,
or `go install github.com/federbenjamin/desk/cmd/desk@latest`), then:

```sh
desk setup
desk daemon &        # or run it under launchd or systemd, below
desk add -t "first task"
desk
```

Commands start the daemon themselves when it is not running, so the last two steps are optional.

## Home and clients

The home runs the daemon, owns the store, and is the only writer. By default it is the machine you
ran `desk setup` on. Its daemon serves a JSON API on a unix socket (`desk.sock` in the state
folder, mode 0600) and, when `[home] listen` is set, on that address with a bearer token.

To use the home from a second machine:

```sh
# on the home
desk setup --listen 127.0.0.1:7411     # use an address on a private network, such as a tailnet
desk token > token.txt                 # move this file to the client over a channel you trust

# on the client
desk client add 127.0.0.1:7411 --token-file token.txt
desk list
```

`desk token rotate` mints a new token and the old one stops working. A wildcard `listen` host
(empty, `0.0.0.0`, `::`) is refused. The listener has no TLS: bind a private network that is
already encrypted.

With the home unreachable, a client:

- answers bare `desk` and `desk list` (with no flag, `--ready`, `--open`, `-p`, or `--desk`) from the
  snapshot of its last read, marked offline;
- refuses every other read and every task write with `home-unreachable` (exit 3);
- accepts `note`, `decide`, and the hook's records into a local outbox and forwards them on the next
  successful call (`queued` on stdout, and stderr says why the home did not take them: it did not
  answer, or it refused the token with `bad-token`). Delivery is at least once. A queued entry the
  home refuses is dropped and named on stderr; one it cannot take yet (`scan-failed`, a server
  error) stays queued, and the command that tried to forward it fails naming the outbox file. A
  forward the home answers with `bad-token` keeps every entry queued: the token is at fault, not
  the entry. Run `desk client add` with the home's current token and the next call sends them.

The offline notice on stderr gives the snapshot's time in UTC, like every time desk prints:
`desk list: the home did not answer; showing the snapshot from 2026-10-04 19:29Z`.

## Commands

Task ids: `T12`, `t12`, and `12` name the same task. Every command that reads or changes tasks
takes `--json`. A refusal prints `desk <command>: <code>: <message>` on stderr and nothing on
stdout.

| exit | meaning |
|---|---|
| 0 | done, or already true |
| 1 | the item was refused; stderr starts with a stable code |
| 2 | usage error |
| 3 | store or home I/O (`home-unreachable`, `bad-token`, `scan-failed`, any failure to reach or read the home) |

Refusal codes: `unknown-task`, `unknown-step`, `unknown-project`, `unknown-event`, `empty-title`,
`secret-detected`, `not-allowed` (an agent set `ready` or `done`), `backup-off`, `bad-token` (exit 3:
the home refused this client's token, HTTP 401; `desk client add` with the current token fixes it).
`desk daemon status` is the one command that exits 1 with no code on stderr: it exits 1 when no
daemon answers, so a script can ask whether one runs.

| command | does |
|---|---|
| `desk` | on a terminal (stdin and stdout both terminals, no `--json`), the board, which stays open until `q`; anywhere else, and with `--json`, a static board: `NEEDS YOU` (blocked, review), `IN MOTION` (started), `ON DECK` (ready, then open); first line `desk · home · runner on\|off`, or `desk · offline (snapshot <age>)` |
| `desk add -t <title> [-n <notes>] [-p <project>\|--desk] [--thread <name>] [--status <s>] [--tag <t>]… [--branch <b>]` | creates a task and prints `T<n>`. With no `-p` or `--desk` the project is the main checkout of the git repo you are in. `-p` takes an absolute directory or the bare name of a known project. When git cannot run (not on your PATH, a timeout), the add stops with exit 3 rather than store a task without its project |
| `desk list [--ready\|--open\|--done\|--archived\|--all] [-p <project>\|--desk]` | lists tasks; default is the five live statuses. `-p` and `--desk` narrow every filter, `--all` too; a bare name no task's project carries is `unknown-project`, as for `add`. Offline the name is looked up in the whole snapshot, which holds live tasks only, so a bare name none of them carries lists nothing; a relative path such as `a/b` is `unknown-project` online and offline |
| `desk show <task>` | one task with its steps and history |
| `desk set <task> [<status>] [--thread <t>] [--root <r>] [--isolation <i>] [--model <m>] [--archive\|--unarchive] [--ref <ref>] [--merged]` | patches fields; `review --merged` writes the status that `runner.on_merged` names |
| `desk edit <task> [--title <t>] [--notes <n>]` | replaces the title or the notes |
| `desk steps <task> add <text>` · `toggle <id>` · `rename <id> <text>` · `remove <id>` | step ids are `s1`, `s2`, … per task, never reused |
| `desk note <text> [--task <task>] [--ref <ref>] [--branch <b>] [--tag <t>]…` | appends a note and prints `e<id>`, or `queued` when the home is unreachable (stderr says why the home did not answer) |
| `desk note --merged --branch <b> [--pr <n>] [--sha <sha>] [<text>]` | records that a branch merged |
| `desk decide <text> [--tag <k:v>]… [--replaces e<id>] [--task <task>]` | appends a decision |
| `desk session [<id>] [--md] [--all] [--continues <old-id>]` | prints the session's journal view; `--json` prints it with the keys `session`, `work`, `todo`, `decisions`; `--all` shows hidden lines; `--continues` first links the session to an older one |
| `desk capture` | on a terminal (stdin and stdout both terminals, no `--json`), the capture popup: one line (words starting `#` set the thread, `@` the project, the rest is the title); Enter adds the task and prints `T<n>`; a refused line shows its error under the line and stays there; an empty line, `esc`, or `ctrl+c` exits 0 with no task. While the home has not answered an `enter`, keys wait, and `esc` ends the popup once it answers. Anywhere else it reads lines on stdin: with stdin a terminal it prompts `capture: ` on stderr, and after a refusal prints the error and asks again; with stdin not a terminal it reads one line and exits with the code of its refusal, as every command does. An empty line ends it with exit 0 |
| `desk daemon [run]` · `stop` · `restart` · `status` | runs or controls the daemon. A second `run` prints `already running` and exits 0; on a client it prints that there is nothing to run and exits 0. `status` never starts a daemon and exits 1 when none answers; its JSON carries `backup_ts`, the last successful backup (`null` when none), `backup_error`, the error of a failed attempt since, so a failing nightly backup shows there, and `config_changed`, true when the config file now holds a different config from the one the daemon started with |
| `desk token [show]` · `rotate` | prints or rotates the token |
| `desk client add <host:port> [--token-file <path>]` | joins a home; the token comes from the file or stdin, never an argument |
| `desk roots [list]` · `add <path> [--about <a>] [--isolation <i>]` · `remove <path>` | edits `[[roots]]`. `add` takes an existing directory (anything else is a usage error, exit 2); on a path already listed it changes only the fields whose flags you pass, and `--about ""` clears one |
| `desk setup [--profile claude-code] [--listen <host:port>] [--runner on\|off] [--skill-dir <dir>] [--force] [--no-herdr]` | first-time setup; never prompts |
| `desk hook start --format claude-code` | the session hook: reads the hook's JSON on stdin, writes the session's journal view to `<state>/sessions/<id>.md`, and prints the path. After that, every `note`, `decide`, `add`, `set`, `edit`, `capture`, and `session --continues` that succeeds for that session rewrites the file, on the home and on a client alike (a write that was queued does not, until the next one). With the home unreachable or refusing the token the hook prints one line saying the journal is not loaded and exits 0 |
| `desk backup` | runs the backup now; a failed run exits 3 naming the git step that failed, with the remote shown as `<remote>` |
| `desk version` | prints the version. A build made from source by the herdr plugin's install step carries the plugin manifest's version with `+src`, for example `0.1.0+src` |

The session is the first of `--session <id>`, `$DESK_SESSION`, and the variable named by
`[agent] session_env`. `DESK_HOOKS=off` makes `desk hook` do nothing.

A write whose text holds a secret (private keys, AWS, GitHub, Anthropic, and Slack tokens) is
refused with `secret-detected` and the pattern's name, never the match.

## The board

Bare `desk` on a terminal is the board, in a herdr pane or any terminal. It refreshes every 3 seconds
and after each write, writes as you (never as an agent), and uses only the terminal's 16 ANSI colours.
`ctrl+d` is bound to nothing. `ctrl+c` and `q` quit.

The header is `desk  <project> ▾  thread: <thread> ▾` on the left and the runner on the right:
`runner ● on · 2/3 · home` (live runs and the cap), `runner ◐ paused`, `runner ○ off · home`, and
`client` in place of `home` on a client machine. With the home unreachable it reads
`offline (snapshot 12m)`: the board shows the last snapshot, and every key that writes refuses with
`offline: <key> needs the home`. A refusal or error from the home shows on the status line until the next key.

Below the header are the three sections, each with its title even when empty. `NEEDS YOU` lists
blocked and review tasks, `IN MOTION` started ones (root, isolation, model, and elapsed time of the
live run, and the last note under the row), `ON DECK` the ready ones, then `inbox` and the open ones.
A ready task with the thread `agent` shows `#agent · queued`. `d` adds a `DONE` section.

Width decides the layout: under 78 columns one surface at a time and rows without their right-hand
detail; from 78 to 109 one surface with full rows; from 110 the board on the left and the selected
task's page on the right.

Board page keys:

| key | does |
|---|---|
| `↓` `j` · `↑` · `g` · `G` | next row · previous row · first · last (`k` is kill, not up) |
| `enter` | open the task's page |
| `+` | add a task: one line, `#thread` and `@project` as in `desk capture`; a refused line stays in the box with its error. Until the home answers an `enter`, the line takes no keys, and `esc` closes the box once it answers |
| `n` | set `ready`; on a blocked task it asks `answer:`, appends your answer as a note, then sets `ready` (an empty answer sets `ready` alone); when the note cannot be written the prompt opens again with your answer |
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

`P` and `k` call the runner's endpoints; a home that does not serve them answers on the status line.
`P` with no runner state reported follows the header: `on` pauses, anything else says the runner is off.
A paste goes to the open text input (a prompt, the add box, the notes editor).

Task page keys (the board's `n s b r x a f k P ? q ctrl+c` work here too, for this task):

| key | does |
|---|---|
| `↓` `j` · `↑` | scroll |
| `esc` | back to the board |
| `e` | edit the notes in an editor; `ctrl+s` saves, `esc` discards. A save that fails opens the editor again with your text; when the notes changed while you edited, the first `ctrl+s` says so and a second replaces them |
| `t` | steps mode: `↓` `j` `↑` move, `space` or `enter` toggles, `a` adds, `r` renames, `x` removes, `esc` leaves |
| `R` · `M` | edit the root · the model (an empty line clears it) |
| `I` | next isolation: none, `self`, `worktree`, `in-place` |
| `o` | open a ref: with several, a pick list; with none, the status line says so |

The page shows the head line, `root · isolation · model`, `NOTES`, `STEPS`, `HISTORY across <n> sessions`
(claim and routing, notes with their refs, decisions, hand-backs, `archived` and `unarchived`), and
`FILES`, the refs of the history. `o` opens an `http` or `https` URL with the OS opener, and a file
(an absolute path, or one relative to the task's project) in the herdr plugin `herdr-file-viewer`
when herdr lists it; with no viewer the status line says so. A ref reaches a command only as one
argument, never through a shell.

## Config

`$XDG_CONFIG_HOME/desk/config.toml` (default `~/.config/desk/config.toml`), mode 0600:

```toml
[home]            # present on the home only
listen = ""       # an address on a private network, e.g. "127.0.0.1:7411"; empty = local only

[client]          # present on clients only
home = ""         # host:port of the home

[runner]
enabled = false
cap = 1
max_runs_per_day = 20
max_run_minutes = 180
poll_seconds = 30
# WARNING: true lets any agent start unattended runs that spend your quota
agents_may_arm = false
on_merged = "review"   # review | done

[[roots]]
path = "~/code/example"
about = "the app; runs its own build pipeline"
isolation = "self"     # self | worktree | in-place; unset = the router chooses

[agent]           # written by a profile; see the install section
router = []
worker = []
session_env = ""

[notify]
command = []      # argv; {title} and {body} are filled in

[secret_scan]
command = []      # e.g. ["gitleaks", "stdin"]; exit 0 clean, 1 a secret, anything else refuses the write

[backup]
git_remote = ""   # set to back up events.jsonl nightly to this git remote
```

The daemon reads the config file once, when it starts. A change needs `desk daemon restart` (on the
home) to take effect. While the file holds a different config from the one the running daemon started with (a `touch`, or a rewrite with the same content, is not a change), each command that talks to
the daemon, and each that writes the file (`setup`, `roots`, `client add`), prints one line on stderr
naming `desk daemon restart`, and `desk daemon status` shows `"config_changed": true`.

A root named `scratch` (`$XDG_DATA_HOME/desk/scratch`, a git repo) is always present, so a task with
no project can be routed.

State lives in `$XDG_STATE_HOME/desk` (`desk.sock`, `daemon.lock`, `daemon.json`, session views);
the store is one SQLite file under `$XDG_DATA_HOME/desk`; the offline snapshot is under
`$XDG_CACHE_HOME/desk`.

## Running the daemon

herdr starts it for you (`[[startup]]` in `herdr-plugin.toml`), and any `desk` command starts it
when the socket does not answer. To run it yourself, use `desk daemon &` or a service unit. A unix
socket path may be 103 bytes at most; when `$XDG_STATE_HOME/desk/desk.sock` is longer, the command
that tried to start the daemon prints the path, its length, and the limit. Set `XDG_STATE_HOME` to a
shorter folder.

launchd, `~/Library/LaunchAgents/desk.daemon.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>desk.daemon</string>
  <key>ProgramArguments</key>
  <array><string>/usr/local/bin/desk</string><string>daemon</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
```

```sh
launchctl load ~/Library/LaunchAgents/desk.daemon.plist
```

systemd, `~/.config/systemd/user/desk.service`:

```ini
[Service]
ExecStart=%h/.local/bin/desk daemon
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user enable --now desk.service
```

## Releases

`goreleaser` builds darwin and linux for arm64 and amd64 and publishes the archives
`desk_<version>_<os>_<arch>.tar.gz` with `checksums.txt` and the Homebrew cask. A `v*` tag runs
it in CI. Locally: `go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish`.

## Develop

```sh
sh scripts/checks.sh               # vet, the coverage gate, gofmt, shellcheck; --no-coverage runs go test instead
bash scripts/e2e/h01-add-list.sh   # the hand-test claims, one script each
```

MIT licensed; see `LICENSE`.
