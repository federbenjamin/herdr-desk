package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
	_ "modernc.org/sqlite"
)

const storeAccessHelperEnv = "HERDR_DESK_STORE_ACCESS_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(storeAccessHelperEnv) == "1" {
		os.Exit(runStoreAccessHelper())
	}
	os.Exit(m.Run())
}

func TestStoreAccessProcessHelper(t *testing.T) {}

func runStoreAccessHelper() int {
	start := os.NewFile(uintptr(3), "store-access-start")
	if start == nil {
		fmt.Fprintln(os.Stderr, "missing start barrier")
		return 2
	}
	defer start.Close()
	if _, err := start.Read(make([]byte, 1)); err != nil {
		fmt.Fprintf(os.Stderr, "wait for start barrier: %v\n", err)
		return 2
	}

	path := os.Getenv("HERDR_DESK_STORE_ACCESS_PATH")
	st, err := store.Open(path, store.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Open(%q): %v\n", path, err)
		return 1
	}
	defer st.Close()

	switch os.Getenv("HERDR_DESK_STORE_ACCESS_MODE") {
	case "add":
		task, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{
			TaskData: model.TaskData{Title: "child task"},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "AddTask: %v\n", err)
			return 1
		}
		fmt.Println(task.Number)
	case "update":
		id, err := strconv.ParseInt(os.Getenv("HERDR_DESK_STORE_ACCESS_RUN"), 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse run id: %v\n", err)
			return 2
		}
		updated, err := st.UpdateRun(context.Background(), id, model.RunStarting, store.RunUpdate{State: model.RunRunning})
		if err != nil {
			fmt.Fprintf(os.Stderr, "UpdateRun: %v\n", err)
			return 1
		}
		fmt.Println(updated)
	case "open":
		fmt.Println("opened")
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", os.Getenv("HERDR_DESK_STORE_ACCESS_MODE"))
		return 2
	}
	return 0
}

func TestConcurrentAddTaskWritesGiveEveryProcessANumber(t *testing.T) {
	machine := testutil.NewMachine(t)
	path := machine.Paths.DB()
	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("initialize store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close initialized store: %v", err)
	}

	outputs := runStoreAccessHelpers(t, path, "add", nil, 12)
	got := make([]int, 0, len(outputs))
	for _, output := range outputs {
		number, err := strconv.Atoi(strings.TrimSpace(output))
		if err != nil {
			t.Fatalf("child output %q is not a task number: %v", output, err)
		}
		got = append(got, number)
	}
	sort.Ints(got)
	for i, number := range got {
		if want := i + 1; number != want {
			t.Fatalf("task numbers = %v, want every number 1 through 12", got)
		}
	}
}

func TestConcurrentColdOpenAndAddTaskDoesNotReturnBusy(t *testing.T) {
	machine := testutil.NewMachine(t)
	outputs := runStoreAccessHelpers(t, machine.Paths.DB(), "add", nil, 12)
	got := make([]int, 0, len(outputs))
	for _, output := range outputs {
		number, err := strconv.Atoi(strings.TrimSpace(output))
		if err != nil {
			t.Fatalf("child output %q is not a task number: %v", output, err)
		}
		got = append(got, number)
	}
	sort.Ints(got)
	for i, number := range got {
		if want := i + 1; number != want {
			t.Fatalf("task numbers = %v, want every number 1 through 12", got)
		}
	}
}

func TestOpenCurrentStoreDoesNotNeedTheWriterLock(t *testing.T) {
	machine := testutil.NewMachine(t)
	path := machine.Paths.DB()
	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("initialize store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close initialized store: %v", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open locking connection: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take writer lock: %v", err)
	}
	defer db.Exec("ROLLBACK")

	output := runStoreAccessHelpers(t, path, "open", nil, 1)
	if got := strings.TrimSpace(output[0]); got != "opened" {
		t.Errorf("open helper output = %q, want opened", got)
	}
}

func TestOpenReadOnlyLeavesTheFilesystemAndSchemaUntouched(t *testing.T) {
	machine := testutil.NewMachine(t)
	missing := filepath.Join(machine.Paths.DataDir, "missing.db")
	if _, err := store.OpenReadOnly(missing, store.Options{}); !errors.Is(err, store.ErrNoStore) {
		t.Fatalf("OpenReadOnly(missing) error = %v, want ErrNoStore", err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing store parent stat error = %v, want it not to exist", err)
	}

	path := machine.Paths.DB()
	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("initialize store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close initialized store: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("make existing store mode observable: %v", err)
	}
	readonly, err := store.OpenReadOnly(path, store.Options{})
	if err != nil {
		t.Fatalf("open current store read-only: %v", err)
	}
	if _, err := readonly.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{
		TaskData: model.TaskData{Title: "must not be written"},
	}); err == nil {
		readonly.Close()
		t.Fatal("AddTask through OpenReadOnly succeeded, want a query-only error")
	}
	if err := readonly.Close(); err != nil {
		t.Fatalf("close read-only store: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store after read-only open: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("store mode after OpenReadOnly = %o, want 644", got)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open schema connection: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
		db.Close()
		t.Fatalf("set future schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close schema connection: %v", err)
	}
	if _, err := store.OpenReadOnly(path, store.Options{}); !errors.Is(err, store.ErrSchema) {
		t.Fatalf("OpenReadOnly(future schema) error = %v, want ErrSchema", err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen schema connection: %v", err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 99 {
		t.Errorf("user_version after OpenReadOnly = %d, want 99", version)
	}
}

func TestConcurrentUpdateRunWritesWaitInsteadOfReturningBusy(t *testing.T) {
	machine := testutil.NewMachine(t)
	path := machine.Paths.DB()
	st, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	var runs []model.Run
	for i := 0; i < 12; i++ {
		task, err := st.AddTask(context.Background(), store.Actor{}, store.AddTaskInput{
			TaskData: model.TaskData{Title: fmt.Sprintf("ready %d", i), Status: model.StatusReady, Thread: "agent"},
		})
		if err != nil {
			st.Close()
			t.Fatalf("add ready task %d: %v", i, err)
		}
		run, err := st.StartRun(context.Background(), task.Number, store.RunRoute{}, 100)
		if err != nil {
			st.Close()
			t.Fatalf("start run for task %d: %v", task.Number, err)
		}
		runs = append(runs, run)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close prepared store: %v", err)
	}

	outputs := runStoreAccessHelpers(t, path, "update", func(i int) map[string]string {
		return map[string]string{"HERDR_DESK_STORE_ACCESS_RUN": strconv.FormatInt(runs[i].ID, 10)}
	}, len(runs))
	for i, output := range outputs {
		if got := strings.TrimSpace(output); got != "true" {
			t.Errorf("UpdateRun(%d) helper output = %q, want true", runs[i].ID, got)
		}
	}
}

func runStoreAccessHelpers(t *testing.T, path, mode string, extra func(int) map[string]string, count int) []string {
	t.Helper()
	type child struct {
		cmd    *exec.Cmd
		start  *os.File
		output bytes.Buffer
	}
	children := make([]*child, 0, count)
	for i := 0; i < count; i++ {
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatalf("create child %d start barrier: %v", i, err)
		}
		c := &child{start: write}
		cmd := exec.Command(os.Args[0], "-test.run=^TestStoreAccessProcessHelper$")
		cmd.ExtraFiles = []*os.File{read}
		cmd.Stdout = &c.output
		cmd.Stderr = &c.output
		cmd.Env = append(os.Environ(),
			storeAccessHelperEnv+"=1",
			"HERDR_DESK_STORE_ACCESS_PATH="+path,
			"HERDR_DESK_STORE_ACCESS_MODE="+mode,
		)
		if extra != nil {
			for key, value := range extra(i) {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
		}
		c.cmd = cmd
		if err := c.cmd.Start(); err != nil {
			read.Close()
			write.Close()
			for _, started := range children {
				started.start.Close()
				_ = started.cmd.Wait()
			}
			t.Fatalf("start child %d: %v", i, err)
		}
		read.Close()
		children = append(children, c)
	}
	for _, c := range children {
		if _, err := c.start.Write([]byte{1}); err != nil {
			t.Fatalf("release child start barrier: %v", err)
		}
		if err := c.start.Close(); err != nil {
			t.Fatalf("release child start barrier: %v", err)
		}
	}
	outputs := make([]string, len(children))
	for i, c := range children {
		if err := c.cmd.Wait(); err != nil {
			t.Fatalf("child %d failed: %v\n%s", i, err, c.output.String())
		}
		outputs[i] = c.output.String()
	}
	return outputs
}
