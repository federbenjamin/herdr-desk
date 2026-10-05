package model

import "errors"

// Refusal is a write or read the desk refused; Code is stable.
type Refusal struct {
	Code string `json:"code"`
	Msg  string `json:"message"`
}

// Error returns "<code>: <message>".
func (r *Refusal) Error() string { return r.Code + ": " + r.Msg }

// The stable refusal codes.
const (
	CodeUnknownTask     = "unknown-task"
	CodeUnknownProject  = "unknown-project"
	CodeUnknownStep     = "unknown-step"
	CodeUnknownEvent    = "unknown-event"
	CodeEmptyTitle      = "empty-title"
	CodeEmptyText       = "empty-text"
	CodeSecretDetected  = "secret-detected"
	CodeNotAllowed      = "not-allowed"
	CodeBackupOff       = "backup-off"
	CodeStaleRun        = "stale-run"        // a status write from a run that is not the task's newest
	CodeStale           = "stale"            // a notes write whose NotesWere is not the task's notes now
	CodeNoRun           = "no-run"           // the task has no live run to kill, or herdr-desk worker's run is not running
	CodeBadInput        = "bad-input"        // exit 2: a value the store does not know (a status, an isolation, a step op, a session id) or a merged event with no branch
	CodeHomeUnreachable = "home-unreachable" // exit 3
	CodeScanFailed      = "scan-failed"      // exit 3
)

// AsRefusal reports the Refusal in err's chain, if any.
func AsRefusal(err error) (*Refusal, bool) {
	var r *Refusal
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}
