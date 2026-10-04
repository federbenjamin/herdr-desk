# Third-party notices

desk is built from the Go modules below (the build list in `go.mod`). Each keeps its own license; the
full text ships in the module's source at the version listed.

| module | version | license |
|---|---|---|
| github.com/dustin/go-humanize | v1.0.1 | MIT |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/inconshreveable/mousetrap | v1.1.0 | Apache-2.0 |
| github.com/mattn/go-isatty | v0.0.24 | MIT |
| github.com/ncruces/go-strftime | v1.0.0 | MIT |
| github.com/pelletier/go-toml/v2 | v2.4.3 | MIT |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |
| github.com/spf13/cobra | v1.10.2 | Apache-2.0 |
| github.com/spf13/pflag | v1.0.9 | BSD-3-Clause |
| golang.org/x/sys | v0.48.0 | BSD-3-Clause |
| modernc.org/libc | v1.77.1 | BSD-3-Clause; third-party parts listed in its `LICENSE-3RD-PARTY.md` |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause |
| modernc.org/memory | v1.12.1 | BSD-3-Clause; parts under the Go license and mmap-go's BSD-3-Clause |
| modernc.org/sqlite | v1.60.1 | BSD-3-Clause; SQLite itself is in the public domain (`LICENSE-SQLITE`) |

The Go standard library and runtime compiled into the binary are under the Go license (BSD-3-Clause),
Copyright The Go Authors.
