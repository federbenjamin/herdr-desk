package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/cli"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
	"github.com/federbenjamin/herdr-desk/internal/worker"
)

func runDeskWithExec(t *testing.T, machine *testutil.Machine, args []string, extra map[string]string, run func(string, []string) error) commandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return commandResult{
		exit: cli.Run(context.Background(), args, cli.Env{
			Stdout: &stdout,
			Stderr: &stderr,
			Getenv: machine.Getenv(extra),
			Cwd:    t.TempDir(),
			Exec:   run,
		}),
		stdout: stdout.String(),
		stderr: stderr.String(),
	}
}

func TestWorkerRejectsMissingAndMalformedRunEnvironment(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	for _, test := range []struct {
		name string
		env  map[string]string
	}{
		{name: "missing run", env: map[string]string{"DESK_TASK": "T1"}},
		{name: "missing task", env: map[string]string{"DESK_RUN": "1"}},
		{name: "malformed run", env: map[string]string{"DESK_RUN": "nope", "DESK_TASK": "T1"}},
		{name: "malformed task", env: map[string]string{"DESK_RUN": "1", "DESK_TASK": "Tnope"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runDeskWithExec(t, home.Machine, []string{"worker"}, test.env, nil)
			if result.exit != 2 {
				t.Fatalf("worker exit = %d, want 2; stderr=%q", result.exit, result.stderr)
			}
		})
	}
}

func TestWorkerRefusesARunThatIsNotCurrentRunningTaskAndSession(t *testing.T) {
	t.Run("run is not running", func(t *testing.T) {
		home, root := runnerHome(t, []string{"worker"})
		task, run := startLiveRun(t, home, root, "ended worker")
		requireSuccess(t, runHomeDesk(t, home, "runs", "kill", fmt.Sprintf("T%d", task)))
		result := runDeskWithExec(t, home.Machine, []string{"worker"}, map[string]string{"DESK_RUN": fmt.Sprint(run.ID), "DESK_TASK": fmt.Sprintf("T%d", task), "DESK_SESSION": run.Session}, nil)
		requireRefusal(t, result, "worker", model.CodeNoRun, 1)
	})
	t.Run("run belongs to another task", func(t *testing.T) {
		home, root := runnerHome(t, []string{"worker"})
		_, run := startLiveRun(t, home, root, "current worker")
		other := addTask(t, home, "other task")
		result := runDeskWithExec(t, home.Machine, []string{"worker"}, map[string]string{"DESK_RUN": fmt.Sprint(run.ID), "DESK_TASK": fmt.Sprintf("T%d", other), "DESK_SESSION": run.Session}, nil)
		requireRefusal(t, result, "worker", model.CodeNoRun, 1)
	})
	t.Run("session does not own run", func(t *testing.T) {
		home, root := runnerHome(t, []string{"worker"})
		task, run := startLiveRun(t, home, root, "session worker")
		result := runDeskWithExec(t, home.Machine, []string{"worker"}, map[string]string{"DESK_RUN": fmt.Sprint(run.ID), "DESK_TASK": fmt.Sprintf("T%d", task), "DESK_SESSION": run.Session + "-other"}, nil)
		requireRefusal(t, result, "worker", model.CodeNoRun, 1)
	})
}

func TestWorkerExpandsOneMessageArgumentAndExecutesTheResolvedProgram(t *testing.T) {
	bin := t.TempDir()
	stub := filepath.Join(bin, "worker-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write worker stub: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	workerTemplate := []string{"worker-stub", "--model={model}", "--session={session}", "--message={message}"}
	home, root := runnerHome(t, workerTemplate)
	task, run := startLiveRun(t, home, root, "title {session} --flag")
	detail, err := home.Client().GetTask(context.Background(), task)
	if err != nil {
		t.Fatalf("get worker task: %v", err)
	}
	message := worker.FirstMessage(detail)
	wantArgv := config.Expand(workerTemplate, map[string]string{"model": run.Model, "session": run.Session, "message": message})
	var gotPath string
	var gotArgv []string
	result := runDeskWithExec(t, home.Machine, []string{"worker"}, map[string]string{
		"DESK_RUN":     fmt.Sprint(run.ID),
		"DESK_TASK":    fmt.Sprintf("T%d", task),
		"DESK_SESSION": run.Session,
	}, func(path string, argv []string) error {
		gotPath = path
		gotArgv = append([]string(nil), argv...)
		return nil
	})
	requireSuccess(t, result)
	if gotPath != stub {
		t.Fatalf("Exec path = %q, want PATH-resolved stub %q", gotPath, stub)
	}
	if !reflect.DeepEqual(gotArgv, wantArgv) {
		t.Fatalf("Exec argv = %#v, want config.Expand result %#v", gotArgv, wantArgv)
	}
	if strings.Count(strings.Join(gotArgv, "\x00"), message) != 1 || !strings.Contains(message, "title {session} --flag") {
		t.Fatalf("worker message was split or re-expanded: argv=%#v; message=%q", gotArgv, message)
	}
	if _, err := os.Stat(home.Paths.RunMessage(run.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worker wrote %s for a root with no first_message: stat error = %v", home.Paths.RunMessage(run.ID), err)
	}
}

// setFirstMessage sets first_message on the configured root at path.
func setFirstMessage(t *testing.T, home *testutil.Home, path, template string) {
	t.Helper()
	cfg, err := config.Load(home.Paths.ConfigFile())
	if err != nil {
		t.Fatalf("load home config: %v", err)
	}
	for i := range cfg.Roots {
		if cfg.Roots[i].Path == path {
			cfg.Roots[i].FirstMessage = template
		}
	}
	if err := cfg.Save(home.Paths.ConfigFile()); err != nil {
		t.Fatalf("save home config: %v", err)
	}
}

func TestWorkerSendsTheRootsFirstMessageNamingAFileThatHoldsTheTask(t *testing.T) {
	bin := t.TempDir()
	stub := filepath.Join(bin, "worker-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write worker stub: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	workerTemplate := []string{"worker-stub", "--model={model}", "{message}"}
	home, root := runnerHome(t, workerTemplate)
	setFirstMessage(t, home, root, "/build {task_file}")
	task, run := startLiveRun(t, home, root, "title {task_file} --flag")
	detail, err := home.Client().GetTask(context.Background(), task)
	if err != nil {
		t.Fatalf("get worker task: %v", err)
	}
	file := home.Paths.RunMessage(run.ID)
	var gotArgv []string
	result := runDeskWithExec(t, home.Machine, []string{"worker"}, map[string]string{
		"DESK_RUN":     fmt.Sprint(run.ID),
		"DESK_TASK":    fmt.Sprintf("T%d", task),
		"DESK_SESSION": run.Session,
	}, func(_ string, argv []string) error {
		gotArgv = append([]string(nil), argv...)
		return nil
	})
	requireSuccess(t, result)
	if want := []string{"worker-stub", "--model=" + run.Model, "/build " + file}; !reflect.DeepEqual(gotArgv, want) {
		t.Fatalf("Exec argv = %#v, want %#v", gotArgv, want)
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the run's first message: %v", err)
	}
	if want := worker.FirstMessage(detail); string(got) != want {
		t.Fatalf("%s holds %q, want worker.FirstMessage %q", file, got, want)
	}
	for path, want := range map[string]os.FileMode{file: 0o600, filepath.Dir(file): 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %04o, want %04o", path, info.Mode().Perm(), want)
		}
	}
}

func TestWorkerBlocksTheTaskWhenItsTemplateCannotStart(t *testing.T) {
	for _, test := range []struct {
		name    string
		worker  []string
		prepare func(t *testing.T, home *testutil.Home, root string)
		want    string
	}{
		{name: "empty template", worker: nil, want: "worker: cannot start"},
		{name: "missing executable", worker: []string{"not-on-path"}, want: `worker: cannot start not-on-path: exec: "not-on-path": executable file not found in $PATH`},
		{name: "first message cannot be written", worker: []string{"sh"}, prepare: func(t *testing.T, home *testutil.Home, root string) {
			setFirstMessage(t, home, root, "/build {task_file}")
			if err := os.WriteFile(filepath.Dir(home.Paths.RunMessage(1)), nil, 0o600); err != nil {
				t.Fatalf("put a file where the runs folder goes: %v", err)
			}
		}, want: "worker: cannot write the first message of run"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, root := runnerHome(t, test.worker)
			if test.prepare != nil {
				test.prepare(t, home, root)
			}
			task, run := startLiveRun(t, home, root, "cannot start worker")
			called := false
			result := runDeskWithExec(t, home.Machine, []string{"worker"}, map[string]string{
				"DESK_RUN":     fmt.Sprint(run.ID),
				"DESK_TASK":    fmt.Sprintf("T%d", task),
				"DESK_SESSION": run.Session,
			}, func(string, []string) error {
				called = true
				return errors.New("Exec must not be called")
			})
			if result.exit != 3 {
				t.Fatalf("worker exit = %d, want 3; stderr=%q", result.exit, result.stderr)
			}
			if !strings.Contains(result.stderr, test.want) {
				t.Fatalf("worker stderr = %q, want reason containing %q", result.stderr, test.want)
			}
			if called {
				t.Fatal("worker called Exec after its template could not start")
			}
			detail, err := home.Client().GetTask(context.Background(), task)
			if err != nil {
				t.Fatalf("get blocked task: %v", err)
			}
			if detail.Task.Status != model.StatusBlocked {
				t.Fatalf("task status = %q, want blocked", detail.Task.Status)
			}
			found := false
			for _, event := range detail.History {
				var note model.NoteData
				if event.Kind == model.KindNote && json.Unmarshal(event.Data, &note) == nil && strings.Contains(note.Text, test.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("task history = %#v, want worker failure note containing %q", detail.History, test.want)
			}
		})
	}
}
