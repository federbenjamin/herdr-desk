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
  `cap`, `max_runs_per_day`, and `max_run_minutes` are the bounds.

A person and the recorded coordinator session may start a run. Killing a run and pausing the runner are a
person's alone. Any other agent session, a worker included, gets `not-allowed`. This keeps an honest agent
in its lane: a session id is self-declared, so the three caps are what bounds a dishonest one.
