# Commands

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
a run, paused the runner, or started a run without being the coordinator or being in a root with
`agents_may_start`; a session that owns a live run started one; or `run start` named an archived or `done`
task), `stale-run` (a newer run owns the task; a run's step write counts too), `stale` (a notes save read notes that
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
| `herdr-desk show <task>` | one task with its steps and history; prints `first_message: <template>` under `model:` when the task sets one |
| `herdr-desk set <task> [<status>] [--thread <t>] [--root <r>] [--isolation <i>] [--model <m>] [--first-message <template>] [--archive\|--unarchive] [--ref <ref>] [--merged]` | patches fields; `review --merged` writes the status that `runner.on_merged` names. `--first-message` sets the task's first-message template (it must hold `{task_file}`) and `--first-message ''` clears it. Setting `done` ends the task's live run, an `idle` one included |
| `herdr-desk edit <task> [--title <t>] [--notes <n>] [--append-notes <text>]` | `--notes` replaces the notes. `--append-notes` adds `<text>` on a new line at the end: it writes only if nobody changed the notes since it read them, and when someone did it reads again and retries once; a second `stale` exits 1. Empty or whitespace-only `<text>` is refused `empty-text` and writes nothing |
| `herdr-desk steps <task> add [--id <id>] <text>` · `toggle <id>` · `done <id>` · `rename <id> <text>` · `remove <id>` | generated step ids are `s1`, `s2`, … per task, never reused. `add --id <id>` picks the id (1 to 64 of letters, digits, `.`, `_`, `-`; never `s<n>`, which is `bad-input`); adding an id that exists writes nothing, keeps the step as it is, and prints the steps, then `unchanged`. `done <id>` sets the step done and never flips it back: it prints `changed` or `unchanged`, exit 0 both times (`toggle` flips, as the board's `space` does). `--json` prints `{"task": …, "changed": …}` for every op, `changed` true when the op wrote. A step write from a run that is not the task's newest is `stale-run` |
| `herdr-desk note <text> [--task <task>] [--ref <ref>] [--branch <b>] [--tag <t>]…` | appends a note and prints `e<id>`, or `queued` when the home is unreachable (stderr says why the home did not answer) |
| `herdr-desk note --merged --branch <b> [--pr <n>] [--sha <sha>] [<text>]` | records that a branch merged |
| `herdr-desk decide <text> [--tag <k:v>]… [--replaces e<id>] [--task <task>]` | appends a decision |
| `herdr-desk session [<id>] [--md] [--all] [--continues <old-id>]` | prints the session's journal view; `--json` prints it with the keys `session`, `work`, `todo`, `decisions`; `--all` shows hidden lines; `--continues` first links the session to an older one |
| `herdr-desk capture` | on a terminal (stdin and stdout both terminals, no `--json`), the capture popup: one line (words starting `#` set the thread, `@` the project, the rest is the title); Enter adds the task and prints `T<n>`; a refused line shows its error under the line and stays there; an empty line, `esc`, or `ctrl+c` exits 0 with no task. While the home has not answered an `enter`, keys wait, and `esc` ends the popup once it answers. Anywhere else it reads lines on stdin: with stdin a terminal it prompts `capture: ` on stderr, and after a refusal prints the error and asks again; with stdin not a terminal it reads one line and exits with the code of its refusal, as every command does. An empty line ends it with exit 0 |
| `herdr-desk run start <task> [--root <r>] [--isolation <i>] [--model <m>] [--first-message <template>] [--json]` | the one way to start a run; prints `run <id>  T<n>  <state>  <root>  <isolation>  <model>`. When the run fails to start in this call it prints the `failed` run, then `run-failed: run <id> failed: <reason>` on stderr, and exits 1. [The runner](runner.md) says what it asks and refuses |
| `herdr-desk runs [--all] [--json]` | checks the live runs against herdr once, then lists them, oldest first: `run <id>  T<n>  <state>  <root>  <isolation>  <model>  <elapsed>` (`-` for a field not decided yet); `no live runs` when none. `--all` lists every run; `--json` prints the array |
| `herdr-desk runs kill <task>` | kills the processes in the task's pane, closes the pane, ends the run `killed`, and blocks the task; prints `T<n> blocked`. `no-run` when the task has no live run; an agent gets `not-allowed`. When the pane did not close, a process outlived the kill, or herdr could not say what ran in the pane (so nothing was signalled), the task is still blocked, the note on it says what is left, and the command exits 3; the ticker closes that pane again on its next tick |
| `herdr-desk runner [status]` · `pause` · `resume` | prints `runner <state>`, and ` · <live>/<cap> live` when the state is `on` or `paused`, then ` · no ticker: …` when no ticker runs (the board's header ends `· no ticker` too, and `context` prints the same line), since nothing then stops a run at `max_run_minutes`. `pause` starts no new runs, live runs go on, and the pause survives a restart; an agent gets `not-allowed` |
| `herdr-desk coordinator` | opens the desk's coordinator in its own herdr workspace, or reports it when its pane is still open; prints `coordinator opened\|open: workspace <id>, pane <id>`. It never focuses: a script's herdr focus moves every attached herdr window, so the first call opens the workspace without focusing it and a later call only names it. On a client it prints where to run it and exits 0; an agent session gets `not-allowed`; no herdr is `no-herdr` |
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
| `herdr-desk hook herdr-event` | herdr runs it on pane events (see [Tracking](runner.md#tracking)); it reads the pane id from the event and exits 0 on every path, writing errors to `<state>/herdr-desk.log`: an event with no pane id, a config that does not load, and a store of another schema version (a binary swap) are logged too |
| `herdr-desk backup` | runs the backup now; a failed run exits 3 naming the git step that failed, with the remote shown as `<remote>` |
| `herdr-desk version` | prints the version. A build made from source by the herdr plugin's install step carries the plugin manifest's version with `+src`, for example `0.1.0+src` |

The session is the first of `--session <id>`, `$DESK_SESSION`, and the variable named by
`[agent] session_env`. `DESK_HOOKS=off` makes `herdr-desk hook` do nothing.

A write whose text holds a secret (private keys, AWS, GitHub, Anthropic, and Slack tokens) is
refused with `secret-detected` and the pattern's name, never the match.
