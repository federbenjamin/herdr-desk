# herdr-desk

herdr-desk is a task board for people who work with coding agents in herdr. A coordinator agent turns what you ask for into tasks and runs, a runner starts an agent on a task in its own herdr workspace and hands the task back to you, and a journal gives every agent session a memory. It ships as a CLI and a board; without herdr you keep the board, the CLI, and the journal.

Go, single static binary, open source. Never hard-code the author's machines, paths, agents, or harness.

## Project state

- 2026-10-09: not launched — no release yet; the GitHub repo is public. Retires at the first version tag.
- 2026-10-06: the project holds no user data; each user's store and journal stay on their own machines. Retires if it ever stores or sends data off the user's machines.

<!-- >>> git-workflow (generated block; do not edit by hand) -->
## Git workflow

- `main` changes only through a PR, squash-merged.
<!-- <<< git-workflow -->
