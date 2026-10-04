// Command desk is a task board and session journal for you and your agents.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/federbenjamin/desk/internal/cli"
	"github.com/federbenjamin/desk/internal/daemon"
)

func main() {
	tty := false
	if fi, err := os.Stdin.Stat(); err == nil {
		tty = fi.Mode()&os.ModeCharDevice != 0
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], cli.Env{
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Getenv:   os.Getenv,
		Cwd:      workingDir(os.Getwd, os.Stderr),
		StdinTTY: tty,
		Spawn:    daemon.Spawn,
	}))
}

// workingDir is the process's working directory. When it cannot be read, a task added here gets no project, so
// the user is told why.
func workingDir(getwd func() (string, error), stderr io.Writer) string {
	cwd, err := getwd()
	if err != nil {
		fmt.Fprintf(stderr, "desk: the working directory cannot be read, so it names no project: %v\n", err)
		return ""
	}
	return cwd
}
