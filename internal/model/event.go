package model

import (
	"encoding/json"
	"time"
)

// Kind is an event's kind.
type Kind string

// The event kinds.
const (
	KindTask      Kind = "task"
	KindSet       Kind = "set"
	KindStep      Kind = "step"
	KindNote      Kind = "note"
	KindDecision  Kind = "decision"
	KindMerged    Kind = "merged"
	KindCompacted Kind = "compacted"
	KindContinues Kind = "continues"
)

// Event is one entry of the history. Ids are a total order.
type Event struct {
	ID      int64           `json:"id"`
	TS      time.Time       `json:"ts"`
	Session string          `json:"session"`
	Who     Who             `json:"who"`
	Kind    Kind            `json:"kind"`
	Task    int             `json:"task"` // 0 = no task
	Data    json.RawMessage `json:"data"`
	Tags    []string        `json:"tags"`
	Run     int64           `json:"run"` // 0 = no run
	V       int             `json:"v"`   // 1
}

// Each kind's Event.Data is one of these types, and each type is also what the caller passes in:
// there is one definition per shape.

// TaskData is a task event's payload.
type TaskData struct {
	Title   string `json:"title"`
	Notes   string `json:"notes,omitempty"`
	Project string `json:"project,omitempty"` // an absolute path, stored as given; or the bare name of one known project
	Thread  string `json:"thread,omitempty"`
	Status  Status `json:"status,omitempty"` // "" → open
}

// Patch is a change to a task's fields. As a set event's payload it holds only the fields that changed.
type Patch struct {
	Status       *Status `json:"status,omitempty"`
	Title        *string `json:"title,omitempty"`
	Notes        *string `json:"notes,omitempty"`
	NotesWere    *string `json:"notes_were,omitempty"` // precondition: the notes the writer read; never stored in a set event
	Thread       *string `json:"thread,omitempty"`
	Root         *string `json:"root,omitempty"`
	Isolation    *string `json:"isolation,omitempty"`
	Model        *string `json:"model,omitempty"`
	FirstMessage *string `json:"first_message,omitempty"`
	Archived     *bool   `json:"archived,omitempty"`
	Ref          string  `json:"ref,omitempty"`
	Merged       bool    `json:"merged,omitempty"`
	Question     string  `json:"question,omitempty"` // with status blocked: what the writer waits on; its run stays live; never stored in a set event
}

// WaitingNote is the note of a task blocked on an answer typed in pane: the runner's, for a pane that asks, and the
// store's, for a run that sets its task blocked with a question.
func WaitingNote(pane string) string { return "the worker is waiting for an answer in pane " + pane }

// StepOp is one change to a task's steps, and a step event's payload.
type StepOp struct {
	Op      string `json:"op"`                 // add | toggle | done | rename | remove
	ShortID string `json:"short_id,omitempty"` // add: the caller's id, "" → generated
	Text    string `json:"text,omitempty"`
}

// NoteData is a note event's payload.
type NoteData struct {
	Text string `json:"text"`
	Ref  string `json:"ref,omitempty"`
}

// DecisionData is a decision event's payload.
type DecisionData struct {
	Text     string `json:"text"`
	Replaces int64  `json:"replaces,omitempty"` // id of the decision event it replaces
}

// MergedData is a merged event's payload.
type MergedData struct {
	Branch string `json:"branch"`
	PR     int    `json:"pr,omitempty"`
	SHA    string `json:"sha,omitempty"`
	Text   string `json:"text,omitempty"`
}

// ContinuesData records the link in the event log. Readers follow the chain through the sessions table.
type ContinuesData struct {
	From string `json:"from"` // the session this one continues
}

// MustData marshals a payload for Event.Data; it panics on a marshal error.
func MustData(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
