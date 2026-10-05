package model_test

import (
	"encoding/json"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
)

func TestRunLiveRecognizesOnlyTheFourLiveStates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		state string
		want  bool
	}{
		{model.RunStarting, true},
		{model.RunWaiting, true},
		{model.RunRunning, true},
		{model.RunIdle, true},
		{"routing", false},
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

func TestRunJSONHasNoExitKey(t *testing.T) {
	t.Parallel()

	b, err := json.Marshal(model.Run{ID: 1, Task: 2, State: model.RunEnded})
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["exit"]; ok || len(keys) == 0 {
		t.Fatalf("run JSON = %s, want no exit key: nothing writes a worker's exit", b)
	}
}

func TestRunConstantsKeepTheirPublishedWireValues(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct{ got, want string }{
		"RunStarting":  {model.RunStarting, "starting"},
		"RunIdle":      {model.RunIdle, "idle"},
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
