// Package api is herdr-desk's JSON API over HTTP: POST /v1/<method> with a JSON body. The daemon serves it on a
// unix socket and, when configured, on a TCP address that requires the bearer token.
package api

import (
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
	MethodRunnerPause  = "runner.pause"
	MethodStatus       = "status"
	MethodBackupRun    = "backup.run" // unix socket only
)

// The values of Status.RunnerState.
const (
	RunnerStateOff      = "off"
	RunnerStateOn       = "on"
	RunnerStatePaused   = "paused"
	RunnerStateNoRouter = "no-router"
	RunnerStateNoHerdr  = "no-herdr"
)

// maxBody is the largest request body the server reads.
const maxBody = 1 << 20

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
	Version   string               `json:"version"`
	Listen    string               `json:"listen"`
	StartedTS time.Time            `json:"started_ts"`
	RunnerOn  bool                 `json:"runner_on"`
	Tasks     map[model.Status]int `json:"tasks"`
	// RunnerState is off, paused, no-herdr, no-router, or on.
	RunnerState  string `json:"runner_state"`
	RunnerPaused bool   `json:"runner_paused"`
	RunnerCap    int    `json:"runner_cap"` // runner.cap
	// BackupTS is the last successful backup run, nil when none is recorded. BackupError is the error of the
	// last attempt when it failed after that run, "" otherwise. Both come from the backup state file.
	BackupTS    *time.Time `json:"backup_ts"`
	BackupError string     `json:"backup_error"`
	// ConfigChanged is true when the config file now holds a different config from the one the daemon started
	// with (a touch or a same-content rewrite is not a change). The daemon reads the file once, at start, so it
	// is still running on the old values until `herdr-desk daemon restart`.
	ConfigChanged bool `json:"config_changed"`
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
	pauseRequest struct {
		Actor  store.Actor `json:"actor"`
		Paused bool        `json:"paused"`
	}
	empty struct{}
)

// errorBody is the body of every answer that is not 200. Code is set only on a 409.
type errorBody struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}
