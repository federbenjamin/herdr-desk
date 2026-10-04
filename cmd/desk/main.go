// Command desk is a task board and session journal for you and your agents.
package main

import (
	"context"
	"os"

	"github.com/federbenjamin/desk/internal/cli"
	"github.com/federbenjamin/desk/internal/daemon"
)

func main() {
	cwd, _ := os.Getwd()
	tty := false
	if fi, err := os.Stdin.Stat(); err == nil {
		tty = fi.Mode()&os.ModeCharDevice != 0
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], cli.Env{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Getenv:   os.Getenv,
		Cwd:      cwd,
		StdinTTY: tty,
		Spawn:    daemon.Spawn,
	}))
}
