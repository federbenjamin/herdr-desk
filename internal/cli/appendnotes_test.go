package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

const appendNotesRPCStateEnv = "DESK_APPEND_NOTES_RPC_STATE"

type appendNotesRPCState struct {
	Gets        int  `json:"gets"`
	Sets        int  `json:"sets"`
	AlwaysStale bool `json:"always_stale"`
}

// init turns a child copy of this test binary into the controlled home command for the retry case.
func init() {
	statePath := os.Getenv(appendNotesRPCStateEnv)
	if statePath == "" {
		return
	}
	var request api.RPCRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(2)
	}
	state := appendNotesRPCState{}
	if data, err := os.ReadFile(statePath); err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			os.Exit(2)
		}
	} else if !os.IsNotExist(err) {
		os.Exit(2)
	}

	response := api.RPCResponse{}
	switch request.Method {
	case api.MethodTasksGet:
		state.Gets++
		notes := "before"
		if state.Gets == 2 {
			notes = "theirs"
		}
		response.Result = mustAppendNotesJSON(storeTaskDetail(notes))
	case api.MethodTasksSet:
		state.Sets++
		var params struct {
			Patch model.Patch `json:"patch"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			os.Exit(2)
		}
		wantOld, wantNotes := "before", "before\nadd this"
		if state.Sets == 2 {
			wantOld, wantNotes = "theirs", "theirs\nadd this"
		}
		if params.Patch.NotesWere == nil || *params.Patch.NotesWere != wantOld || params.Patch.Notes == nil || *params.Patch.Notes != wantNotes {
			response.Error = &api.RPCError{Message: "append patch did not name the notes it read"}
		} else if state.Sets == 1 || state.AlwaysStale {
			response.Refusal = &model.Refusal{Code: model.CodeStale, Msg: "notes changed"}
		} else {
			response.Result = mustAppendNotesJSON(model.Task{Number: 1, Notes: wantNotes})
		}
	default:
		response.Error = &api.RPCError{BadRequest: true, Message: "unexpected method"}
	}
	data, err := json.Marshal(state)
	if err != nil || os.WriteFile(statePath, data, 0o600) != nil {
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func storeTaskDetail(notes string) struct {
	Task model.Task `json:"task"`
} {
	return struct {
		Task model.Task `json:"task"`
	}{Task: model.Task{Number: 1, Notes: notes}}
}

func mustAppendNotesJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestEditAppendNotesRetriesOnceWithTheNotesItLastRead(t *testing.T) {
	machine := testutil.NewMachine(t)
	cfg := config.Default()
	statePath := machine.Paths.StateDir + "/append-notes-rpc.json"
	cfg.Client.Home = "test-home"
	cfg.Client.Command = []string{"env", appendNotesRPCStateEnv + "=" + statePath, os.Args[0], "-test.run=^$"}
	if err := cfg.Save(machine.Paths.ConfigFile()); err != nil {
		t.Fatalf("save client config: %v", err)
	}

	result := runDesk(t, machine.Getenv(nil), t.TempDir(), "", "edit", "T1", "--append-notes", "add this")
	if result.exit != 0 || result.stdout != "T1\n" || result.stderr != "" {
		t.Fatalf("append after stale = %#v, want exit 0 and T1", result)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read rpc state: %v", err)
	}
	var state appendNotesRPCState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode rpc state: %v", err)
	}
	if state.Gets != 2 || state.Sets != 2 {
		t.Fatalf("append calls = %d gets, %d sets; want one retry", state.Gets, state.Sets)
	}
}

func TestEditAppendNotesStopsAfterOneStaleRetry(t *testing.T) {
	machine := testutil.NewMachine(t)
	statePath := machine.Paths.StateDir + "/append-notes-rpc.json"
	state := []byte(`{"always_stale":true}`)
	if err := os.MkdirAll(machine.Paths.StateDir, 0o700); err != nil {
		t.Fatalf("make rpc state directory: %v", err)
	}
	if err := os.WriteFile(statePath, state, 0o600); err != nil {
		t.Fatalf("write rpc state: %v", err)
	}
	cfg := config.Default()
	cfg.Client.Home = "test-home"
	cfg.Client.Command = []string{"env", appendNotesRPCStateEnv + "=" + statePath, os.Args[0], "-test.run=^$"}
	if err := cfg.Save(machine.Paths.ConfigFile()); err != nil {
		t.Fatalf("save client config: %v", err)
	}

	result := runDesk(t, machine.Getenv(nil), t.TempDir(), "", "edit", "T1", "--append-notes", "add this")
	if result.exit != 1 || result.stdout != "" || !strings.Contains(result.stderr, model.CodeStale) {
		t.Fatalf("append after two stale responses = %#v, want exit 1 with stale", result)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read rpc state: %v", err)
	}
	var calls appendNotesRPCState
	if err := json.Unmarshal(data, &calls); err != nil {
		t.Fatalf("decode rpc state: %v", err)
	}
	if calls.Gets != 2 || calls.Sets != 2 {
		t.Fatalf("append calls after stale = %d gets, %d sets; want exactly one retry", calls.Gets, calls.Sets)
	}
}

func TestEditRejectsReplacingAndAppendingNotesTogether(t *testing.T) {
	machine := testutil.NewMachine(t)
	result := runDesk(t, machine.Getenv(nil), t.TempDir(), "", "edit", "T1", "--notes", "replace", "--append-notes", "append")
	if result.exit != 2 || result.stdout != "" || !strings.Contains(result.stderr, "notes") || !strings.Contains(result.stderr, "append-notes") {
		t.Fatalf("edit with both notes flags = %#v, want usage refusal", result)
	}
}

func TestEditAppendNotesRefusesEmptyTextAndWritesNothing(t *testing.T) {
	for _, text := range []string{"", "   ", "\n\t"} {
		home := testutil.StartHome(t, testutil.HomeOptions{})
		task, err := home.Client().AddTask(context.Background(), store.Actor{}, store.AddTaskInput{TaskData: model.TaskData{Title: "notes task", Notes: "keep me"}})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		result := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"edit", "T1", "--append-notes", text}, "", nil)
		if result.exit != 1 || result.stdout != "" || !strings.Contains(result.stderr, model.CodeEmptyText) {
			t.Errorf("edit --append-notes %q = %#v; want exit 1 and %s", text, result, model.CodeEmptyText)
		}
		detail, err := home.Client().GetTask(context.Background(), task.Number)
		if err != nil {
			t.Fatalf("read task: %v", err)
		}
		if detail.Task.Notes != "keep me" {
			t.Errorf("notes after refused append %q = %q; want them unchanged", text, detail.Task.Notes)
		}
	}
}
