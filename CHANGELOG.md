# Changelog

## Unreleased

- `herdr plugin install federbenjamin/herdr-desk` is the whole install: its build step places the binary,
  then runs `herdr-desk setup`, with the `claude-code` profile when `claude` is on PATH, and with `claude`
  on PATH installs the Claude Code plugin (`claude plugin marketplace add federbenjamin/herdr-desk`, then
  `claude plugin install herdr-desk@herdr-desk`). Without `claude` it says how to set `[agent]` and prints
  the two `/plugin` commands. A step that does not finish is reported, and the install still succeeds.
  herdr shows a build step's output only when it fails, so the report is also written to
  `<XDG_STATE_HOME or ~/.local/state>/herdr-desk/install.log`. `herdr-desk setup` re-runs setup; the two
  `claude plugin` commands re-run the Claude Code plugin step.
- `herdr-desk setup` creates herdr's `config.toml` when herdr is found and has none, holding only
  herdr-desk's keys and sidebar row (0600, no backup). With no herdr found, or with `--no-herdr`, it
  still writes nothing there; with no herdr found it says why and to run `herdr-desk setup` once herdr is
  found.
- `herdr-desk setup` writes to `HERDR_CONFIG_PATH` when it is set, the file herdr itself reads then.
