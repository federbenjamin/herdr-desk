package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/ticker"
	"github.com/federbenjamin/herdr-desk/internal/version"
)

// ServerOptions configures a Server.
type ServerOptions struct {
	Store  *store.Store
	Config config.Config
	Paths  config.Paths
	Backup func(ctx context.Context) (backup.Result, error) // nil → backup.run refuses backup-off
	Runner RunnerControl                                    // nil → runs.start, runs.kill, and runner.pause are unknown methods; runner_state is "off"
}

// RunnerControl is what the server needs from the runner. *runner.Runner satisfies it.
type RunnerControl interface {
	State() string
	Paused() bool
	Pause(ctx context.Context, a store.Actor, paused bool) error
	Kill(ctx context.Context, a store.Actor, task int) (model.Task, error)
	Start(ctx context.Context, a store.Actor, task int, route store.RunRoute) (model.Run, error)
	AfterSet(ctx context.Context, task int)
	Reconcile(ctx context.Context) error
}

var _ RunnerControl = (*runner.Runner)(nil)

// Server answers the API methods from one store. It is a Transport whose Close closes nothing: its store is the
// caller's.
type Server struct {
	o       ServerOptions
	methods map[string]method
}

// method decodes a request's params and calls the store.
type method func(ctx context.Context, params []byte) (any, error)

// badRequest is a request whose params do not decode.
type badRequest struct{ err error }

func (b badRequest) Error() string { return b.err.Error() }

// bind is the one decode-and-call adapter every method goes through.
func bind[Req any](fn func(ctx context.Context, r Req) (any, error)) method {
	return func(ctx context.Context, params []byte) (any, error) {
		var r Req
		if err := json.Unmarshal(params, &r); err != nil {
			return nil, badRequest{err}
		}
		return fn(ctx, r)
	}
}

// homeServer is the server over r's store, as every command on the home and `herdr-desk rpc` serve it.
func homeServer(r *runner.Runner, p config.Paths, c config.Config) *Server {
	o := ServerOptions{Store: r.Store(), Config: c, Paths: p, Runner: r}
	if remote := c.Backup.GitRemote; remote != "" {
		o.Backup = func(ctx context.Context) (backup.Result, error) { return backup.Run(ctx, r.Store(), p, remote) }
	}
	return NewServer(o)
}

// NewServer returns a server over o.Store.
func NewServer(o ServerOptions) *Server {
	s := &Server{o: o}
	st := o.Store
	s.methods = map[string]method{
		MethodTasksList: bind(func(ctx context.Context, f store.Filter) (any, error) {
			tasks, err := st.ListTasks(ctx, f)
			if tasks == nil {
				tasks = []model.Task{}
			}
			return TaskList{Tasks: tasks}, err
		}),
		MethodTasksGet: bind(func(ctx context.Context, r getRequest) (any, error) {
			return st.GetTask(ctx, r.Number)
		}),
		MethodTasksAdd: bind(func(ctx context.Context, r addRequest) (any, error) {
			return st.AddTask(ctx, r.Actor, r.Input)
		}),
		MethodTasksSet: bind(func(ctx context.Context, r setRequest) (any, error) {
			t, err := st.SetTask(ctx, r.Actor, r.Number, r.Patch)
			if err == nil && r.Patch.Status != nil && o.Runner != nil {
				o.Runner.AfterSet(ctx, r.Number)
			}
			return t, err
		}),
		MethodTasksSteps: bind(func(ctx context.Context, r stepsRequest) (any, error) {
			return st.Step(ctx, r.Actor, r.Number, r.Op)
		}),
		MethodEventsAppend: bind(s.appendEvent),
		MethodSessionView: bind(func(ctx context.Context, r sessionRequest) (any, error) {
			return st.SessionEvents(ctx, r.Session)
		}),
		MethodRunsList: bind(func(ctx context.Context, r runsRequest) (any, error) {
			if r.Reconcile && o.Runner != nil {
				// A failed reconcile is logged by the runner; the list is still the store's truth now.
				_ = o.Runner.Reconcile(ctx)
			}
			runs, err := st.ListRuns(ctx)
			if runs == nil {
				runs = []model.Run{}
			}
			return runs, err
		}),
		MethodCoordinatorChanges: bind(func(ctx context.Context, r changesRequest) (any, error) {
			return st.Changes(ctx, r.Actor, maxChanges)
		}),
		MethodStatus: bind(func(ctx context.Context, _ empty) (any, error) { return s.status(ctx) }),
		MethodBackupRun: bind(func(ctx context.Context, _ empty) (any, error) {
			if o.Backup == nil {
				return nil, &model.Refusal{Code: model.CodeBackupOff, Msg: "no [backup] git_remote is configured"}
			}
			// A push the caller stops waiting for still finishes, so the remote never holds half a run.
			return o.Backup(context.WithoutCancel(ctx))
		}),
	}
	if rn := o.Runner; rn != nil {
		s.methods[MethodRunsKill] = bind(func(ctx context.Context, r killRequest) (any, error) {
			return rn.Kill(ctx, r.Actor, r.Task)
		})
		s.methods[MethodRunsStart] = bind(func(ctx context.Context, r startRequest) (any, error) {
			return rn.Start(ctx, r.Actor, r.Task, r.Route)
		})
		s.methods[MethodRunnerPause] = bind(func(ctx context.Context, r pauseRequest) (any, error) {
			if err := rn.Pause(ctx, r.Actor, r.Paused); err != nil {
				return nil, err
			}
			return s.status(ctx)
		})
	}
	return s
}

func (s *Server) status(ctx context.Context) (Status, error) {
	o := s.o
	counts, err := o.Store.CountByStatus(ctx)
	backupTS, backupErr := backup.Last(o.Paths)
	runnerState, paused := "off", false
	if o.Runner != nil {
		runnerState, paused = o.Runner.State(), o.Runner.Paused()
	}
	var tk TickerStatus
	if info, ok := ticker.Running(o.Paths); ok {
		tk = TickerStatus{Running: true, PID: info.PID, StartedTS: &info.StartedTS}
	}
	return Status{
		Version:      version.Version,
		Ticker:       tk,
		RunnerOn:     o.Config.Runner.Enabled,
		RunnerState:  runnerState,
		RunnerPaused: paused,
		RunnerCap:    o.Config.Runner.Cap,
		Tasks:        counts,
		BackupTS:     backupTS,
		BackupError:  backupErr,
	}, err
}

func (s *Server) appendEvent(ctx context.Context, r AppendRequest) (any, error) {
	if !r.valid() {
		return nil, badRequest{fmt.Errorf("an append of kind %q must set exactly the field its kind names", r.Kind)}
	}
	st := s.o.Store
	switch r.Kind {
	case model.KindNote:
		return st.Note(ctx, r.Actor, *r.Note)
	case model.KindDecision:
		return st.Decide(ctx, r.Actor, *r.Decision)
	case model.KindMerged:
		return st.Merged(ctx, r.Actor, *r.Merged)
	case model.KindCompacted:
		return st.Compacted(ctx, r.Actor)
	default:
		return st.Continues(ctx, r.Actor, r.From)
	}
}

// RoundTrip answers one request and returns its RPCResponse, encoded. It fails only when the answer cannot be
// encoded.
func (s *Server) RoundTrip(ctx context.Context, method string, params []byte) ([]byte, error) {
	return json.Marshal(s.answer(ctx, method, params))
}

// Close closes nothing: the server's store belongs to its caller.
func (s *Server) Close() error { return nil }

func (s *Server) answer(ctx context.Context, method string, params []byte) RPCResponse {
	m, ok := s.methods[method]
	if !ok {
		return RPCResponse{Error: &RPCError{BadRequest: true, Message: fmt.Sprintf("unknown method %q", method)}}
	}
	res, err := m(ctx, params)
	if err != nil {
		var bad badRequest
		if ref, ok := model.AsRefusal(err); ok {
			return RPCResponse{Refusal: ref}
		} else if errors.As(err, &bad) {
			return RPCResponse{Error: &RPCError{BadRequest: true, Message: bad.Error()}}
		}
		return RPCResponse{Error: &RPCError{Message: fmt.Sprintf("%s: %v", method, err)}}
	}
	b, err := json.Marshal(res)
	if err != nil {
		return RPCResponse{Error: &RPCError{Message: fmt.Sprintf("%s: encode the result: %v", method, err)}}
	}
	return RPCResponse{Result: b}
}
