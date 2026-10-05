package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/backup"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// defaultTimeout bounds one request on the command transport.
const defaultTimeout = 15 * time.Second

// ClientOptions configures a Client.
type ClientOptions struct {
	Paths   config.Paths
	Config  config.Config
	Timeout time.Duration // 0 → 15 s per request on the command transport
	// Transport carries the requests; nil → NewCommandTransport on a client, NewLocalTransport on a home.
	Transport Transport
	// Refused is called once for each queued entry the home refuses for good; nil → the entry is dropped silently.
	Refused func(kind model.Kind, r *model.Refusal)
	// Unreachable is called with the error each time a write is queued because the home did not answer
	// (home-unreachable); nil → not reported. Append's results are unchanged: (Event{}, true, nil).
	Unreachable func(err error)
}

// Client calls the home through its Transport. It owns the snapshot and the outbox, so a caller sees the answer, a
// refusal, or home-unreachable. Once the home has not answered, every later request of this Client gets the same
// refusal at once, so a command that makes several requests waits for one connect timeout, not one per request.
type Client struct {
	o  ClientOptions
	tr Transport

	mu   sync.Mutex
	down error // the home-unreachable refusal, once the home did not answer
}

// NewClient returns a client for the machine o describes.
func NewClient(o ClientOptions) *Client {
	if o.Timeout == 0 {
		o.Timeout = defaultTimeout
	}
	tr := o.Transport
	switch {
	case tr != nil:
	case o.Config.IsClient():
		tr = NewCommandTransport(o.Paths, o.Config, o.Timeout)
	default:
		tr = NewLocalTransport(o.Paths, o.Config)
	}
	return &Client{o: o, tr: tr}
}

// Close closes the client's transport: on a home, the store it opened.
func (c *Client) Close() error { return c.tr.Close() }

// Retry forgets that the home did not answer, so the next request tries the transport again. A caller that lives
// past one command, as the board does, calls it once per refresh; within one command the sticky refusal stands.
func (c *Client) Retry() {
	c.mu.Lock()
	c.down = nil
	c.mu.Unlock()
}

func isUnreachable(err error) bool {
	r, ok := model.AsRefusal(err)
	return ok && r.Code == model.CodeHomeUnreachable
}

// call forwards the outbox, then sends one request. When the outbox finds the home unreachable, so does the call;
// an outbox that cannot be forwarded for another reason ends the call with that reason.
func (c *Client) call(ctx context.Context, method string, req, res any) error {
	if _, err := c.Flush(ctx); isUnreachable(err) {
		return err
	} else if err != nil {
		return fmt.Errorf("the queued entries in %s could not be forwarded: %w", c.o.Paths.Outbox(), err)
	}
	return c.send(ctx, method, req, res)
}

// send makes one request and decodes its answer into res: a refusal is a *model.Refusal, an error a *RPCError.
func (c *Client) send(ctx context.Context, method string, req, res any) error {
	params, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.mu.Lock()
	down := c.down
	c.mu.Unlock()
	if down != nil {
		return down
	}
	b, err := c.tr.RoundTrip(ctx, method, params)
	if isUnreachable(err) {
		c.mu.Lock()
		c.down = err
		c.mu.Unlock()
	}
	if err != nil {
		return err
	}
	var resp RPCResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		return fmt.Errorf("the home's answer to %s does not parse: %w", method, err)
	}
	switch {
	case resp.Refusal != nil:
		return resp.Refusal
	case resp.Error != nil:
		return resp.Error
	case res == nil:
		return nil
	}
	return json.Unmarshal(resp.Result, res)
}

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

// Append sends one journal event. queued=true means the home did not answer, and the event is in the outbox.
func (c *Client) Append(ctx context.Context, r AppendRequest) (ev model.Event, queued bool, err error) {
	if !r.valid() {
		return model.Event{}, false, fmt.Errorf("an append of kind %q must set exactly the field its kind names", r.Kind)
	}
	err = c.call(ctx, MethodEventsAppend, r, &ev)
	if !isUnreachable(err) {
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

// ListRuns returns the runner's runs. It never reconciles.
func (c *Client) ListRuns(ctx context.Context) ([]model.Run, error) {
	var runs []model.Run
	err := c.call(ctx, MethodRunsList, runsRequest{}, &runs)
	return runs, err
}

// ReconcileRuns has the home check its live runs against herdr once, then returns the runner's runs.
func (c *Client) ReconcileRuns(ctx context.Context) ([]model.Run, error) {
	var runs []model.Run
	err := c.call(ctx, MethodRunsList, runsRequest{Reconcile: true}, &runs)
	return runs, err
}

// Status asks for the home's status without forwarding the outbox first.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.send(ctx, MethodStatus, empty{}, &s)
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

// StartRun starts a run of the task; a field of route left empty is not given.
func (c *Client) StartRun(ctx context.Context, a store.Actor, task int, route store.RunRoute) (model.Run, error) {
	var r model.Run
	err := c.call(ctx, MethodRunsStart, startRequest{Actor: a, Task: task, Route: route}, &r)
	return r, err
}

// Changes returns the events after the coordinator's cursor, at most 100 of them, oldest first; it moves the cursor
// only when a is the recorded coordinator's session.
func (c *Client) Changes(ctx context.Context, a store.Actor) (store.Changes, error) {
	var ch store.Changes
	err := c.call(ctx, MethodCoordinatorChanges, changesRequest{Actor: a}, &ch)
	return ch, err
}

// PauseRunner pauses or resumes the runner and returns the status after.
func (c *Client) PauseRunner(ctx context.Context, a store.Actor, paused bool) (Status, error) {
	var s Status
	err := c.call(ctx, MethodRunnerPause, pauseRequest{Actor: a, Paused: paused}, &s)
	return s, err
}
