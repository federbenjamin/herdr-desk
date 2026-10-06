# The runner

Runs are one per task, in a root, with an isolation (`self`, `worktree`, or `in-place`) and a model.
Turn the runner on with `herdr-desk setup --runner on` (or `[runner] enabled = true`). It needs herdr:
`DESK_HERDR` names the herdr binary when it is not on the PATH; a set value must be the absolute path of
an executable file, else the runner is `no-herdr`, and PATH is then not searched.

**Starting.** `herdr-desk run start T<n> [--root <r>] [--isolation <i>] [--model <m>] [--first-message <template>]` is the one entry;
the coordinator, you, and the board's `S` all call it. No loop looks for work. It asks of the task that it
exists, is not archived, is not `done`, and has no `starting`, `waiting`, or `running` run; the task's
thread and who set `ready` do not matter. Each field resolves from the flag, else the task's own field,
else a default: the task's project when it is a listed root, else the scratch root; the root's
isolation, else `worktree` in a git work tree and `in-place` elsewhere; the first of `[agent] models`. The
first message resolves in three tiers: the `--first-message` flag, else the task's `first_message`, else the
root's, else none (the worker starts on plain text). The root's value is used only when the flag and the task's
field are both empty, and it is recorded on the run, never copied onto the task. A root that is not listed, an
isolation that is not one of the three, a model not in `[agent] models`, or a first message without
`{task_file}` is `bad-input`. It sets the task `started` and prints the run.

It refuses, in this order: `not-allowed` for a session that owns a live run (before the task is read, so it
learns nothing about it); `runner-off`; `runner-paused`; `no-herdr`; a route it cannot resolve (`bad-input`);
`not-allowed` for an agent session that is not the coordinator when the resolved root does not set
`agents_may_start` ([who may start](coordinator.md)); an archived or `done` task (`not-allowed`); `cap-reached` once today's runs reach `max_runs_per_day`. Today's count and the new run are
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
`{model}` is the run's model, `{session}` its session id, `{message}` the first message. A run whose work
goes through its own command has a `first_message`, a template that must hold `{task_file}`, set by the flag, the
task, or the root (see Starting): the worker then
writes the first message to `runs/run-<id>.md` in the state folder (0600) and `{message}` is the template with
`{task_file}` replaced by that file's path, so `first_message = "/build {task_file}"` starts the agent on
`/build` with the task's file. That task file omits the paragraph on how to hand back, because the command's own
pipeline hands back; a run with no `first_message` keeps it. The ticker removes the file once the run has ended,
failed, or been killed. A file it cannot write blocks the task, as a worker command that cannot start
does. No task text ever reaches a shell. A note records the workspace and pane, and `[notify] command` runs
once per spawn. A worker finishes with `review` or `blocked` (`herdr-desk set T<n> review` or `blocked`),
which ends its run. Every write it makes carries its run id, and a write from a run that is not the task's
newest is refused with `stale-run`.

## Tracking

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
as the `claude` trust question ([Install](../README.md#herdr-with-claude-code)), shows as `blocked` with no session: the task stays `blocked` until a
person answers in the pane, and the worker, still the task's newest run, can then hand it back.

## The ticker's run jobs

Once a minute, and only while a run is live (with none and no pane left open, herdr is not asked), the
ticker does jobs 1 to 4:

1. checks every `running` and `idle` run against one `herdr pane list`, with the same table, for an event
   herdr never delivered;
2. stops a `running` run past `max_run_minutes`, counted from its spawn and not from a wait before it, as
   `herdr-desk runs kill` does, and blocks the task;
3. starts the oldest `waiting` run whose root is free and whose slot is open, and fails a run left
   `starting` for over a minute;
4. closes again a pane a kill could not close, and blocks a task left `started` after its newest run ended.

Jobs 5 and 6 run on every tick, with or without a live run, and never ask herdr:

5. removes the first-message file of every run that ended, failed, or was killed (a live run's file stays);
6. removes the worktree of a done task when it is clean and no run is live (a plain worktree remove, never
   forced, since a dirty tree can hold unpushed work). A dirty one stays, with one note on the task naming it,
   written once per `done`. The branch `desk/T<n>-<slug>` is left as it is.

`herdr-desk runs` and `herdr-desk context` check the runs once when called; the board's refresh does not.
When the check cannot run (no herdr on the home, herdr not answering), they list the runs as the store has
them and say why the check did not run: `runs` on stderr, `context` under `live runs:`.

## The sidebar row

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
