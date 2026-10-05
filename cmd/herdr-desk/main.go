// Command herdr-desk is a task board and session journal for you and your agents.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/x/term"

	"github.com/federbenjamin/herdr-desk/internal/cli"
)

func main() {
	stdinTTY, stdoutTTY := isTerminal(os.Stdin), isTerminal(os.Stdout)
	os.Exit(cli.Run(context.Background(), os.Args[1:], cli.Env{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Getenv:    os.Getenv,
		Cwd:       workingDir(os.Getwd, os.Stderr),
		StdinTTY:  stdinTTY,
		StdoutTTY: stdoutTTY,
	}))
}

// isTerminal reports whether f is a terminal: one the terminal's own ioctl answers for, so /dev/null and other
// character devices are not.
func isTerminal(f *os.File) bool { return term.IsTerminal(f.Fd()) }

// workingDir is the process's working directory. When it cannot be read, a task added here gets no project, so
// the user is told why.
func workingDir(getwd func() (string, error), stderr io.Writer) string {
	cwd, err := getwd()
	if err != nil {
		fmt.Fprintf(stderr, "herdr-desk: the working directory cannot be read, so it names no project: %v\n", err)
		return ""
	}
	return cwd
}
