package board

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// FailedOf is the Failed that Run's executor feeds back when e fails with err: its error names e.
func FailedOf(e Effect, err error) Failed { return Failed{Err: effectErr{effect: e, err: err}} }

// Answer is what Run's executor feeds back when it runs e against h.
func Answer(h Home, e Effect) tea.Msg {
	return newExecutor(Options{Home: h}).answer(context.Background(), e)
}

// Feed applies msg to s as Run's program does.
func Feed(s State, msg tea.Msg) (State, []Effect) { return feed(s, msg) }

// Sent is the number of notes saves s keeps because their write may still fail.
func Sent(s State) int { return len(s.sent) }
