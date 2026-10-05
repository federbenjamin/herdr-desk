package board_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/board"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// press is a key that types r.
func press(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// named is a key with a name and no text, such as tea.KeyEnter.
func named(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

// ctrl is r with the control key held.
func ctrl(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

func wantEffects(t *testing.T, got, want []board.Effect) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v, want %#v", got, want)
	}
}

type addReply struct {
	task model.Task
	err  error
}

// fakeHome is the one Home of the board's tests. With only set, a call to a method not in it records the method
// in unscripted and fails, so a test can prove which calls a flow makes.
type fakeHome struct {
	mu sync.Mutex

	only       map[string]bool
	unscripted []string
	addReplies []addReply              // answered in order before add is
	addGate    chan struct{}           // AddTask waits for it to close before it answers
	listHook   func(call int) error    // runs outside the lock before ListTasks call number call answers
	setRefuse  func(model.Patch) error // SetTask answers its error when it returns one
	setHook    func(model.Patch) error // runs outside the lock once SetTask records its patch; its error is the answer

	liveLists, maxLiveLists int // ListTasks calls for the live tasks in flight now, and the most at once

	tasks      []model.Task
	runs       []model.Run
	detail     store.TaskDetail
	status     api.Status
	offline    bool
	snapshotTS *time.Time
	list       error
	get        error
	add        error
	set        error
	step       error
	append     error
	run        error
	state      error
	kill       error
	pause      error
	queued     bool

	listCalls    int
	listFilters  []store.Filter
	getTaskCalls int
	statusCalls  int
	runsCalls    int
	setActors    []store.Actor
	setPatches   []model.Patch
	addActors    []store.Actor
	addInputs    []store.AddTaskInput
	stepActors   []store.Actor
	stepOps      []model.StepOp
	appendReqs   []api.AppendRequest
	killActors   []store.Actor
	killTasks    []int
	pauseActors  []store.Actor
	pauseValues  []bool
}

// script records a call to a method outside only and refuses it. The caller holds mu.
func (h *fakeHome) script(method string) error {
	if h.only == nil || h.only[method] {
		return nil
	}
	h.unscripted = append(h.unscripted, method)
	return fmt.Errorf("unscripted call: %s", method)
}

func (h *fakeHome) mostLiveLists() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.maxLiveLists
}

func (h *fakeHome) unscriptedCalls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.unscripted...)
}

func (h *fakeHome) ListTasks(_ context.Context, f store.Filter) (api.TaskList, error) {
	h.mu.Lock()
	if err := h.script("ListTasks"); err != nil {
		h.mu.Unlock()
		return api.TaskList{}, err
	}
	h.listCalls++
	h.listFilters = append(h.listFilters, f)
	call, hook, live := h.listCalls, h.listHook, len(f.Statuses) == 0
	if live {
		h.liveLists++
		h.maxLiveLists = max(h.maxLiveLists, h.liveLists)
	}
	h.mu.Unlock()
	var hookErr error
	if hook != nil {
		hookErr = hook(call)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if live {
		h.liveLists--
	}
	if hookErr != nil {
		return api.TaskList{}, hookErr
	}
	if h.list != nil {
		return api.TaskList{}, h.list
	}
	return api.TaskList{Tasks: append([]model.Task(nil), h.tasks...), Offline: h.offline, SnapshotTS: h.snapshotTS}, nil
}

func (h *fakeHome) GetTask(_ context.Context, _ int) (store.TaskDetail, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.script("GetTask"); err != nil {
		return store.TaskDetail{}, err
	}
	h.getTaskCalls++
	if h.get != nil {
		return store.TaskDetail{}, h.get
	}
	return h.detail, nil
}

func (h *fakeHome) AddTask(_ context.Context, a store.Actor, in store.AddTaskInput) (model.Task, error) {
	h.mu.Lock()
	if err := h.script("AddTask"); err != nil {
		h.mu.Unlock()
		return model.Task{}, err
	}
	h.addActors = append(h.addActors, a)
	h.addInputs = append(h.addInputs, in)
	gate := h.addGate
	h.mu.Unlock()
	if gate != nil {
		<-gate
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.addReplies) > 0 {
		r := h.addReplies[0]
		h.addReplies = h.addReplies[1:]
		return r.task, r.err
	}
	if h.add != nil {
		return model.Task{}, h.add
	}
	return model.Task{Number: 1}, nil
}

func (h *fakeHome) SetTask(_ context.Context, a store.Actor, number int, p model.Patch) (model.Task, error) {
	h.mu.Lock()
	if err := h.script("SetTask"); err != nil {
		h.mu.Unlock()
		return model.Task{}, err
	}
	h.setActors = append(h.setActors, a)
	h.setPatches = append(h.setPatches, p)
	hook := h.setHook
	h.mu.Unlock()
	if hook != nil {
		if err := hook(p); err != nil {
			return model.Task{}, err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.set != nil {
		return model.Task{}, h.set
	}
	if h.setRefuse != nil {
		if err := h.setRefuse(p); err != nil {
			return model.Task{}, err
		}
	}
	for _, task := range h.tasks {
		if task.Number == number {
			return task, nil
		}
	}
	return model.Task{Number: number}, nil
}

func (h *fakeHome) Step(_ context.Context, a store.Actor, number int, op model.StepOp) (model.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.script("Step"); err != nil {
		return model.Task{}, err
	}
	h.stepActors = append(h.stepActors, a)
	h.stepOps = append(h.stepOps, op)
	if h.step != nil {
		return model.Task{}, h.step
	}
	return model.Task{Number: number}, nil
}

func (h *fakeHome) Append(_ context.Context, r api.AppendRequest) (model.Event, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.script("Append"); err != nil {
		return model.Event{}, false, err
	}
	h.appendReqs = append(h.appendReqs, r)
	if h.append != nil {
		return model.Event{}, false, h.append
	}
	return model.Event{}, h.queued, nil
}

func (h *fakeHome) ListRuns(context.Context) ([]model.Run, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.script("ListRuns"); err != nil {
		return nil, err
	}
	h.runsCalls++
	if h.run != nil {
		return nil, h.run
	}
	return append([]model.Run(nil), h.runs...), nil
}

func (h *fakeHome) Status(context.Context) (api.Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.script("Status"); err != nil {
		return api.Status{}, err
	}
	h.statusCalls++
	if h.state != nil {
		return api.Status{}, h.state
	}
	return h.status, nil
}

func (h *fakeHome) KillRun(_ context.Context, a store.Actor, task int) (model.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.script("KillRun"); err != nil {
		return model.Task{}, err
	}
	h.killActors = append(h.killActors, a)
	h.killTasks = append(h.killTasks, task)
	if h.kill != nil {
		return model.Task{}, h.kill
	}
	return model.Task{Number: task}, nil
}

func (h *fakeHome) StartRun(_ context.Context, _ store.Actor, task int, _ store.RunRoute) (model.Run, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.script("StartRun"); err != nil {
		return model.Run{}, err
	}
	return model.Run{Task: task}, nil
}

func (h *fakeHome) PauseRunner(_ context.Context, a store.Actor, paused bool) (api.Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.script("PauseRunner"); err != nil {
		return api.Status{}, err
	}
	h.pauseActors = append(h.pauseActors, a)
	h.pauseValues = append(h.pauseValues, paused)
	if h.pause != nil {
		return api.Status{}, h.pause
	}
	return api.Status{}, nil
}

func (h *fakeHome) w5ListCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.listCalls
}

func (h *fakeHome) w5GetTaskCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.getTaskCalls
}

func (h *fakeHome) w5OnlineCalls() (status, runs int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.statusCalls, h.runsCalls
}

func (h *fakeHome) w5SetActors() []store.Actor {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Actor(nil), h.setActors...)
}

func (h *fakeHome) w5SetPatches() []model.Patch {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]model.Patch(nil), h.setPatches...)
}

func (h *fakeHome) w5ListFilters() []store.Filter {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Filter(nil), h.listFilters...)
}

func (h *fakeHome) w5Add() ([]store.Actor, []store.AddTaskInput) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Actor(nil), h.addActors...), append([]store.AddTaskInput(nil), h.addInputs...)
}

func (h *fakeHome) w5Step() ([]store.Actor, []model.StepOp) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Actor(nil), h.stepActors...), append([]model.StepOp(nil), h.stepOps...)
}

func (h *fakeHome) w5Append() []api.AppendRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]api.AppendRequest(nil), h.appendReqs...)
}

func (h *fakeHome) w5KillPause() ([]store.Actor, []int, []store.Actor, []bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]store.Actor(nil), h.killActors...), append([]int(nil), h.killTasks...), append([]store.Actor(nil), h.pauseActors...), append([]bool(nil), h.pauseValues...)
}

type lockedOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *lockedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p)
}

func (o *lockedOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func (o *lockedOutput) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Len()
}

func eventually(t *testing.T, description string, ok func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if ok() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", description)
		case <-tick.C:
		}
	}
}
