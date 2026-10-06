# Config

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
# WARNING: auto lets the coordinator start runs unasked and agents set tasks ready, which spends your quota; another agent may start a run only in a root with agents_may_start = true
start_runs = "propose" # propose | auto

[[roots]]
path = "~/code/example"
about = "the app; runs its own build pipeline"
isolation = "self"     # self | worktree | in-place; unset = worktree in a git work tree, else in-place
first_message = "/build {task_file}" # optional; the worker's first message; see "The spawn"
agents_may_start = false # optional; lets agent sessions other than the coordinator start runs in this root

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
the outbox, the runner's pause file, session views, each run's first-message file under `runs/` (the ticker removes it once the run has ended, failed, or been killed), and on a
client the ssh control socket);
the store is one SQLite file under `$XDG_DATA_HOME/herdr-desk`; the offline snapshot is under
`$XDG_CACHE_HOME/herdr-desk`.
