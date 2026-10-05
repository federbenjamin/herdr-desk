// Package api is herdr-desk's JSON API: one RPCRequest, one RPCResponse. On a home a command answers it in its own
// process against the store; a client sends it through the [client] command, which runs `herdr-desk rpc` on the
// home.
package api

import (
	"encoding/json"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

// The API methods.
const (
	MethodTasksList    = "tasks.list"
	MethodTasksGet     = "tasks.get"
	MethodTasksAdd     = "tasks.add"
	MethodTasksSet     = "tasks.set"
	MethodTasksSteps   = "tasks.steps"
	MethodEventsAppend = "events.append"
	MethodSessionView  = "session.view"
	MethodRunsList     = "runs.list"
	MethodRunsKill     = "runs.kill"
	MethodRunsStart    = "runs.start"
	MethodRunnerPause  = "runner.pause"
	MethodStatus       = "status"
	MethodBackupRun    = "backup.run"
	// MethodCoordinatorChanges returns the events after the coordinator's cursor; only the coordinator's own call
	// moves the cursor. The coordinator is recorded on the home by runner.Coordinator, so no method gets or sets it.
	MethodCoordinatorChanges = "coordinator.changes"
)

// maxChanges is the most events coordinator.changes returns; the rest are counted as left out.
const maxChanges = 100

// The values of Status.RunnerState.
const (
	RunnerStateOff     = "off"
	RunnerStateOn      = "on"
	RunnerStatePaused  = "paused"
	RunnerStateNoHerdr = "no-herdr"
)

// maxBody is the largest request rpc reads.
const maxBody = 1 << 20

// RPCRequest is one call: a method and its params.
type RPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// RPCResponse is one answer. Exactly one field is set: the result, a refusal with its stable code, or an error.
type RPCResponse struct {
	Result  json.RawMessage `json:"result,omitempty"`
	Refusal *model.Refusal  `json:"refusal,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is an answer that is neither a result nor a refusal. BadRequest means the request itself is at fault (it
// does not parse, names no method, or is over 1 MiB), so a retry never helps; else the home failed and a retry may.
type RPCError struct {
	BadRequest bool   `json:"bad_request"`
	Message    string `json:"message"`
}

// Error returns the message.
func (e *RPCError) Error() string { return e.Message }

// AppendRequest carries one journal event. Exactly the field its Kind names is set; any other pairing is a 400.
type AppendRequest struct {
	Actor    store.Actor          `json:"actor"`
	Kind     model.Kind           `json:"kind"` // note | decision | merged | compacted | continues
	Note     *store.NoteInput     `json:"note,omitempty"`
	Decision *store.DecisionInput `json:"decision,omitempty"`
	Merged   *model.MergedData    `json:"merged,omitempty"`
	From     string               `json:"from,omitempty"` // continues
}

// valid reports whether exactly the field Kind names is set.
func (r AppendRequest) valid() bool {
	note, decision, merged, from := r.Note != nil, r.Decision != nil, r.Merged != nil, r.From != ""
	switch r.Kind {
	case model.KindNote:
		return note && !decision && !merged && !from
	case model.KindDecision:
		return decision && !note && !merged && !from
	case model.KindMerged:
		return merged && !note && !decision && !from
	case model.KindCompacted:
		return !note && !decision && !merged && !from
	case model.KindContinues:
		return from && !note && !decision && !merged
	}
	return false
}

// Status is what the status method returns.
type Status struct {
	Version  string               `json:"version"`
	Ticker   TickerStatus         `json:"ticker"`
	RunnerOn bool                 `json:"runner_on"`
	Tasks    map[model.Status]int `json:"tasks"`
	// RunnerState is off, paused, no-herdr, or on.
	RunnerState  string `json:"runner_state"`
	RunnerPaused bool   `json:"runner_paused"`
	RunnerCap    int    `json:"runner_cap"` // runner.cap
	// BackupTS is the last successful backup run, nil when none is recorded. BackupError is the error of the
	// last attempt when it failed after that run, "" otherwise. Both come from the backup state file.
	BackupTS    *time.Time `json:"backup_ts"`
	BackupError string     `json:"backup_error"`
}

// TickerStatus is the home's ticker: whether one holds the lock, and its pid and start time when it does.
type TickerStatus struct {
	Running   bool       `json:"running"`
	PID       int        `json:"pid"`
	StartedTS *time.Time `json:"started_ts"`
}

// TaskList is what tasks.list returns, and what Client.ListTasks returns when it answers from the snapshot.
type TaskList struct {
	Tasks      []model.Task `json:"tasks"`
	Offline    bool         `json:"offline"`
	SnapshotTS *time.Time   `json:"snapshot_ts"` // set only when Offline
}

// The request bodies of the methods whose body is not an existing type.
type (
	getRequest struct {
		Number int `json:"number"`
	}
	addRequest struct {
		Actor store.Actor        `json:"actor"`
		Input store.AddTaskInput `json:"input"`
	}
	setRequest struct {
		Actor  store.Actor `json:"actor"`
		Number int         `json:"number"`
		Patch  model.Patch `json:"patch"`
	}
	stepsRequest struct {
		Actor  store.Actor  `json:"actor"`
		Number int          `json:"number"`
		Op     model.StepOp `json:"op"`
	}
	sessionRequest struct {
		Session string `json:"session"`
	}
	killRequest struct {
		Actor store.Actor `json:"actor"`
		Task  int         `json:"task"`
	}
	startRequest struct {
		Actor store.Actor    `json:"actor"`
		Task  int            `json:"task"`
		Route store.RunRoute `json:"route"`
	}
	runsRequest struct {
		Reconcile bool `json:"reconcile,omitempty"` // check the live runs against herdr once before listing
	}
	pauseRequest struct {
		Actor  store.Actor `json:"actor"`
		Paused bool        `json:"paused"`
	}
	changesRequest struct {
		Actor store.Actor `json:"actor"`
	}
	empty struct{}
)
