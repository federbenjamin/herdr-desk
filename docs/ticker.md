# Running the ticker

herdr starts the ticker (`[[startup]]` in `herdr-plugin.toml`). It is one process per home, holds
`ticker.lock` so a second one exits 0, and once a minute does the timed jobs: the backup when one is due
(checked hourly), then the run jobs above, whatever `runner.enabled` says: switching the runner off stops new
runs, not the limits on live ones. A command on a home with no ticker still works; only the timed jobs wait.
With no herdr, run it yourself with `herdr-desk ticker &` or a service unit. Its errors, and every line the
runner logs in any process on the home (the ticker, a command, `herdr-desk rpc`, the event hook), go to
`<state>/herdr-desk.log`.

launchd, `~/Library/LaunchAgents/herdr-desk.ticker.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>herdr-desk.ticker</string>
  <key>ProgramArguments</key>
  <array><string>/usr/local/bin/herdr-desk</string><string>ticker</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
```

```sh
launchctl load ~/Library/LaunchAgents/herdr-desk.ticker.plist
```

systemd, `~/.config/systemd/user/herdr-desk.service`:

```ini
[Service]
ExecStart=%h/.local/bin/herdr-desk ticker
Restart=on-failure

[Install]
WantedBy=default.target
```

```sh
systemctl --user enable --now herdr-desk.service
```
