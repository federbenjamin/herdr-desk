package api_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestW1KillRunSendsActorAndTaskAndReturnsTheHomeTask(t *testing.T) {
	actor := store.Actor{Session: "runner-session", Run: 17}
	want := model.Task{Number: 42, Title: "stopped by user", Status: model.StatusBlocked}
	_, client := fakeClient(t, func(method string, params []byte) api.RPCResponse {
		if method != api.MethodRunsKill {
			t.Fatalf("method = %q, want %q", method, api.MethodRunsKill)
		}
		var body struct {
			Actor store.Actor `json:"actor"`
			Task  int         `json:"task"`
		}
		if err := json.Unmarshal(params, &body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !reflect.DeepEqual(body.Actor, actor) || body.Task != want.Number {
			t.Fatalf("request = %#v, want actor %#v and task %d", body, actor, want.Number)
		}
		return result(t, want)
	}, api.ClientOptions{})

	got, err := client.KillRun(context.Background(), actor, want.Number)
	if err != nil {
		t.Fatalf("KillRun: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("KillRun result = %#v, want %#v", got, want)
	}
}

func TestW1PauseRunnerSendsActorAndPausedAndReturnsTheHomeStatus(t *testing.T) {
	for _, paused := range []bool{true, false} {
		actor := store.Actor{Session: "runner-session", Run: 23}
		want := api.Status{RunnerOn: true, RunnerPaused: paused, RunnerCap: 3, RunnerState: api.RunnerStatePaused}
		_, client := fakeClient(t, func(method string, params []byte) api.RPCResponse {
			if method != api.MethodRunnerPause {
				t.Fatalf("method = %q, want %q", method, api.MethodRunnerPause)
			}
			var body struct {
				Actor  store.Actor `json:"actor"`
				Paused bool        `json:"paused"`
			}
			if err := json.Unmarshal(params, &body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if !reflect.DeepEqual(body.Actor, actor) || body.Paused != paused {
				t.Fatalf("request = %#v, want actor %#v and paused %t", body, actor, paused)
			}
			return result(t, want)
		}, api.ClientOptions{})

		got, err := client.PauseRunner(context.Background(), actor, paused)
		if err != nil {
			t.Fatalf("PauseRunner(%t): %v", paused, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("PauseRunner(%t) result = %#v, want %#v", paused, got, want)
		}
	}
}

func TestW1RunnerWritesReturnHomeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		call   func(*api.Client) error
	}{
		{
			name:   "kill",
			method: api.MethodRunsKill,
			call: func(client *api.Client) error {
				_, err := client.KillRun(context.Background(), store.Actor{}, 7)
				return err
			},
		},
		{
			name:   "pause",
			method: api.MethodRunnerPause,
			call: func(client *api.Client) error {
				_, err := client.PauseRunner(context.Background(), store.Actor{}, false)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client := fakeClient(t, func(method string, _ []byte) api.RPCResponse {
				if method != tc.method {
					t.Fatalf("method = %q, want %q", method, tc.method)
				}
				return api.RPCResponse{Refusal: &model.Refusal{Code: "no-run", Msg: "task has no live run"}}
			}, api.ClientOptions{})

			refusal, ok := model.AsRefusal(tc.call(client))
			if !ok {
				t.Fatalf("%s returned a non-refusal error", tc.name)
			}
			if refusal.Code != "no-run" || refusal.Msg != "task has no live run" {
				t.Fatalf("refusal = %#v, want code no-run and the home's message", refusal)
			}
		})
	}
}

func TestW1StatusReportsConfiguredRunnerCap(t *testing.T) {
	config := config.Default()
	config.Runner.Cap = 7
	home := testutil.StartHome(t, testutil.HomeOptions{Config: config})

	status, err := home.Client().Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.RunnerCap != config.Runner.Cap {
		t.Fatalf("RunnerCap = %d, want configured cap %d", status.RunnerCap, config.Runner.Cap)
	}
}

func TestW1RunnerStateConstantsKeepPublishedWireValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{name: "off", got: api.RunnerStateOff, want: "off"},
		{name: "on", got: api.RunnerStateOn, want: "on"},
		{name: "paused", got: api.RunnerStatePaused, want: "paused"},
		{name: "no router", got: api.RunnerStateNoRouter, want: "no-router"},
		{name: "no herdr", got: api.RunnerStateNoHerdr, want: "no-herdr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("runner state = %q, want %q", tc.got, tc.want)
			}
		})
	}
}
