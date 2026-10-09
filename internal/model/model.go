// Package model holds the types herdr-desk passes between its store, API, and commands. It does no I/O.
package model

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

// Status is a task's state.
type Status string

// The six task statuses.
const (
	StatusOpen    Status = "open"
	StatusReady   Status = "ready"
	StatusStarted Status = "started"
	StatusBlocked Status = "blocked"
	StatusReview  Status = "review"
	StatusDone    Status = "done"
)

// ParseStatus returns the status s names and whether it names one.
func ParseStatus(s string) (Status, bool) {
	switch st := Status(s); st {
	case StatusOpen, StatusReady, StatusStarted, StatusBlocked, StatusReview, StatusDone:
		return st, true
	}
	return "", false
}

// Who is the kind of writer behind an event.
type Who string

// The two writers.
const (
	WhoUser   Who = "user"
	WhoAgent  Who = "agent"
	WhoRunner Who = "runner" // the runner's own writes: a hand-back, a spawn's note, a ticker job's note
)

// Step is one checklist item of a task.
type Step struct {
	ShortID string `json:"short_id"`
	Text    string `json:"text"`
	Done    bool   `json:"done"`
}

// Task is a task's current state.
type Task struct {
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	Notes        string    `json:"notes"`
	Status       Status    `json:"status"`
	Project      string    `json:"project"`
	Thread       string    `json:"thread"`
	Archived     bool      `json:"archived"`
	Root         string    `json:"root"`
	Isolation    string    `json:"isolation"`
	Model        string    `json:"model"`
	FirstMessage string    `json:"first_message"`
	CreatedTS    time.Time `json:"created_ts"`
	UpdatedTS    time.Time `json:"updated_ts"`
	Steps        []Step    `json:"steps"`
}

// Run is one runner attempt at a task.
type Run struct {
	ID           int64     `json:"id"`
	Task         int       `json:"task"`
	State        string    `json:"state"`
	Root         string    `json:"root"`
	Isolation    string    `json:"isolation"`
	Model        string    `json:"model"`
	FirstMessage string    `json:"first_message"`
	Reason       string    `json:"reason"`
	Session      string    `json:"session"`
	Workspace    string    `json:"workspace"`
	Pane         string    `json:"pane"`
	StartedTS    time.Time `json:"started_ts"`
	EndedTS      time.Time `json:"ended_ts"`
	LeftOpen     bool      `json:"left_open"` // its pane is owed a close (a kill or a spawn could not close it, or a start ended the run idle); the ticker closes it again
}

// The run states. A run in the first four is live. A starting or running run takes one of the runner's cap slots;
// a starting, running, or idle in-place run holds its root.
const (
	RunStarting = "starting" // its pane is being opened
	RunWaiting  = "waiting"  // it cannot start now: its in-place root is busy or cap runs are live
	RunRunning  = "running"  // a pane was started for it
	RunIdle     = "idle"     // its agent stopped without handing back
	RunEnded    = "ended"    // its task was handed back or set done
	RunFailed   = "failed"   // the spawn failed or the run was left starting
	RunKilled   = "killed"   // killed by a person
)

// LiveRunStates are the states of a live run: starting, waiting, running, idle.
func LiveRunStates() []string { return []string{RunStarting, RunWaiting, RunRunning, RunIdle} }

// FinalRunStates are the states a run never leaves: ended, failed, killed.
func FinalRunStates() []string { return []string{RunEnded, RunFailed, RunKilled} }

// SlotRunStates are the states of a run that takes one of the runner's cap slots: starting, running.
func SlotRunStates() []string { return []string{RunStarting, RunRunning} }

// RootRunStates are the states in which an in-place run holds its root: starting, running, idle.
func RootRunStates() []string { return []string{RunStarting, RunRunning, RunIdle} }

// RunLive reports whether state is one of LiveRunStates.
func RunLive(state string) bool { return slices.Contains(LiveRunStates(), state) }

// RunFinal reports whether state is one of FinalRunStates.
func RunFinal(state string) bool { return slices.Contains(FinalRunStates(), state) }

// RunTakesSlot reports whether state is one of SlotRunStates.
func RunTakesSlot(state string) bool { return slices.Contains(SlotRunStates(), state) }

// Coordinator is the desk's one coordinator session, its herdr workspace and pane, and the id of the newest event
// its last context showed.
type Coordinator struct {
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Pane      string `json:"pane"`
	Cursor    int64  `json:"cursor"`
}

// The tags the runner writes.
const (
	TagRunner = "runner" // on every note the runner writes
	TagRouter = "router" // on the notes the removed router wrote; a worker's first message still leaves them out
)

// SessionTask is a task created by a session in the chain, with what the journal needs.
type SessionTask struct {
	Task
	Created int64    `json:"created"` // id of its task event
	DoneAt  int64    `json:"done_at"` // id of the event that last set it done; 0 when not done
	Tags    []string `json:"tags"`    // tags of its task event
}

// SessionData is what session.view returns.
type SessionData struct {
	Session string        `json:"session"`
	Chain   []string      `json:"chain"` // the session, then each one it continues
	Events  []Event       `json:"events"`
	Tasks   []SessionTask `json:"tasks"`
}

const branchPrefix = "branch:"

// BranchTag returns "branch:<b>".
func BranchTag(branch string) string { return branchPrefix + branch }

// BranchOf returns the branch a tag list carries, or "".
func BranchOf(tags []string) string {
	for _, t := range tags {
		if b, ok := strings.CutPrefix(t, branchPrefix); ok {
			return b
		}
	}
	return ""
}

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// ValidSessionID reports whether s can name a session: ^[A-Za-z0-9._-]{1,128}$ and neither "." nor "..".
func ValidSessionID(s string) bool {
	return s != "." && s != ".." && sessionIDRe.MatchString(s)
}

var stepIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidStepID reports whether id can name a step a caller picks: ^[A-Za-z0-9._-]{1,64}$.
func ValidStepID(id string) bool { return stepIDRe.MatchString(id) }

var generatedStepIDRe = regexp.MustCompile(`^s[0-9]+$`)

// GeneratedStepID reports whether id has the shape of the ids the store generates, ^s[0-9]+$; a caller's id may not.
func GeneratedStepID(id string) bool { return generatedStepIDRe.MatchString(id) }

// TaskFile is the one placeholder of a first_message, written {task_file}: the path of the file that holds the task.
const TaskFile = "task_file"

// ValidFirstMessage reports whether s can be a first_message: "" (none) or a template holding {task_file}.
// config.Validate, the store, and Resolve all check a first_message with it.
func ValidFirstMessage(s string) bool { return s == "" || strings.Contains(s, "{"+TaskFile+"}") }

var isolations = []string{"", "self", "worktree", "in-place"}

// Isolations returns every isolation in order: "", self, worktree, in-place. The board's I key cycles through it.
func Isolations() []string { return slices.Clone(isolations) }

// ValidIsolation reports whether s is one of Isolations. config.Validate, AddRoot, and the store all check an
// isolation with it; internal/store does not import internal/config.
func ValidIsolation(s string) bool { return slices.Contains(isolations, s) }

// OnMergedStatus maps a runner.on_merged value to its status: "" and "review" give StatusReview, "done"
// gives StatusDone, and any other value gives false. config.Validate and store.Open both parse with it.
func OnMergedStatus(s string) (Status, bool) {
	switch s {
	case "", string(StatusReview):
		return StatusReview, true
	case string(StatusDone):
		return StatusDone, true
	}
	return "", false
}
