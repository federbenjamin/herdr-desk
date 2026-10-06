# Changelog

## Unreleased

- `herdr plugin install federbenjamin/herdr-desk` is the whole install: its build step places the binary,
  then runs `herdr-desk setup`, with the `claude-code` profile when `claude` is on PATH, and with `claude`
  on PATH installs the Claude Code plugin (`claude plugin marketplace add federbenjamin/herdr-desk`, then
  `claude plugin install herdr-desk@herdr-desk`). Without `claude` it says how to set `[agent]` and prints
  the two `/plugin` commands. A step that does not finish is reported, and the install still succeeds;
  `herdr-desk setup` re-runs it.
- `herdr-desk setup` creates herdr's `config.toml` when herdr is found and has none, holding only
  herdr-desk's keys and sidebar row (0600, no backup). With no herdr found, or with `--no-herdr`, it
  still writes nothing there.
- `herdr-desk setup` writes to `HERDR_CONFIG_PATH` when it is set, the file herdr itself reads then.
