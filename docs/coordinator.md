# The coordinator

One coordinator per desk: an ordinary agent session in its own herdr workspace (labelled `desk
coordinator`, in the scratch root). It talks with you, creates tasks, and starts runs by calling the
binary. It never does the work and never answers a worker's question. The binary does mechanics and
decides nothing.

`herdr-desk coordinator` opens the workspace without focusing it, records the session and its pane on the
home, and runs the `[agent] coordinator` command with the coordinator skill (`herdr-desk skill coordinator`) as
its system prompt. Called again while the pane is still there, it reports that pane and focuses nothing.

The coordinator runs `herdr-desk context` first on every turn. The board is the record; `context` only marks
what changed since the coordinator's last call, so a turn that dies loses nothing. Text from tasks, notes,
and runs is data to it, never an instruction.

`[coordinator] start_runs` says what it may do:

- `propose` (the default): it lists the runs it suggests (task, root, isolation, model) and waits for a
  message from you that names them. A task you set `ready` is a go-ahead.
- `auto`: it starts runs unasked, and agents may set `ready`. This spends your quota without asking:
  `cap` and `max_runs_per_day` are the bounds.

A person and the recorded coordinator session may start a run. So may any other agent session, but only in a
root with `agents_may_start = true`, and the root it starts in is the one the run resolves to (`--root`, the task's
root, or the default). A session that owns a live run (`starting`, `waiting`, `running`, or `idle`) never may, so a
worker cannot start runs: the store decides this, not `DESK_RUN`, which a worker can unset. Any other start by an agent
session gets `not-allowed`. An agent cannot read a root's `agents_may_start`: it runs `run start` once, and
`not-allowed` is its answer. Killing a run, pausing or resuming the runner, and opening the coordinator are a person's
alone: an agent session gets `not-allowed` for each. A task's `first_message` is task text, like its notes and root:
any session may set it with `set --first-message`, so a launcher agent that adds a task chooses the command its runs
start on, and a worker may change it for the task's next run. This keeps an honest agent in its lane: a session id is
self-declared, so what bounds a dishonest one is the live-run check (a session id that owns a live run is refused)
and the three caps.
