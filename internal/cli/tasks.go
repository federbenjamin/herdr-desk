package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/board"
	"github.com/federbenjamin/herdr-desk/internal/gitcmd"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/store"
)

const gitTimeout = 10 * time.Second

// timeFormat is how herdr-desk prints a time: UTC, to the minute, with a Z.
const timeFormat = "2006-01-02 15:04Z"

// mainCheckout returns the main checkout of the git repo dir is in, "" when git says dir is in none. A worktree
// resolves to its main checkout. Any other git failure is an error, so a task never loses its project to it.
func (a *app) mainCheckout(dir string) (string, error) {
	if _, err := os.Stat(dir); dir == "" || errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	common, err := gitcmd.Run(a.ctx, dir, gitTimeout, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if gitcmd.IsNotRepo(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find the project of %s (pass --desk for no project): %w", dir, err)
	}
	if common == "" {
		return "", nil
	}
	return filepath.Dir(common), nil
}

// projectArg turns a -p value into what the store takes: an absolute directory becomes its main checkout
// when it is in a git repo; a bare name is passed on as it is.
func (a *app) projectArg(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return p, nil
	}
	p = filepath.Clean(p)
	top, err := a.mainCheckout(p)
	if top == "" || err != nil {
		return p, err
	}
	return top, nil
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
			in.Project, err = a.projectArg(project)
		default:
			in.Project, err = a.mainCheckout(a.env.Cwd)
		}
		if err != nil {
			return err
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
		a.refreshSessionView(cmd, c, actor.Session)
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

// listProject resolves a -p value the way add does (model.ResolveProject). The known projects are every task's
// on the home. Offline they are those of the whole snapshot, tl, which holds live tasks only: a bare name none
// of them carries may still be a project the home knows from a done task, so it is kept as it is, and since a
// stored project is never a bare name, the list is empty rather than refused. Any other refusal stands.
func (a *app) listProject(c *api.Client, project string, tl api.TaskList, all bool) (string, error) {
	project, err := a.projectArg(project)
	if err != nil || project == "" || filepath.IsAbs(project) {
		return project, err
	}
	tasks := tl.Tasks
	if !tl.Offline && !all {
		every, err := c.ListTasks(a.ctx, store.Filter{All: true})
		if err != nil {
			return "", err
		}
		tasks = every.Tasks
	}
	known := make([]string, 0, len(tasks))
	for _, t := range tasks {
		known = append(known, t.Project)
	}
	resolved, err := model.ResolveProject(project, known)
	bare := !strings.ContainsRune(project, filepath.Separator)
	if tl.Offline && bare && !slices.ContainsFunc(known, func(p string) bool { return p != "" && filepath.Base(p) == project }) {
		return project, nil
	}
	return resolved, err
}

// filterProject narrows tasks to one project with store.Filter.Match.
func filterProject(f store.Filter, project string, tasks []model.Task) []model.Task {
	f.Project = &project
	out := []model.Task{}
	for _, t := range tasks {
		if f.Match(t) {
			out = append(out, t)
		}
	}
	return out
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
		// A bare -p name is resolved over the whole board, so a live filter asks for the zero Filter (the
		// same call ListTasks makes for it, and what the snapshot holds) and is applied below.
		query := f
		if f.Live() && project != "" && !filepath.IsAbs(project) {
			query = store.Filter{}
		}
		tl, err := c.ListTasks(a.ctx, query)
		if err != nil {
			return err
		}
		if noProject || cmd.Flags().Changed("project") {
			p := ""
			if !noProject {
				if p, err = a.listProject(c, project, tl, all); err != nil {
					return err
				}
			}
			tl.Tasks = filterProject(f, p, tl.Tasks)
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
		when = tl.SnapshotTS.UTC().Format(timeFormat)
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
			a.say("e%d  %s  %s  %s  %s", e.ID, e.TS.UTC().Format(timeFormat), e.Who, e.Kind, e.Data)
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
		a.refreshSessionView(cmd, c, actor.Session)
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
	var title, notes, appendNotes string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "edit <task>",
		Short: "Replace a task's title or notes, or add a line to its notes",
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
		appending := cmd.Flags().Changed("append-notes")
		if p.Title == nil && p.Notes == nil && !appending {
			return usage("give --title, --notes, or --append-notes")
		}
		if appending && strings.TrimSpace(appendNotes) == "" {
			return &model.Refusal{Code: model.CodeEmptyText, Msg: "--append-notes needs text"}
		}
		actor, err := a.actor()
		if err != nil {
			return err
		}
		c, err := a.client()
		if err != nil {
			return err
		}
		var t model.Task
		if appending {
			t, err = a.appendNotes(c, actor, n, p, appendNotes)
		} else {
			t, err = c.SetTask(a.ctx, actor, n, p)
		}
		if err != nil {
			return err
		}
		a.refreshSessionView(cmd, c, actor.Session)
		if asJSON {
			return a.printJSON(t)
		}
		a.say("T%d", t.Number)
		return nil
	})
	cmd.Flags().StringVar(&title, "title", "", "the new title")
	cmd.Flags().StringVar(&notes, "notes", "", "the new notes; replaces the old ones")
	cmd.Flags().StringVar(&appendNotes, "append-notes", "", "a line to add to the end of the notes")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the task as JSON")
	cmd.MarkFlagsMutuallyExclusive("notes", "append-notes")
	return cmd
}

// appendNotes reads task n and writes p with text on a new line after its notes, naming the notes it read.
// When they changed in between (stale) it reads once more and tries once more.
func (a *app) appendNotes(c *api.Client, actor store.Actor, n int, p model.Patch, text string) (model.Task, error) {
	var err error
	for range 2 {
		var d store.TaskDetail
		if d, err = c.GetTask(a.ctx, n); err != nil {
			return model.Task{}, err
		}
		old, notes := d.Task.Notes, text
		if old != "" {
			notes = old + "\n" + text
		}
		p.Notes, p.NotesWere = &notes, &old
		var t model.Task
		t, err = c.SetTask(a.ctx, actor, n, p)
		if r, ok := model.AsRefusal(err); !ok || r.Code != model.CodeStale {
			return t, err
		}
	}
	return model.Task{}, err
}

// stepOp reads the words after `herdr-desk steps <task>`.
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

// captureLine adds the task one captured line names and prints it.
func (a *app) captureLine(cmd *cobra.Command, line string, asJSON bool) error {
	actor, err := a.actor()
	if err != nil {
		return err
	}
	c, err := a.client()
	if err != nil {
		return err
	}
	t, err := c.AddTask(a.ctx, actor, store.AddTaskInput{TaskData: model.ParseCapture(line)})
	if err != nil {
		return err
	}
	a.refreshSessionView(cmd, c, actor.Session)
	if asJSON {
		return a.printJSON(t)
	}
	a.say("T%d", t.Number)
	return nil
}

// capturePopup runs the capture popup and prints the task that lands.
func (a *app) capturePopup() error {
	c, err := a.client()
	if err != nil {
		return err
	}
	o, err := a.boardOptions(c)
	if err != nil {
		return err
	}
	t, err := board.Capture(a.ctx, o)
	if err != nil {
		return err
	}
	if t != nil {
		a.say("T%d", t.Number)
	}
	return nil
}

func (a *app) captureCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "capture",
		Short: "Add a task from one line on stdin (#thread, @project)",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.do(func(cmd *cobra.Command, _ []string) error {
		if a.interactive(asJSON) {
			return a.capturePopup()
		}
		in := bufio.NewReader(a.env.Stdin)
		// On a terminal the command may run in a pane that closes when it exits, so a refused line is shown and
		// asked for again. Anywhere else a refusal ends the command, as it does for every other one.
		for {
			if a.env.StdinTTY {
				fmt.Fprint(a.env.Stderr, "capture: ")
			}
			line, err := in.ReadString('\n')
			if err != nil && err != io.EOF {
				return err
			}
			if strings.TrimSpace(line) == "" {
				return nil
			}
			err = a.captureLine(cmd, line, asJSON)
			if err == nil || !a.env.StdinTTY {
				return err
			}
			fmt.Fprintf(a.env.Stderr, "%s: %s\n", cmd.CommandPath(), err)
		}
	})
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the task as JSON")
	return cmd
}
