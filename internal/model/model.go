// Package model holds the types desk passes between its store, API, and commands. It does no I/O.
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
	WhoUser  Who = "user"
	WhoAgent Who = "agent"
)

// Step is one checklist item of a task.
type Step struct {
	ShortID string `json:"short_id"`
	Text    string `json:"text"`
	Done    bool   `json:"done"`
}

// Task is a task's current state.
type Task struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Notes     string    `json:"notes"`
	Status    Status    `json:"status"`
	Project   string    `json:"project"`
	Thread    string    `json:"thread"`
	Archived  bool      `json:"archived"`
	Root      string    `json:"root"`
	Isolation string    `json:"isolation"`
	Model     string    `json:"model"`
	CreatedTS time.Time `json:"created_ts"`
	UpdatedTS time.Time `json:"updated_ts"`
	Steps     []Step    `json:"steps"`
}

// Run is one runner attempt at a task.
type Run struct {
	ID        int64     `json:"id"`
	Task      int       `json:"task"`
	State     string    `json:"state"`
	Root      string    `json:"root"`
	Isolation string    `json:"isolation"`
	Model     string    `json:"model"`
	Reason    string    `json:"reason"`
	Session   string    `json:"session"`
	Workspace string    `json:"workspace"`
	Pane      string    `json:"pane"`
	StartedTS time.Time `json:"started_ts"`
	EndedTS   time.Time `json:"ended_ts"`
}

// The run states. A run in the first three is live: it holds one of the runner's slots.
const (
	RunRouting = "routing" // the router is choosing where the task runs
	RunWaiting = "waiting" // routed; its root already has a live in-place run
	RunRunning = "running" // a pane was started for it
	RunEnded   = "ended"   // its task left started
	RunFailed  = "failed"  // the router or the spawn failed
	RunKilled  = "killed"  // killed by a person or by the time limit
)

// RunLive reports whether state is routing, waiting, or running.
func RunLive(state string) bool {
	return state == RunRouting || state == RunWaiting || state == RunRunning
}

// The tags the runner writes.
const (
	TagRunner = "runner" // on every note the runner writes
	TagRouter = "router" // also on the note that records a route or a router failure
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
