# desk

A task board for you and your agents, with a runner that turns a task you arm into a working agent
in a herdr workspace, and a journal that gives every agent session a memory.

Status: pre-release, under construction. This version has the store, the daemon, the CLI, the
journal, and the packaging. The runner (`desk worker`) and the full-screen board come later; bare
`desk` prints a static board for now.

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
`desk` is on your PATH it copies the binary to `~/.local/bin/desk` and says so.

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
  successful call (`queued` on stdout, and stderr says why the home did not answer). Delivery is at
  least once. A queued entry the home refuses is dropped and named on stderr; one it cannot take yet
  (`scan-failed`, a server error) stays queued, and the command that tried to forward it fails
  naming the outbox file.

## Commands

Task ids: `T12`, `t12`, and `12` name the same task. Every command that reads or changes tasks
takes `--json`. A refusal prints `desk <command>: <code>: <message>` on stderr and nothing on
stdout.

| exit | meaning |
|---|---|
| 0 | done, or already true |
| 1 | the item was refused; stderr starts with a stable code |
| 2 | usage error |
| 3 | store or home I/O (`home-unreachable`, `scan-failed`, any failure to reach or read the home) |

Refusal codes: `unknown-task`, `unknown-step`, `unknown-project`, `unknown-event`, `empty-title`,
`secret-detected`, `not-allowed` (an agent set `ready` or `done`), `backup-off`.

| command | does |
|---|---|
| `desk` | a static board: `NEEDS YOU` (blocked, review), `IN MOTION` (started), `ON DECK` (ready, then open); first line `desk · home · runner on\|off`, or `desk · offline (snapshot <age>)` |
| `desk add -t <title> [-n <notes>] [-p <project>\|--desk] [--thread <name>] [--status <s>] [--tag <t>]… [--branch <b>]` | creates a task and prints `T<n>`. With no `-p` or `--desk` the project is the main checkout of the git repo you are in. `-p` takes an absolute directory or the bare name of a known project. When git cannot run (not on your PATH, a timeout), the add stops with exit 3 rather than store a task without its project |
| `desk list [--ready\|--open\|--done\|--archived\|--all] [-p <project>\|--desk]` | lists tasks; default is the five live statuses. `-p` and `--desk` narrow every filter, `--all` too; a bare name no task's project carries is `unknown-project`, as for `add` |
| `desk show <task>` | one task with its steps and history |
| `desk set <task> [<status>] [--thread <t>] [--root <r>] [--isolation <i>] [--model <m>] [--archive\|--unarchive] [--ref <ref>] [--merged]` | patches fields; `review --merged` writes the status that `runner.on_merged` names |
| `desk edit <task> [--title <t>] [--notes <n>]` | replaces the title or the notes |
| `desk steps <task> add <text>` · `toggle <id>` · `rename <id> <text>` · `remove <id>` | step ids are `s1`, `s2`, … per task, never reused |
| `desk note <text> [--task <task>] [--ref <ref>] [--branch <b>] [--tag <t>]…` | appends a note and prints `e<id>`, or `queued` when the home is unreachable |
| `desk note --merged --branch <b> [--pr <n>] [--sha <sha>] [<text>]` | records that a branch merged |
| `desk decide <text> [--tag <k:v>]… [--replaces e<id>] [--task <task>]` | appends a decision |
| `desk session [<id>] [--md] [--all] [--continues <old-id>]` | prints the session's journal view; `--all` shows hidden lines; `--continues` first links the session to an older one |
| `desk capture` | reads one line on stdin: words starting `#` set the thread, `@` the project, the rest is the title |
| `desk daemon [run]` · `stop` · `restart` · `status` | runs or controls the daemon. A second `run` prints `already running` and exits 0; on a client it prints that there is nothing to run and exits 0. `status` never starts a daemon |
| `desk token [show]` · `rotate` | prints or rotates the token |
| `desk client add <host:port> [--token-file <path>]` | joins a home; the token comes from the file or stdin, never an argument |
| `desk roots [list]` · `add <path> [--about <a>] [--isolation <i>]` · `remove <path>` | edits `[[roots]]` |
| `desk setup [--profile claude-code] [--listen <host:port>] [--runner on\|off] [--skill-dir <dir>] [--force] [--no-herdr]` | first-time setup; never prompts |
| `desk hook start --format claude-code` | the session hook: reads the hook's JSON on stdin and prints the path of the session's journal view |
| `desk backup` | runs the backup now |
| `desk version` | prints the version |

The session is the first of `--session <id>`, `$DESK_SESSION`, and the variable named by
`[agent] session_env`. `DESK_HOOKS=off` makes `desk hook` do nothing.

A write whose text holds a secret (private keys, AWS, GitHub, Anthropic, and Slack tokens) is
refused with `secret-detected` and the pattern's name, never the match.

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

A root named `scratch` (`$XDG_DATA_HOME/desk/scratch`, a git repo) is always present, so a task with
no project can be routed.

State lives in `$XDG_STATE_HOME/desk` (`desk.sock`, `daemon.lock`, `daemon.json`, session views);
the store is one SQLite file under `$XDG_DATA_HOME/desk`; the offline snapshot is under
`$XDG_CACHE_HOME/desk`.

## Running the daemon

herdr starts it for you (`[[startup]]` in `herdr-plugin.toml`), and any `desk` command starts it
when the socket does not answer. To run it yourself, use `desk daemon &` or a service unit.

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
go vet ./... && sh scripts/coverage.sh && test -z "$(gofmt -l .)" && shellcheck scripts/*.sh scripts/e2e/*.sh
bash scripts/e2e/h01-add-list.sh   # the hand-test claims, one script each
```

MIT licensed; see `LICENSE`.
