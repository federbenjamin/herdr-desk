package cli

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
	"github.com/federbenjamin/herdr-desk/internal/worker"
)

// workerCmd is what a runner's pane runs: it becomes the worker session for the run in DESK_RUN.
func (a *app) workerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Become the worker session for the run in $DESK_RUN (started by the runner)",
		Args:  cobra.NoArgs,
		RunE: a.do(func(_ *cobra.Command, _ []string) error {
			runID := a.runID()
			if runID == 0 {
				return usage("DESK_RUN is not set to a run id; the runner sets it")
			}
			task, err := parseTask(a.env.Getenv("DESK_TASK"))
			if err != nil {
				return usage("DESK_TASK is not set to a task id; the runner sets it")
			}
			actor, err := a.actor()
			if err != nil {
				return err
			}
			c, err := a.client()
			if err != nil {
				return err
			}
			run, err := a.workerRun(c, runID, task, actor.Session)
			if err != nil {
				return err
			}
			d, err := c.GetTask(a.ctx, task)
			if err != nil {
				return err
			}
			cfg, err := a.config()
			if err != nil {
				return err
			}
			argv := config.Expand(cfg.Agent.Worker, map[string]string{
				"model": run.Model, "session": run.Session, "message": worker.FirstMessage(d),
			})
			bin, err := agentBinary("worker", argv)
			if err != nil {
				return a.workerBlocked(c, actor, task, err)
			}
			return a.execAgent(bin, argv)
		}),
	}
}

// execAgent replaces this process with the agent argv names; bin is its first word's path.
func (a *app) execAgent(bin string, argv []string) error {
	exe := a.env.Exec
	if exe == nil {
		exe = execProcess
	}
	if err := exe(bin, argv); err != nil {
		return &exitError{code: exitIO, msg: fmt.Sprintf("cannot start %s: %v", argv[0], err)}
	}
	return nil
}

// workerRun returns the run with this id when it is running on the task under the session; anything else is no-run.
func (a *app) workerRun(c *api.Client, id int64, task int, session string) (model.Run, error) {
	runs, err := a.liveRuns(c)
	if err != nil {
		return model.Run{}, err
	}
	for _, r := range runs {
		if r.ID == id {
			if r.State == model.RunRunning && r.Task == task && r.Session != "" && r.Session == session {
				return r, nil
			}
			break
		}
	}
	return model.Run{}, &model.Refusal{Code: model.CodeNoRun, Msg: fmt.Sprintf("run %d is not a running run of T%d for this session", id, task)}
}

// agentBinary returns the path of the first word of the [agent] <role> template, or why there is none.
func agentBinary(role string, argv []string) (string, error) {
	if len(argv) == 0 || argv[0] == "" {
		return "", fmt.Errorf("%s: cannot start: no [agent] %s template is set", role, role)
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return "", fmt.Errorf("%s: cannot start %s", role, argv[0])
	}
	return bin, nil
}

// workerBlocked records why the worker cannot start, blocks the task, and exits 3.
func (a *app) workerBlocked(c *api.Client, actor store.Actor, task int, cause error) error {
	in := store.NoteInput{NoteData: model.NoteData{Text: cause.Error()}, Task: task}
	if _, _, err := c.Append(a.ctx, api.AppendRequest{Actor: actor, Kind: model.KindNote, Note: &in}); err != nil {
		return err
	}
	blocked := model.StatusBlocked
	if _, err := c.SetTask(a.ctx, actor, task, model.Patch{Status: &blocked}); err != nil {
		return err
	}
	return &exitError{code: exitIO, msg: cause.Error()}
}
