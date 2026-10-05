// Package herdrtest gives tests a herdr that lives in memory.
package herdrtest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/federbenjamin/herdr-desk/internal/herdr"
)

// Workspace is one workspace created through the stand-in.
type Workspace struct {
	ID, Pane, Cwd, Label string
	Env                  []string // KEY=VALUE, as given
	Command              string   // what Run was given; "" until then
}

// Herdr has the methods of herdr.Client that runner.Herdr names, so it satisfies it. It is safe for concurrent use. A
// new pane reports status "unknown" and no session. ClosePane removes the pane; Run, Processes, ClosePane, FocusPane,
// and ReportToken on an unknown pane are errors, and Pane reports it not found.
type Herdr struct {
	mu         sync.Mutex
	next       int
	workspaces []Workspace
	order      []string // pane ids in the order Panes lists them
	panes      map[string]herdr.Pane
	procs      map[string]herdr.Processes
	closed     []string
	focused    []string
	tokens     map[string]map[string]string
	fails      map[string]error
}

// NewHerdr returns an empty stand-in.
func NewHerdr() *Herdr {
	return &Herdr{
		panes:  map[string]herdr.Pane{},
		procs:  map[string]herdr.Processes{},
		tokens: map[string]map[string]string{},
		fails:  map[string]error{},
	}
}

// CreateWorkspace records a workspace and its one pane.
func (h *Herdr) CreateWorkspace(_ context.Context, cwd, label string, env []string) (herdr.Created, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fails["CreateWorkspace"]; err != nil {
		return herdr.Created{}, err
	}
	h.next++
	ws := Workspace{
		ID:    fmt.Sprintf("w%d", h.next),
		Pane:  fmt.Sprintf("w%d-1", h.next),
		Cwd:   cwd,
		Label: label,
		Env:   slices.Clone(env),
	}
	h.workspaces = append(h.workspaces, ws)
	h.order = append(h.order, ws.Pane)
	h.panes[ws.Pane] = herdr.Pane{ID: ws.Pane, Workspace: ws.ID, Status: "unknown"}
	return herdr.Created{Workspace: ws.ID, Pane: ws.Pane}, nil
}

// Run records the command typed into the pane.
func (h *Herdr) Run(_ context.Context, pane, command string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fails["Run"]; err != nil {
		return err
	}
	if _, ok := h.panes[pane]; !ok {
		return fmt.Errorf("herdr pane run: unknown pane %q", pane)
	}
	for i := range h.workspaces {
		if h.workspaces[i].Pane == pane {
			h.workspaces[i].Command = command
		}
	}
	return nil
}

// Panes lists the open panes in the order they were created.
func (h *Herdr) Panes(_ context.Context) ([]herdr.Pane, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fails["Panes"]; err != nil {
		return nil, err
	}
	out := make([]herdr.Pane, 0, len(h.order))
	for _, id := range h.order {
		out = append(out, h.panes[id])
	}
	return out, nil
}

// Pane returns the pane as Panes would list it; found is false when it is not open.
func (h *Herdr) Pane(_ context.Context, id string) (herdr.Pane, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fails["Pane"]; err != nil {
		return herdr.Pane{}, false, err
	}
	p, ok := h.panes[id]
	return p, ok, nil
}

// Processes returns what SetProcesses gave for the pane, empty when it gave nothing.
func (h *Herdr) Processes(_ context.Context, pane string) (herdr.Processes, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fails["Processes"]; err != nil {
		return herdr.Processes{}, err
	}
	if _, ok := h.panes[pane]; !ok {
		return herdr.Processes{}, fmt.Errorf("herdr pane process-info: unknown pane %q", pane)
	}
	p := h.procs[pane]
	return herdr.Processes{Group: p.Group, PIDs: slices.Clone(p.PIDs)}, nil
}

// ClosePane removes the pane and records it as closed.
func (h *Herdr) ClosePane(_ context.Context, pane string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fails["ClosePane"]; err != nil {
		return err
	}
	if _, ok := h.panes[pane]; !ok {
		return fmt.Errorf("herdr pane close: unknown pane %q", pane)
	}
	h.drop(pane)
	h.closed = append(h.closed, pane)
	return nil
}

// FocusPane records the pane as focused. It checks the ids as herdr.FocusArgv does; an unknown pane is an error.
func (h *Herdr) FocusPane(_ context.Context, workspace, pane string) error {
	if _, err := herdr.FocusArgv("herdr", workspace, pane); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fails["FocusPane"]; err != nil {
		return err
	}
	if _, ok := h.panes[pane]; !ok {
		return fmt.Errorf("herdr pane zoom: unknown pane %q", pane)
	}
	h.focused = append(h.focused, pane)
	return nil
}

// Focused returns the panes FocusPane was called for, in order.
func (h *Herdr) Focused() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.focused)
}

// ReportToken sets the pane's token name to value, or removes it when value is empty, as herdr's report-metadata does.
func (h *Herdr) ReportToken(_ context.Context, pane, source, name, value string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.fails["ReportToken"]; err != nil {
		return err
	}
	if _, ok := h.panes[pane]; !ok {
		return fmt.Errorf("herdr pane report-metadata: unknown pane %q", pane)
	}
	if source == "" || name == "" {
		return fmt.Errorf("herdr pane report-metadata: source %q or token name %q is empty", source, name)
	}
	if value == "" {
		delete(h.tokens[pane], name)
		return nil
	}
	if h.tokens[pane] == nil {
		h.tokens[pane] = map[string]string{}
	}
	h.tokens[pane][name] = value
	return nil
}

// Tokens returns the tokens last reported for the pane, kept after the pane closes so a test can read what it showed.
func (h *Herdr) Tokens(pane string) map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return maps.Clone(h.tokens[pane])
}

// Workspaces returns every workspace created, in order.
func (h *Herdr) Workspaces() []Workspace {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := slices.Clone(h.workspaces)
	for i := range out {
		out[i].Env = slices.Clone(out[i].Env)
	}
	return out
}

// Set sets what Panes and Pane report for the pane from now on.
func (h *Herdr) Set(pane, session, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.panes[pane]
	if !ok {
		p = herdr.Pane{ID: pane}
		h.order = append(h.order, pane)
	}
	p.Session, p.Status = session, status
	h.panes[pane] = p
}

// Remove makes the pane gone from Panes and Pane.
func (h *Herdr) Remove(pane string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.drop(pane)
}

// SetProcesses sets what Processes reports for the pane.
func (h *Herdr) SetProcesses(pane string, p herdr.Processes) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.procs[pane] = herdr.Processes{Group: p.Group, PIDs: slices.Clone(p.PIDs)}
}

// Closed returns the panes ClosePane closed, in order.
func (h *Herdr) Closed() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.closed)
}

// Fail makes the named method (CreateWorkspace, Run, Panes, Pane, Processes, ClosePane, FocusPane, or ReportToken)
// return err; nil clears it.
func (h *Herdr) Fail(method string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		delete(h.fails, method)
		return
	}
	h.fails[method] = err
}

func (h *Herdr) drop(pane string) {
	delete(h.panes, pane)
	delete(h.procs, pane)
	h.order = slices.DeleteFunc(h.order, func(id string) bool { return id == pane })
}
