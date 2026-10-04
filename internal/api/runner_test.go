package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/config"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
	"github.com/federbenjamin/desk/internal/testutil"
)

const w1RunnerToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func w1RunnerClient(t *testing.T, handler http.Handler) *api.Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	machine := testutil.NewMachine(t)
	cfg := config.Default()
	cfg.Client.Home = strings.TrimPrefix(server.URL, "http://")
	if err := config.WriteToken(machine.Paths, w1RunnerToken); err != nil {
		t.Fatalf("write client token: %v", err)
	}
	return api.NewClient(api.ClientOptions{Paths: machine.Paths, Config: cfg})
}

func TestW1KillRunPostsActorAndTaskAndReturnsTheHomeTask(t *testing.T) {
	actor := store.Actor{Session: "runner-session", Run: 17}
	want := model.Task{Number: 42, Title: "stopped by user", Status: model.StatusBlocked}
	client := w1RunnerClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("request method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/"+api.MethodRunsKill {
			t.Fatalf("request path = %q, want %q", r.URL.Path, "/v1/"+api.MethodRunsKill)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+w1RunnerToken {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		var body struct {
			Actor store.Actor `json:"actor"`
			Task  int         `json:"task"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !reflect.DeepEqual(body.Actor, actor) || body.Task != want.Number {
			t.Fatalf("request = %#v, want actor %#v and task %d", body, actor, want.Number)
		}
		if err := json.NewEncoder(w).Encode(want); err != nil {
			t.Fatalf("encode task: %v", err)
		}
	}))

	got, err := client.KillRun(context.Background(), actor, want.Number)
	if err != nil {
		t.Fatalf("KillRun: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("KillRun result = %#v, want %#v", got, want)
	}
}

func TestW1PauseRunnerPostsActorAndPausedAndReturnsTheHomeStatus(t *testing.T) {
	actor := store.Actor{Session: "runner-session", Run: 23}
	want := api.Status{RunnerOn: true, RunnerPaused: true, RunnerCap: 3, RunnerState: api.RunnerStatePaused}
	client := w1RunnerClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("request method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/"+api.MethodRunnerPause {
			t.Fatalf("request path = %q, want %q", r.URL.Path, "/v1/"+api.MethodRunnerPause)
		}
		var body struct {
			Actor  store.Actor `json:"actor"`
			Paused bool        `json:"paused"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !reflect.DeepEqual(body.Actor, actor) || !body.Paused {
			t.Fatalf("request = %#v, want actor %#v and paused true", body, actor)
		}
		if err := json.NewEncoder(w).Encode(want); err != nil {
			t.Fatalf("encode status: %v", err)
		}
	}))

	got, err := client.PauseRunner(context.Background(), actor, true)
	if err != nil {
		t.Fatalf("PauseRunner: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PauseRunner result = %#v, want %#v", got, want)
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
			client := w1RunnerClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/"+tc.method {
					t.Fatalf("request path = %q, want %q", r.URL.Path, "/v1/"+tc.method)
				}
				w.WriteHeader(http.StatusConflict)
				if err := json.NewEncoder(w).Encode(map[string]string{"code": "no-run", "message": "task has no live run"}); err != nil {
					t.Fatalf("encode refusal: %v", err)
				}
			}))

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
