package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/desk/internal/api"
	"github.com/federbenjamin/desk/internal/model"
	"github.com/federbenjamin/desk/internal/store"
)

const gitTimeout = 10 * time.Second

// mainCheckout returns the main checkout of the git repo dir is in, "" when dir is in none. A worktree
// resolves to its main checkout.
func (a *app) mainCheckout(dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(a.ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return ""
	}
	return filepath.Dir(common)
}

// projectArg turns a -p value into what the store takes: an absolute directory becomes its main checkout
// when it is in a git repo; a bare name is passed on for the store to resolve.
func (a *app) projectArg(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	p = filepath.Clean(p)
	if top := a.mainCheckout(p); top != "" {
		return top
	}
	return p
}

// taskLine is a task's one-line form: T<n>  <status>  <title>  #<thread>  <project base name>.
func taskLine(t model.Task) string {
	s := fmt.Sprintf("T%d  %s  %s", t.Number, t.Status, t.Title)
	if t.Thread != "" {
		s += "  #" + t.Thread
	}
	if t.Project != "" {
		s += "  " + filepath.Base(t.Project)
	}
	return s
}

func (a *app) addCmd() *cobra.Command {
	var title, notes, project, thread, status, branch string
	var noProject, asJSON bool
	var tags []string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a task",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, _ []string) error {
		actor, err := a.actor()
		if err != nil {
			return err
		}
		in := store.AddTaskInput{
			TaskData: model.TaskData{Title: title, Notes: notes, Thread: thread, Status: model.Status(status)},
			Tags:     slices.Clone(tags),
		}
		switch {
		case noProject:
		case cmd.Flags().Changed("project"):
			in.Project = a.projectArg(project)
		default:
			in.Project = a.mainCheckout(a.env.Cwd)
		}
		if branch != "" {
			in.Tags = append(in.Tags, model.BranchTag(branch))
		}
		c, err := a.client()
		if err != nil {
			return err
		}
		t, err := c.AddTask(a.ctx, actor, in)
		if err != nil {
			return err
		}
		if asJSON {
			return a.printJSON(t)
		}
		a.say("T%d", t.Number)
		return nil
	})
	f := cmd.Flags()
	f.StringVarP(&title, "title", "t", "", "the title")
	f.StringVarP(&notes, "notes", "n", "", "the notes")
	f.StringVarP(&project, "project", "p", "", "an absolute directory, or the bare name of a known project")
	f.BoolVar(&noProject, "desk", false, "no project")
	f.StringVar(&thread, "thread", "", "the thread (agent proposes)")
	f.StringVar(&status, "status", "", "the starting status (default open)")
	f.StringArrayVar(&tags, "tag", nil, "a tag; repeatable")
	f.StringVar(&branch, "branch", "", "the branch the task belongs to (adds the tag branch:<b>)")
	f.BoolVar(&asJSON, "json", false, "print the task as JSON")
	cmd.MarkFlagsMutuallyExclusive("project", "desk")
	return cmd
}

// listFilter is the filter list's flags name. The project is left to filterProject.
func listFilter(ready, open, done, archived, all bool) store.Filter {
	switch {
	case ready:
		return store.Filter{Statuses: []model.Status{model.StatusReady}}
	case open:
		return store.Filter{Statuses: []model.Status{model.StatusOpen}}
	case done:
		return store.Filter{Statuses: []model.Status{model.StatusDone}}
	case archived:
		return store.Filter{Archived: true}
	case all:
		return store.Filter{All: true}
	}
	return store.Filter{}
}

// filterProject narrows tasks to one project with store.Filter.Match. A bare name is the one project among
// these tasks with that base name; several is unknown-project.
func filterProject(f store.Filter, project string, tasks []model.Task) ([]model.Task, error) {
	if !filepath.IsAbs(project) && project != "" {
		var found []string
		for _, t := range tasks {
			if t.Project != "" && filepath.Base(t.Project) == project && !slices.Contains(found, t.Project) {
				found = append(found, t.Project)
			}
		}
		if len(found) > 1 {
			return nil, &model.Refusal{Code: model.CodeUnknownProject,
				Msg: fmt.Sprintf("%d projects are named %q; give the path", len(found), project)}
		}
		if len(found) == 1 {
			project = found[0]
		}
	}
	f.Project = &project
	out := []model.Task{}
	for _, t := range tasks {
		if f.Match(t) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (a *app) listCmd() *cobra.Command {
	var ready, open, done, archived, all, noProject, asJSON bool
	var project string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks (default: the five live statuses, every project)",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, _ []string) error {
		f := listFilter(ready, open, done, archived, all)
		c, err := a.client()
		if err != nil {
			return err
		}
		tl, err := c.ListTasks(a.ctx, f)
		if err != nil {
			return err
		}
		if noProject || cmd.Flags().Changed("project") {
			p := ""
			if !noProject {
				p = a.projectArg(project)
			}
			if tl.Tasks, err = filterProject(f, p, tl.Tasks); err != nil {
				return err
			}
		}
		if tl.Offline {
			a.warnOffline(cmd, tl)
		}
		if asJSON {
			return a.printJSON(tl)
		}
		for _, t := range tl.Tasks {
			a.say("%s", taskLine(t))
		}
		return nil
	})
	fl := cmd.Flags()
	fl.BoolVar(&ready, "ready", false, "ready tasks only")
	fl.BoolVar(&open, "open", false, "open tasks only")
	fl.BoolVar(&done, "done", false, "done tasks only")
	fl.BoolVar(&archived, "archived", false, "archived tasks only")
	fl.BoolVar(&all, "all", false, "every task")
	fl.StringVarP(&project, "project", "p", "", "an absolute directory, or the bare name of a project")
	fl.BoolVar(&noProject, "desk", false, "tasks with no project")
	fl.BoolVar(&asJSON, "json", false, "print a TaskList as JSON")
	cmd.MarkFlagsMutuallyExclusive("ready", "open", "done", "archived", "all")
	cmd.MarkFlagsMutuallyExclusive("project", "desk")
	return cmd
}

func (a *app) warnOffline(cmd *cobra.Command, tl api.TaskList) {
	when := "an unknown time"
	if tl.SnapshotTS != nil {
		when = tl.SnapshotTS.Local().Format(time.DateTime)
	}
	a.warn(cmd, "the home did not answer; showing the snapshot from %s", when)
}

func (a *app) showCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <task>",
		Short: "Show one task with its steps and history",
		Args:  cobra.ExactArgs(1),
	}
	cmd.RunE = a.do(func(_ *cobra.Command, args []string) error {
		n, err := parseTask(args[0])
		if err != nil {
			return err
		}
		c, err := a.client()
		if err != nil {
			return err
		}
		d, err := c.GetTask(a.ctx, n)
		if err != nil {
			return err
		}
		if asJSON {
			return a.printJSON(d)
		}
		a.showTask(d)
		return nil
	})
	cmd.Flags().BoolVar(&asJSON, "json", false, "print a TaskDetail as JSON")
	return cmd
}

func (a *app) showTask(d store.TaskDetail) {
	t := d.Task
	a.say("%s", taskLine(t))
	for _, f := range []struct{ name, value string }{
		{"project", t.Project}, {"root", t.Root}, {"isolation", t.Isolation}, {"model", t.Model},
	} {
		if f.value != "" {
			a.say("%s: %s", f.name, f.value)
		}
	}
	if t.Archived {
		a.say("archived")
	}
	if t.Notes != "" {
		a.say("\n%s", t.Notes)
	}
	if len(t.Steps) > 0 {
		a.say("\nsteps:")
		a.printSteps(t.Steps)
	}
	if len(d.History) > 0 {
		a.say("\nhistory:")
		for _, e := range d.History {
			a.say("e%d  %s  %s  %s  %s", e.ID, e.TS.UTC().Format("2006-01-02 15:04Z"), e.Who, e.Kind, e.Data)
		}
	}
}

func (a *app) printSteps(steps []model.Step) {
	for _, s := range steps {
		mark := " "
		if s.Done {
			mark = "x"
		}
		a.say("%s [%s] %s", s.ShortID, mark, s.Text)
	}
}

func (a *app) setCmd() *cobra.Command {
	var thread, root, isolation, mdl, ref string
	var archive, unarchive, merged, asJSON bool
	cmd := &cobra.Command{
		Use:   "set <task> [<status>]",
		Short: "Change a task's status or fields",
		Args:  cobra.RangeArgs(1, 2),
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, args []string) error {
		n, err := parseTask(args[0])
		if err != nil {
			return err
		}
		actor, err := a.actor()
		if err != nil {
			return err
		}
		p := model.Patch{Ref: ref, Merged: merged}
		if len(args) == 2 {
			st := model.Status(args[1])
			p.Status = &st
		}
		changed := cmd.Flags().Changed
		for _, f := range []struct {
			name  string
			value *string
			dst   **string
		}{
			{"thread", &thread, &p.Thread}, {"root", &root, &p.Root},
			{"isolation", &isolation, &p.Isolation}, {"model", &mdl, &p.Model},
		} {
			if changed(f.name) {
				*f.dst = f.value
			}
		}
		if archive || unarchive {
			p.Archived = &archive
		}
		c, err := a.client()
		if err != nil {
			return err
		}
		t, err := c.SetTask(a.ctx, actor, n, p)
		if err != nil {
			return err
		}
		if asJSON {
			return a.printJSON(t)
		}
		a.say("T%d %s", t.Number, t.Status)
		return nil
	})
	f := cmd.Flags()
	f.StringVar(&thread, "thread", "", "the thread")
	f.StringVar(&root, "root", "", "the root the task runs in")
	f.StringVar(&isolation, "isolation", "", "self, worktree, or in-place")
	f.StringVar(&mdl, "model", "", "the model a run uses")
	f.BoolVar(&archive, "archive", false, "archive the task")
	f.BoolVar(&unarchive, "unarchive", false, "bring the task back from the archive")
	f.StringVar(&ref, "ref", "", "a file or PR this change is about")
	f.BoolVar(&merged, "merged", false, "the PR is merged: review writes the status runner.on_merged names")
	f.BoolVar(&asJSON, "json", false, "print the task as JSON")
	cmd.MarkFlagsMutuallyExclusive("archive", "unarchive")
	return cmd
}

func (a *app) editCmd() *cobra.Command {
	var title, notes string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "edit <task>",
		Short: "Replace a task's title or notes",
		Args:  cobra.ExactArgs(1),
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, args []string) error {
		n, err := parseTask(args[0])
		if err != nil {
			return err
		}
		var p model.Patch
		if cmd.Flags().Changed("title") {
			p.Title = &title
		}
		if cmd.Flags().Changed("notes") {
			p.Notes = &notes
		}
		if p.Title == nil && p.Notes == nil {
			return usage("give --title or --notes")
		}
		actor, err := a.actor()
		if err != nil {
			return err
		}
		c, err := a.client()
		if err != nil {
			return err
		}
		t, err := c.SetTask(a.ctx, actor, n, p)
		if err != nil {
			return err
		}
		if asJSON {
			return a.printJSON(t)
		}
		a.say("T%d", t.Number)
		return nil
	})
	cmd.Flags().StringVar(&title, "title", "", "the new title")
	cmd.Flags().StringVar(&notes, "notes", "", "the new notes; replaces the old ones")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the task as JSON")
	return cmd
}

// stepOp reads the words after `desk steps <task>`.
func stepOp(args []string) (model.StepOp, error) {
	op, rest := args[0], args[1:]
	switch {
	case op == "add" && len(rest) > 0:
		return model.StepOp{Op: op, Text: strings.Join(rest, " ")}, nil
	case (op == "toggle" || op == "remove") && len(rest) == 1:
		return model.StepOp{Op: op, ShortID: rest[0]}, nil
	case op == "rename" && len(rest) > 1:
		return model.StepOp{Op: op, ShortID: rest[0], Text: strings.Join(rest[1:], " ")}, nil
	}
	return model.StepOp{}, usage("use: add <text> · toggle <id> · rename <id> <text> · remove <id>")
}

func (a *app) stepsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "steps <task> add <text> | toggle <id> | rename <id> <text> | remove <id>",
		Short: "Change a task's steps",
		Args:  cobra.MinimumNArgs(2),
	}
	cmd.RunE = a.do(func(_ *cobra.Command, args []string) error {
		n, err := parseTask(args[0])
		if err != nil {
			return err
		}
		op, err := stepOp(args[1:])
		if err != nil {
			return err
		}
		actor, err := a.actor()
		if err != nil {
			return err
		}
		c, err := a.client()
		if err != nil {
			return err
		}
		t, err := c.Step(a.ctx, actor, n, op)
		if err != nil {
			return err
		}
		if asJSON {
			return a.printJSON(t)
		}
		a.printSteps(t.Steps)
		return nil
	})
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the task as JSON")
	return cmd
}

// parseCapture splits a captured line: #word sets the thread, @word the project, the rest is the title.
func parseCapture(line string) model.TaskData {
	var d model.TaskData
	var title []string
	for _, w := range strings.Fields(line) {
		switch {
		case len(w) > 1 && w[0] == '#':
			d.Thread = w[1:]
		case len(w) > 1 && w[0] == '@':
			d.Project = w[1:]
		default:
			title = append(title, w)
		}
	}
	d.Title = strings.Join(title, " ")
	return d
}

func (a *app) captureCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Add a task from one line on stdin (#thread, @project)",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(_ *cobra.Command, _ []string) error {
		if a.env.StdinTTY {
			fmt.Fprint(a.env.Stderr, "capture: ")
		}
		line, err := bufio.NewReader(a.env.Stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		if strings.TrimSpace(line) == "" {
			return nil
		}
		actor, err := a.actor()
		if err != nil {
			return err
		}
		c, err := a.client()
		if err != nil {
			return err
		}
		t, err := c.AddTask(a.ctx, actor, store.AddTaskInput{TaskData: parseCapture(line)})
		if err != nil {
			return err
		}
		if asJSON {
			return a.printJSON(t)
		}
		a.say("T%d", t.Number)
		return nil
	})
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the task as JSON")
	return cmd
}
