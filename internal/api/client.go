package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/federbenjamin/desk/internal/backup"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

// ClientOptions configures a Client.
type ClientOptions struct {
	Paths   config.Paths
	Config  config.Config
	Timeout time.Duration            // 0 → 5s
	Spawn   func(config.Paths) error // starts the daemon on a home; nil → never
	// Refused is called once for each queued entry the home refuses for good; nil → the entry is dropped silently.
	Refused func(kind model.Kind, r *model.Refusal)
	// Token, when set, is used in place of the token file; `client add` checks a home with it before saving it.
	Token string
	// Unreachable is called with the error each time a write is queued because the home could not take it now:
	// it did not answer (home-unreachable) or refused the token (bad-token); nil → not reported. Append's
	// results are unchanged: (Event{}, true, nil).
	Unreachable func(err error)
}

// Client calls the home: through the unix socket on the home itself, through TCP with the token on a client.
// It owns the snapshot and the outbox, so a caller sees the answer, a refusal, or home-unreachable.
type Client struct {
	o    ClientOptions
	http *http.Client
	long *http.Client // the same transport with no timeout, for a backup, which runs git against a remote
	base string
}

// NewClient returns a client for the machine o describes.
func NewClient(o ClientOptions) *Client {
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Second
	}
	c := &Client{o: o}
	// The token goes to the home only: never through a proxy the environment names.
	tr := &http.Transport{Proxy: nil}
	if o.Config.IsClient() {
		c.base = "http://" + o.Config.Client.Home
	} else {
		sock := o.Paths.Socket()
		c.base = "http://desk"
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
	}
	c.http = &http.Client{Timeout: o.Timeout, Transport: tr}
	c.long = &http.Client{Transport: tr}
	return c
}

// where names the home for a message.
func (c *Client) where() string {
	if c.o.Config.IsClient() {
		return "the home at " + c.o.Config.Client.Home
	}
	return "the desk daemon"
}

func unreachable(where string, err error) error {
	return &model.Refusal{Code: model.CodeHomeUnreachable, Msg: fmt.Sprintf("%s did not answer: %v", where, err)}
}

func isUnreachable(err error) bool {
	r, ok := model.AsRefusal(err)
	return ok && r.Code == model.CodeHomeUnreachable
}

// badToken is the home's 401. The fault is the client's token, never the entry, so a journal write the home
// answers this way is queued like one it did not answer.
func (c *Client) badToken() error {
	return &model.Refusal{Code: model.CodeBadToken, Msg: fmt.Sprintf("%s refused the token (HTTP 401); run `desk client add` with the home's current token", c.where())}
}

// cannotTake reports an error that says the home cannot take a write now, for a cause a later call may change
// without touching the entry: it did not answer, or it refused the token.
func cannotTake(err error) bool {
	r, ok := model.AsRefusal(err)
	return ok && (r.Code == model.CodeHomeUnreachable || r.Code == model.CodeBadToken)
}

// call forwards the outbox, then sends one request. When the outbox finds the home unreachable or refusing the
// token, so does the call; an outbox that cannot be forwarded for another reason ends the call with that reason.
func (c *Client) call(ctx context.Context, method string, req, res any) error {
	if _, err := c.Flush(ctx); cannotTake(err) {
		return err
	} else if err != nil {
		return fmt.Errorf("the queued entries in %s could not be forwarded: %w", c.o.Paths.Outbox(), err)
	}
	return c.send(ctx, method, req, res, true)
}

// send makes one request. On a home whose socket refuses the dial it starts the daemon once, when spawn and
// Spawn allow, and tries again.
func (c *Client) send(ctx context.Context, method string, req, res any, spawn bool) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	err = c.post(ctx, method, body, res)
	var opErr *net.OpError
	if spawn && c.o.Spawn != nil && !c.o.Config.IsClient() && errors.As(err, &opErr) && opErr.Op == "dial" {
		if serr := c.o.Spawn(c.o.Paths); serr != nil {
			return unreachable(c.where(), serr)
		}
		err = c.post(ctx, method, body, res)
	}
	var te transportError
	if errors.As(err, &te) {
		return unreachable(c.where(), te.err)
	}
	return err
}

// transportError is a request that got no HTTP answer.
type transportError struct{ err error }

func (t transportError) Error() string { return t.err.Error() }
func (t transportError) Unwrap() error { return t.err }

func (c *Client) post(ctx context.Context, method string, body []byte, res any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.o.Config.IsClient() {
		token := c.o.Token
		if token == "" {
			if token, err = config.ReadToken(c.o.Paths); err != nil {
				return fmt.Errorf("read the token: %w", err)
			}
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	hc := c.http
	if method == MethodBackupRun {
		hc = c.long
	}
	resp, err := hc.Do(req)
	if err != nil {
		return transportError{err}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return transportError{err}
	}
	if resp.StatusCode == http.StatusOK {
		if res == nil {
			return nil
		}
		return json.Unmarshal(b, res)
	}
	var eb errorBody
	_ = json.Unmarshal(b, &eb)
	switch {
	case resp.StatusCode == http.StatusConflict && eb.Code != "":
		return &model.Refusal{Code: eb.Code, Msg: eb.Message}
	case resp.StatusCode == http.StatusUnauthorized:
		return c.badToken()
	default:
		return &httpError{resp.StatusCode, fmt.Sprintf("%s answered HTTP %d: %s", c.where(), resp.StatusCode, eb.Message)}
	}
}

// httpError is an answer that is neither 200 nor a refusal.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

// ListTasks keeps the snapshot whole: for a filter with f.Live() it asks the home for the zero Filter, writes
// the snapshot, and returns the tasks f matches. With the home unreachable it answers such a filter from the
// snapshot, marked Offline. Any other filter is sent as it is and is never answered offline.
func (c *Client) ListTasks(ctx context.Context, f store.Filter) (TaskList, error) {
	if !f.Live() {
		var tl TaskList
		err := c.call(ctx, MethodTasksList, f, &tl)
		return tl, err
	}
	var whole TaskList
	err := c.call(ctx, MethodTasksList, store.Filter{}, &whole)
	if isUnreachable(err) {
		snap, serr := c.readSnapshot()
		if serr != nil {
			return TaskList{}, err
		}
		return TaskList{Tasks: match(f, snap.Tasks), Offline: true, SnapshotTS: &snap.TS}, nil
	}
	if err != nil {
		return TaskList{}, err
	}
	if err := c.writeSnapshot(whole.Tasks); err != nil {
		return TaskList{}, err
	}
	return TaskList{Tasks: match(f, whole.Tasks)}, nil
}

func match(f store.Filter, tasks []model.Task) []model.Task {
	out := []model.Task{}
	for _, t := range tasks {
		if f.Match(t) {
			out = append(out, t)
		}
	}
	return out
}

// GetTask returns one task with its history.
func (c *Client) GetTask(ctx context.Context, number int) (store.TaskDetail, error) {
	var d store.TaskDetail
	err := c.call(ctx, MethodTasksGet, getRequest{Number: number}, &d)
	return d, err
}

// AddTask creates a task.
func (c *Client) AddTask(ctx context.Context, a store.Actor, in store.AddTaskInput) (model.Task, error) {
	var t model.Task
	err := c.call(ctx, MethodTasksAdd, addRequest{Actor: a, Input: in}, &t)
	return t, err
}

// SetTask patches a task.
func (c *Client) SetTask(ctx context.Context, a store.Actor, number int, p model.Patch) (model.Task, error) {
	var t model.Task
	err := c.call(ctx, MethodTasksSet, setRequest{Actor: a, Number: number, Patch: p}, &t)
	return t, err
}

// Step changes a task's steps.
func (c *Client) Step(ctx context.Context, a store.Actor, number int, op model.StepOp) (model.Task, error) {
	var t model.Task
	err := c.call(ctx, MethodTasksSteps, stepsRequest{Actor: a, Number: number, Op: op}, &t)
	return t, err
}

// Append sends one journal event. queued=true means the home did not answer or refused the token, and the event
// is in the outbox.
func (c *Client) Append(ctx context.Context, r AppendRequest) (ev model.Event, queued bool, err error) {
	if !r.valid() {
		return model.Event{}, false, fmt.Errorf("an append of kind %q must set exactly the field its kind names", r.Kind)
	}
	err = c.call(ctx, MethodEventsAppend, r, &ev)
	if !cannotTake(err) {
		return ev, false, err
	}
	if qerr := c.enqueue(r); qerr != nil {
		return model.Event{}, false, errors.Join(err, qerr)
	}
	if c.o.Unreachable != nil {
		c.o.Unreachable(err)
	}
	return model.Event{}, true, nil
}

// SessionView returns a session's events and tasks.
func (c *Client) SessionView(ctx context.Context, session string) (model.SessionData, error) {
	var d model.SessionData
	err := c.call(ctx, MethodSessionView, sessionRequest{Session: session}, &d)
	return d, err
}

// ListRuns returns the runner's runs.
func (c *Client) ListRuns(ctx context.Context) ([]model.Run, error) {
	var runs []model.Run
	err := c.call(ctx, MethodRunsList, empty{}, &runs)
	return runs, err
}

// Status never starts the daemon.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.send(ctx, MethodStatus, empty{}, &s, false)
	return s, err
}

// Backup runs the backup on the home now.
func (c *Client) Backup(ctx context.Context) (backup.Result, error) {
	var r backup.Result
	err := c.call(ctx, MethodBackupRun, empty{}, &r)
	return r, err
}

// KillRun stops the task's live run and blocks the task.
func (c *Client) KillRun(ctx context.Context, a store.Actor, task int) (model.Task, error) {
	var t model.Task
	err := c.call(ctx, MethodRunsKill, killRequest{Actor: a, Task: task}, &t)
	return t, err
}

// PauseRunner pauses or resumes the runner and returns the status after.
func (c *Client) PauseRunner(ctx context.Context, a store.Actor, paused bool) (Status, error) {
	var s Status
	err := c.call(ctx, MethodRunnerPause, pauseRequest{Actor: a, Paused: paused}, &s)
	return s, err
}
