package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestServeRPCWritesOneEnvelopeForResultsRefusalsAndBadRequests(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		setup func(t *testing.T, home *testutil.Home)
		check func(t *testing.T, response api.RPCResponse)
	}{
		{
			name:  "result",
			input: `{"method":"status","params":{}}`,
			check: func(t *testing.T, response api.RPCResponse) {
				t.Helper()
				if response.Refusal != nil || response.Error != nil || !json.Valid(response.Result) {
					t.Fatalf("status response = %+v, want only a JSON result", response)
				}
			},
		},
		{
			name:  "refusal",
			input: `{"method":"tasks.get","params":{"number":999}}`,
			check: func(t *testing.T, response api.RPCResponse) {
				t.Helper()
				if response.Result != nil || response.Error != nil || response.Refusal == nil || response.Refusal.Code != model.CodeUnknownTask {
					t.Fatalf("unknown task response = %+v, want only the %q refusal", response, model.CodeUnknownTask)
				}
			},
		},
		{
			name:  "bad request",
			input: `{`,
			check: func(t *testing.T, response api.RPCResponse) {
				t.Helper()
				if response.Result != nil || response.Refusal != nil || response.Error == nil || !response.Error.BadRequest {
					t.Fatalf("malformed request response = %+v, want only a bad-request error", response)
				}
			},
		},
		{
			name:  "request over one mebibyte",
			input: strings.Repeat("x", 1<<20+1),
			check: func(t *testing.T, response api.RPCResponse) {
				t.Helper()
				if response.Result != nil || response.Refusal != nil || response.Error == nil || !response.Error.BadRequest || !strings.Contains(response.Error.Message, "over 1048576 bytes") {
					t.Fatalf("oversize request response = %+v, want only a bad-request error", response)
				}
			},
		},
		{
			name:  "home failure",
			input: `{"method":"status","params":{}}`,
			setup: func(t *testing.T, home *testutil.Home) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(home.Paths.DB()), 0o700); err != nil {
					t.Fatalf("create the store folder: %v", err)
				}
				if err := os.Mkdir(home.Paths.DB(), 0o700); err != nil {
					t.Fatalf("block the store path: %v", err)
				}
			},
			check: func(t *testing.T, response api.RPCResponse) {
				t.Helper()
				if response.Result != nil || response.Refusal != nil || response.Error == nil || response.Error.BadRequest {
					t.Fatalf("store failure response = %+v, want only a retryable error", response)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := testutil.StartHome(t, testutil.HomeOptions{})
			cfg, err := config.Load(home.Paths.ConfigFile())
			if err != nil {
				t.Fatalf("load home config: %v", err)
			}
			if tc.setup != nil {
				tc.setup(t, home)
			}
			var out bytes.Buffer
			if err := api.ServeRPC(context.Background(), home.Paths, cfg, bytes.NewBufferString(tc.input), &out); err != nil {
				t.Fatalf("ServeRPC: %v", err)
			}
			decoder := json.NewDecoder(&out)
			var response api.RPCResponse
			if err := decoder.Decode(&response); err != nil {
				t.Fatalf("decode rpc response %q: %v", out.String(), err)
			}
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				t.Fatalf("rpc output contains another response after %q: %v", out.String(), err)
			}
			tc.check(t, response)
		})
	}
}

func TestLocalTransportKeepsItsStoreUntilClose(t *testing.T) {
	ctx := context.Background()
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := home.Client()
	task, err := client.AddTask(ctx, store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "keep the opened store"}})
	if err != nil {
		t.Fatalf("add task: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close setup client: %v", err)
	}
	cfg, err := config.Load(home.Paths.ConfigFile())
	if err != nil {
		t.Fatalf("load home config: %v", err)
	}
	transport := api.NewLocalTransport(home.Paths, cfg)
	t.Cleanup(func() {
		if err := transport.Close(); err != nil {
			t.Errorf("close local transport: %v", err)
		}
	})

	list := func() api.TaskList {
		t.Helper()
		response, err := transport.RoundTrip(ctx, api.MethodTasksList, []byte(`{}`))
		if err != nil {
			t.Fatalf("tasks.list: %v", err)
		}
		var envelope api.RPCResponse
		if err := json.Unmarshal(response, &envelope); err != nil {
			t.Fatalf("decode tasks.list response %q: %v", response, err)
		}
		if envelope.Error != nil || envelope.Refusal != nil {
			t.Fatalf("tasks.list response = %+v, want a result", envelope)
		}
		var got api.TaskList
		if err := json.Unmarshal(envelope.Result, &got); err != nil {
			t.Fatalf("decode task list: %v", err)
		}
		return got
	}

	if got := list(); len(got.Tasks) != 1 || got.Tasks[0].Number != task.Number {
		t.Fatalf("first task list = %+v, want task %d", got.Tasks, task.Number)
	}
	moved := home.Paths.DB() + ".moved"
	if err := os.Rename(home.Paths.DB(), moved); err != nil {
		t.Fatalf("move store after first request: %v", err)
	}
	if got := list(); len(got.Tasks) != 1 || got.Tasks[0].Number != task.Number {
		t.Fatalf("second task list = %+v, want the already-opened task %d", got.Tasks, task.Number)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("close local transport: %v", err)
	}
	if got := list(); len(got.Tasks) != 0 {
		t.Fatalf("task list after Close = %+v, want a reopened empty store", got.Tasks)
	}
}

func TestCommandTransportCarriesOneRequestAndClassifiesAnUnreachableHome(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	machine := testutil.NewClientMachine(t, home)
	cfg, err := config.Load(machine.Paths.ConfigFile())
	if err != nil {
		t.Fatalf("load client config: %v", err)
	}
	transport := api.NewCommandTransport(machine.Paths, cfg, time.Second)

	response, err := transport.RoundTrip(context.Background(), api.MethodTasksGet, []byte(`{"number":999}`))
	if err != nil {
		t.Fatalf("round trip to running home: %v", err)
	}
	var envelope api.RPCResponse
	if err := json.Unmarshal(response, &envelope); err != nil {
		t.Fatalf("decode home response %q: %v", response, err)
	}
	if envelope.Result != nil || envelope.Error != nil || envelope.Refusal == nil || envelope.Refusal.Code != model.CodeUnknownTask {
		t.Fatalf("running home response = %+v, want only the %q refusal", envelope, model.CodeUnknownTask)
	}

	home.Stop()
	_, err = transport.RoundTrip(context.Background(), api.MethodStatus, []byte(`{}`))
	refusal, ok := model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("round trip to stopped home error = %v, want %q refusal", err, model.CodeHomeUnreachable)
	}

	missingCommand := api.NewCommandTransport(machine.Paths, config.Config{Client: config.Client{
		Home:    "missing-home",
		Command: []string{machine.Paths.ConfigDir + "/missing-client-command"},
	}}, time.Second)
	_, err = missingCommand.RoundTrip(context.Background(), api.MethodStatus, []byte(`{}`))
	refusal, ok = model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("round trip with an unstartable command error = %v, want %q refusal", err, model.CodeHomeUnreachable)
	}
}

func TestCommandTransportClassifiesItsDeadlineAsAnUnreachableHome(t *testing.T) {
	if os.Getenv("HERDR_DESK_W3_HANG") == "1" {
		select {}
	}

	machine := testutil.NewMachine(t)
	transport := api.NewCommandTransport(machine.Paths, config.Config{Client: config.Client{
		Home:    "slow-home",
		Command: []string{"env", "HERDR_DESK_W3_HANG=1", os.Args[0], "-test.run=^TestCommandTransportClassifiesItsDeadlineAsAnUnreachableHome$"},
	}}, time.Millisecond)

	_, err := transport.RoundTrip(context.Background(), api.MethodStatus, []byte(`{}`))
	refusal, ok := model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("round trip past its deadline error = %v, want %q refusal", err, model.CodeHomeUnreachable)
	}
}

// runs.start may make a git worktree and a herdr workspace on the home, which can take longer than the per-request
// timeout: cutting it would report the home unreachable and leave the run half started.
func TestCommandTransportLetsARunStartOutlastThePerRequestTimeout(t *testing.T) {
	machine := testutil.NewMachine(t)
	command := filepath.Join(machine.Paths.ConfigDir, "slow-home")
	if err := os.MkdirAll(filepath.Dir(command), 0o700); err != nil {
		t.Fatalf("create command folder: %v", err)
	}
	if err := os.WriteFile(command, []byte("#!/bin/sh\ncat >/dev/null\nsleep 0.3\nprintf '%s\\n' '{\"result\":{\"id\":7}}'\n"), 0o700); err != nil {
		t.Fatalf("write slow home command: %v", err)
	}
	transport := api.NewCommandTransport(machine.Paths, config.Config{Client: config.Client{Home: "slow-home", Command: []string{command}}},
		50*time.Millisecond)

	response, err := transport.RoundTrip(context.Background(), api.MethodRunsStart, []byte(`{"task":1}`))
	if err != nil || !strings.Contains(string(response), `"id":7`) {
		t.Fatalf("runs.start past the timeout = %q, %v; want the home's answer", response, err)
	}
	if _, err := transport.RoundTrip(context.Background(), api.MethodStatus, []byte(`{}`)); err == nil {
		t.Fatal("status past the timeout error = nil, want the timeout to still bound other requests")
	}
}

type w3CountingTransport struct {
	calls  int
	closes int
	err    error
}

func (t *w3CountingTransport) RoundTrip(context.Context, string, []byte) ([]byte, error) {
	t.calls++
	return nil, t.err
}

func (t *w3CountingTransport) Close() error {
	t.closes++
	return nil
}

func TestClientKeepsTheFirstUnreachableHomeForItsLifetime(t *testing.T) {
	transport := &w3CountingTransport{err: &model.Refusal{Code: model.CodeHomeUnreachable, Msg: "the home did not answer"}}
	client := api.NewClient(api.ClientOptions{Transport: transport})

	for attempt := 1; attempt <= 2; attempt++ {
		_, err := client.Status(context.Background())
		refusal, ok := model.AsRefusal(err)
		if !ok || refusal.Code != model.CodeHomeUnreachable {
			t.Fatalf("Status attempt %d error = %v, want %q refusal", attempt, err, model.CodeHomeUnreachable)
		}
	}
	if transport.calls != 1 {
		t.Fatalf("unreachable client called its transport %d times, want one", transport.calls)
	}
}

func TestClientKeepsACommandStartFailureStickyForItsLifetime(t *testing.T) {
	machine := testutil.NewMachine(t)
	command := filepath.Join(machine.Paths.ConfigDir, "home-command")
	client := api.NewClient(api.ClientOptions{
		Paths: machine.Paths,
		Config: config.Config{Client: config.Client{
			Home:    "home",
			Command: []string{command},
		}},
		Timeout: time.Second,
	})

	_, err := client.Status(context.Background())
	refusal, ok := model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("Status with a missing command error = %v, want %q refusal", err, model.CodeHomeUnreachable)
	}
	if err := os.MkdirAll(filepath.Dir(command), 0o700); err != nil {
		t.Fatalf("create command folder: %v", err)
	}
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s\\n' '{\"result\":{}}'\n"), 0o700); err != nil {
		t.Fatalf("write command that would answer after the failure: %v", err)
	}

	_, err = client.Status(context.Background())
	refusal, ok = model.AsRefusal(err)
	if !ok || refusal.Code != model.CodeHomeUnreachable {
		t.Fatalf("second Status after command appears error = %v, want the sticky %q refusal", err, model.CodeHomeUnreachable)
	}
}

func TestClientCloseClosesItsTransportOnce(t *testing.T) {
	transport := &w3CountingTransport{}
	client := api.NewClient(api.ClientOptions{Transport: transport})

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if transport.closes != 1 {
		t.Fatalf("Close called the transport %d times, want once", transport.closes)
	}
}
