package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/federbenjamin/desk/internal/backup"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/version"
)

// ServerOptions configures a Server.
type ServerOptions struct {
	Store     *store.Store
	Config    config.Config
	Paths     config.Paths // the token is read with config.ReadToken on each TCP request, so a rotation needs no restart
	StartedTS time.Time
	Backup    func(ctx context.Context) (backup.Result, error) // nil → backup.run refuses backup-off
}

// Server answers the API methods from one store.
type Server struct {
	o       ServerOptions
	methods map[string]method
}

// method decodes a request body and calls the store.
type method func(ctx context.Context, body []byte) (any, error)

// badRequest is an error the server answers with 400.
type badRequest struct{ err error }

func (b badRequest) Error() string { return b.err.Error() }

// shown is a failure whose text the caller reads in the 500's body: a backup's, which names the git step,
// served on the unix socket only, and stripped of the remote by backup.Run.
type shown struct{ err error }

func (s shown) Error() string { return s.err.Error() }
func (s shown) Unwrap() error { return s.err }

// bind is the one decode-and-call adapter every method goes through.
func bind[Req any](fn func(ctx context.Context, r Req) (any, error)) method {
	return func(ctx context.Context, body []byte) (any, error) {
		var r Req
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, badRequest{err}
		}
		return fn(ctx, r)
	}
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
			return st.SetTask(ctx, r.Actor, r.Number, r.Patch)
		}),
		MethodTasksSteps: bind(func(ctx context.Context, r stepsRequest) (any, error) {
			return st.Step(ctx, r.Actor, r.Number, r.Op)
		}),
		MethodEventsAppend: bind(s.appendEvent),
		MethodSessionView: bind(func(ctx context.Context, r sessionRequest) (any, error) {
			return st.SessionEvents(ctx, r.Session)
		}),
		MethodRunsList: bind(func(ctx context.Context, _ empty) (any, error) {
			runs, err := st.ListRuns(ctx)
			if runs == nil {
				runs = []model.Run{}
			}
			return runs, err
		}),
		MethodStatus: bind(func(ctx context.Context, _ empty) (any, error) {
			counts, err := st.CountByStatus(ctx)
			return Status{
				Version:   version.Version,
				Listen:    o.Config.Home.Listen,
				StartedTS: o.StartedTS,
				RunnerOn:  o.Config.Runner.Enabled,
				Tasks:     counts,
			}, err
		}),
		MethodBackupRun: bind(func(ctx context.Context, _ empty) (any, error) {
			if o.Backup == nil {
				return nil, &model.Refusal{Code: model.CodeBackupOff, Msg: "no [backup] git_remote is configured"}
			}
			// A push the caller stops waiting for still finishes, so the remote never holds half a run.
			res, err := o.Backup(context.WithoutCancel(ctx))
			if err != nil {
				return nil, shown{err}
			}
			return res, nil
		}),
	}
	return s
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

// Handler serves the API. trusted=true is the unix socket: no token. trusted=false requires the bearer token
// and does not serve backup.run.
func (s *Server) Handler(trusted bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "", "use POST")
			return
		}
		if !trusted && !s.tokenOK(r) {
			writeError(w, http.StatusUnauthorized, "", "a missing or wrong token")
			return
		}
		name, _ := strings.CutPrefix(r.URL.Path, "/v1/")
		m, ok := s.methods[name]
		if !ok || (!trusted && name == MethodBackupRun) {
			writeError(w, http.StatusBadRequest, "", "unknown method "+r.URL.Path)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
				writeError(w, http.StatusRequestEntityTooLarge, "", fmt.Sprintf("the body is over %d bytes", maxBody))
				return
			}
			writeError(w, http.StatusBadRequest, "", err.Error())
			return
		}
		res, err := m(r.Context(), body)
		if err != nil {
			var bad badRequest
			var sh shown
			switch ref, isRef := model.AsRefusal(err); {
			case isRef:
				writeError(w, http.StatusConflict, ref.Code, ref.Msg)
			case errors.As(err, &bad):
				writeError(w, http.StatusBadRequest, "", bad.Error())
			default:
				log.Printf("desk daemon: %s: %v", name, err)
				msg := "an internal error; the daemon log has it"
				if errors.As(err, &sh) {
					msg = sh.Error()
				}
				writeError(w, http.StatusInternalServerError, "", msg)
			}
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
}

// tokenOK reads the token file on every request, so a rotation takes effect without a restart.
func (s *Server) tokenOK(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || got == "" {
		return false
	}
	want, err := config.ReadToken(s.o.Paths)
	if err != nil {
		log.Printf("desk daemon: the token cannot be read, so every TCP request gets 401: %v", err)
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeError(w http.ResponseWriter, code int, refusal, msg string) {
	writeJSON(w, code, errorBody{Code: refusal, Message: msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
