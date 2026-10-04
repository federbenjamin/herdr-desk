package model_test

import (
	"testing"

	"github.com/federbenjamin/desk/internal/model"
)

func TestRunLiveRecognizesOnlyTheThreeLiveStates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		state string
		want  bool
	}{
		{model.RunRouting, true},
		{model.RunWaiting, true},
		{model.RunRunning, true},
		{model.RunEnded, false},
		{model.RunFailed, false},
		{model.RunKilled, false},
		{"unknown", false},
	} {
		if got := model.RunLive(test.state); got != test.want {
			t.Errorf("RunLive(%q) = %t; want %t", test.state, got, test.want)
		}
	}
}

func TestRunConstantsKeepTheirPublishedWireValues(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct{ got, want string }{
		"RunRouting":   {model.RunRouting, "routing"},
		"RunWaiting":   {model.RunWaiting, "waiting"},
		"RunRunning":   {model.RunRunning, "running"},
		"RunEnded":     {model.RunEnded, "ended"},
		"RunFailed":    {model.RunFailed, "failed"},
		"RunKilled":    {model.RunKilled, "killed"},
		"TagRunner":    {model.TagRunner, "runner"},
		"TagRouter":    {model.TagRouter, "router"},
		"CodeStaleRun": {model.CodeStaleRun, "stale-run"},
		"CodeNoRun":    {model.CodeNoRun, "no-run"},
	} {
		if test.got != test.want {
			t.Errorf("%s = %q; want %q", name, test.got, test.want)
		}
	}
}
