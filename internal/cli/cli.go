// Package cli is desk's command line. Run takes its streams, environment, and working directory as values,
// so a test drives a command without touching the process.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/daemon"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

// Env is what a command reads from and writes to in place of the process.
type Env struct {
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Getenv   func(string) string
	Cwd      string
	StdinTTY bool
	Spawn    func(config.Paths) error // starts the daemon; nil → never (tests)
}

// The exit codes.
const (
	exitOK      = 0
	exitRefused = 1
	exitUsage   = 2
	exitIO      = 3
)

// exitError is a command's failure with the exit code it maps to.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func usage(format string, args ...any) error {
	return &exitError{code: exitUsage, msg: fmt.Sprintf(format, args...)}
}

// exitCode maps a command's error to its exit code: a refusal by its code, anything else that got past the
// start of a command is store or home I/O.
func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	if r, ok := model.AsRefusal(err); ok {
		switch r.Code {
		case model.CodeBadInput:
			return exitUsage
		case model.CodeHomeUnreachable, model.CodeScanFailed, model.CodeBadToken:
			return exitIO
		}
		return exitRefused
	}
	return exitIO
}

// app is one Run: the environment, the flags every command shares, and what is loaded once.
type app struct {
	ctx        context.Context
	env        Env
	paths      config.Paths
	cfg        *config.Config
	session    string // --session
	started    bool   // a command's own code began; an error before it is a usage error
	unanswered string // why the last write was queued, from api.ClientOptions.Unreachable

	usedHome    bool // the command made a client, so it talked to the daemon
	wroteConfig bool // the command wrote the config file
}

// Run runs one desk command and returns its exit code. args excludes the program name.
func Run(ctx context.Context, args []string, env Env) int {
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	a := &app{ctx: ctx, env: env, paths: config.ResolvePaths(env.Getenv)}
	root := a.rootCmd()
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	if args == nil {
		args = []string{}
	}
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	code := exitOK
	if err != nil {
		code = exitUsage
		if a.started {
			code = exitCode(err)
		}
		name := "desk"
		if cmd != nil {
			name = cmd.CommandPath()
		}
		fmt.Fprintf(env.Stderr, "%s: %s\n", name, err)
	}
	if a.usedHome || a.wroteConfig {
		a.warnStaleConfig()
	}
	return code
}

// warnStaleConfig says so, once, when the daemon running on this machine started before the config file was
// last written: the daemon reads the file only at start. A client runs no daemon, so it has nothing to say.
func (a *app) warnStaleConfig() {
	c, err := config.Load(a.paths.ConfigFile())
	if err != nil || c.IsClient() {
		return
	}
	if info, ok := daemon.Running(a.paths); ok && a.paths.ConfigChangedSince(info.StartedTS) {
		fmt.Fprintln(a.env.Stderr, "desk: the config file changed after the daemon started; run `desk daemon restart` to apply it")
	}
}

// do wraps a command's body so Run can tell its errors from cobra's argument errors.
func (a *app) do(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		a.started = true
		return fn(cmd, args)
	}
}

func (a *app) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "desk",
		Short:         "A task board and session journal for you and your agents",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usage("%v", err) })
	root.PersistentFlags().StringVar(&a.session, "session", "", "the session this call belongs to (default: $DESK_SESSION, then [agent] session_env)")
	a.boardFlags(root)
	root.AddCommand(
		a.addCmd(), a.listCmd(), a.showCmd(), a.setCmd(), a.editCmd(), a.stepsCmd(), a.captureCmd(),
		a.noteCmd(), a.decideCmd(), a.sessionCmd(),
		a.daemonCmd(), a.tokenCmd(), a.clientCmd(), a.rootsCmd(), a.setupCmd(), a.backupCmd(), a.versionCmd(),
		a.hookCmd(),
	)
	return root
}

// config loads the config file once.
func (a *app) config() (config.Config, error) {
	if a.cfg != nil {
		return *a.cfg, nil
	}
	c, err := config.Load(a.paths.ConfigFile())
	if err != nil {
		return config.Config{}, err
	}
	a.cfg = &c
	return c, nil
}

// client returns a client of this machine's home. A queued entry the home refuses is reported on stderr and
// changes nothing else about the command.
func (a *app) client() (*api.Client, error) {
	c, err := a.config()
	if err != nil {
		return nil, err
	}
	a.usedHome = true
	return api.NewClient(api.ClientOptions{
		Paths:  a.paths,
		Config: c,
		Spawn:  a.env.Spawn,
		Refused: func(kind model.Kind, r *model.Refusal) {
			fmt.Fprintf(a.env.Stderr, "desk: a queued %s was refused: %s\n", kind, r.Error())
		},
		Unreachable: func(err error) {
			a.unanswered = err.Error()
			if r, ok := model.AsRefusal(err); ok {
				a.unanswered = r.Msg
			}
		},
	}), nil
}

// callerSession is the first of --session, DESK_SESSION, and the variable [agent] session_env names.
func (a *app) callerSession() (string, error) {
	s := a.session
	if s == "" {
		s = a.env.Getenv("DESK_SESSION")
	}
	if s == "" {
		c, err := a.config()
		if err != nil {
			return "", err
		}
		if c.Agent.SessionEnv != "" {
			s = a.env.Getenv(c.Agent.SessionEnv)
		}
	}
	if s != "" && !model.ValidSessionID(s) {
		return "", badSessionID()
	}
	return s, nil
}

func badSessionID() error {
	return usage("a session id is 1 to 128 letters, digits, '.', '_', or '-', and not . or ..")
}

// runID is DESK_RUN when it is a positive integer, else 0.
func (a *app) runID() int64 {
	n, err := strconv.ParseInt(a.env.Getenv("DESK_RUN"), 10, 64)
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// actor is who makes this call's writes.
func (a *app) actor() (store.Actor, error) {
	s, err := a.callerSession()
	if err != nil {
		return store.Actor{}, err
	}
	return store.Actor{Session: s, Run: a.runID()}, nil
}

// parseTask reads T12, t12, or 12.
func parseTask(s string) (int, error) {
	digits := s
	if strings.HasPrefix(s, "T") || strings.HasPrefix(s, "t") {
		digits = s[1:]
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 {
		return 0, usage("%q is not a task id (T12, t12, or 12)", s)
	}
	return n, nil
}

// parseEventID reads e12 or 12.
func parseEventID(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimPrefix(s, "e"), 10, 64)
	if err != nil || n < 1 {
		return 0, usage("%q is not an event id (e12 or 12)", s)
	}
	return n, nil
}

func (a *app) printJSON(v any) error {
	return json.NewEncoder(a.env.Stdout).Encode(v)
}

func (a *app) say(format string, args ...any) {
	fmt.Fprintf(a.env.Stdout, format+"\n", args...)
}

func (a *app) warn(cmd *cobra.Command, format string, args ...any) {
	fmt.Fprintf(a.env.Stderr, cmd.CommandPath()+": "+format+"\n", args...)
}
