package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

func TestEditAppendNotesRetriesOnceWithTheNotesItLastRead(t *testing.T) {
	gets, sets := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tasks.get":
			gets++
			notes := "before"
			if gets == 2 {
				notes = "theirs"
			}
			if err := json.NewEncoder(w).Encode(store.TaskDetail{Task: model.Task{Number: 1, Notes: notes}}); err != nil {
				t.Errorf("encode task detail: %v", err)
			}
		case "/v1/tasks.set":
			sets++
			var request struct {
				Patch model.Patch `json:"patch"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode set request: %v", err)
				return
			}
			wantOld, wantNotes := "before", "before\nadd this"
			if sets == 2 {
				wantOld, wantNotes = "theirs", "theirs\nadd this"
			}
			if request.Patch.NotesWere == nil || *request.Patch.NotesWere != wantOld || request.Patch.Notes == nil || *request.Patch.Notes != wantNotes {
				t.Errorf("set %d patch = %#v, want notes %q with NotesWere %q", sets, request.Patch, wantNotes, wantOld)
			}
			if sets == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"code":"stale","message":"notes changed"}`))
				return
			}
			if err := json.NewEncoder(w).Encode(model.Task{Number: 1, Notes: wantNotes}); err != nil {
				t.Errorf("encode task: %v", err)
			}
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	machine := testutil.NewMachine(t)
	cfg := config.Default()
	cfg.Client.Home = strings.TrimPrefix(server.URL, "http://")
	if err := cfg.Save(machine.Paths.ConfigFile()); err != nil {
		t.Fatalf("save client config: %v", err)
	}
	if err := config.WriteToken(machine.Paths, "test-token"); err != nil {
		t.Fatalf("write client token: %v", err)
	}

	result := runDesk(t, machine.Getenv(nil), t.TempDir(), "", "edit", "T1", "--append-notes", "add this")
	if result.exit != 0 || result.stdout != "T1\n" || result.stderr != "" {
		t.Fatalf("append after stale = %#v, want exit 0 and T1", result)
	}
	if gets != 2 || sets != 2 {
		t.Fatalf("append calls = %d gets, %d sets; want one retry", gets, sets)
	}
}

func TestEditRejectsReplacingAndAppendingNotesTogether(t *testing.T) {
	machine := testutil.NewMachine(t)
	result := runDesk(t, machine.Getenv(nil), t.TempDir(), "", "edit", "T1", "--notes", "replace", "--append-notes", "append")
	if result.exit != 2 || result.stdout != "" || !strings.Contains(result.stderr, "mutually exclusive") {
		t.Fatalf("edit with both notes flags = %#v, want usage refusal", result)
	}
}
