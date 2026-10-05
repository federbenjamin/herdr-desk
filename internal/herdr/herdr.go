// Package herdr drives the herdr terminal workspace manager through its command line.
package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	defaultTimeout = 10 * time.Second
	maxStderr      = 300
)

// Find returns the path of the herdr binary. DESK_HERDR, when set and not empty, must be an absolute path to an
// executable regular file and is returned as is; PATH is not searched then. Unset or empty, herdr is looked up on PATH.
func Find() (string, error) {
	bin := os.Getenv("DESK_HERDR")
	if bin == "" {
		return exec.LookPath("herdr")
	}
	if !filepath.IsAbs(bin) {
		return "", fmt.Errorf("DESK_HERDR %q is not an absolute path", bin)
	}
	info, err := os.Stat(bin)
	if err != nil {
		return "", fmt.Errorf("DESK_HERDR %q: %w", bin, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("DESK_HERDR %q is not an executable file", bin)
	}
	return bin, nil
}

// Client runs herdr commands.
type Client struct {
	Bin     string        // "" → Find at each command
	Timeout time.Duration // 0 → 10s, per command
}

// Created is a new workspace and its one pane.
type Created struct{ Workspace, Pane string }

// Pane is one pane as `herdr pane list` reports it.
type Pane struct {
	ID        string // pane_id
	Workspace string // workspace_id
	Session   string // agent_session.value; "" when herdr knows no agent session for the pane
	Status    string // agent_status: idle | working | blocked | done | unknown
}

// Processes is what runs in a pane.
type Processes struct {
	Group int   // the foreground process group; 0 when herdr reports none
	PIDs  []int // the foreground processes, then the shell when herdr reports one; no duplicates
}

// CreateWorkspace runs `herdr workspace create --cwd <cwd> --label <label> --no-focus --env <e>…`; env entries are KEY=VALUE.
func (c *Client) CreateWorkspace(ctx context.Context, cwd, label string, env []string) (Created, error) {
	args := []string{"workspace", "create", "--cwd", cwd, "--label", label, "--no-focus"}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	var res struct {
		Result struct {
			Workspace struct {
				ID string `json:"workspace_id"`
			} `json:"workspace"`
			RootPane struct {
				ID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := c.json(ctx, "workspace create", args, &res); err != nil {
		return Created{}, err
	}
	out := Created{Workspace: res.Result.Workspace.ID, Pane: res.Result.RootPane.ID}
	if out.Workspace == "" || out.Pane == "" {
		return Created{}, errors.New("herdr workspace create: the answer holds no workspace or pane id")
	}
	return out, nil
}

// Run runs `herdr pane run <pane> <command>`, the command as one argument.
func (c *Client) Run(ctx context.Context, pane, command string) error {
	_, err := c.exec(ctx, "pane run", "pane", "run", pane, command)
	return err
}

// paneJSON is one pane as `herdr pane list` and `herdr pane get` print it.
type paneJSON struct {
	ID           string `json:"pane_id"`
	Workspace    string `json:"workspace_id"`
	Status       string `json:"agent_status"`
	AgentSession *struct {
		Value string `json:"value"`
	} `json:"agent_session"`
}

func (p paneJSON) pane() Pane {
	out := Pane{ID: p.ID, Workspace: p.Workspace, Status: p.Status}
	if p.AgentSession != nil {
		out.Session = p.AgentSession.Value
	}
	return out
}

// Panes runs `herdr pane list`.
func (c *Client) Panes(ctx context.Context) ([]Pane, error) {
	var res struct {
		Result struct {
			Panes *[]paneJSON `json:"panes"`
		} `json:"result"`
	}
	if err := c.json(ctx, "pane list", []string{"pane", "list"}, &res); err != nil {
		return nil, err
	}
	// An answer with no panes list is not "every pane is gone": the reconcile would hand back every live run.
	if res.Result.Panes == nil {
		return nil, errors.New("herdr pane list: the answer holds no panes list")
	}
	panes := make([]Pane, 0, len(*res.Result.Panes))
	for _, p := range *res.Result.Panes {
		panes = append(panes, p.pane())
	}
	return panes, nil
}

// Pane runs `herdr pane get <id>`. found is false, with no error, when herdr answers pane_not_found.
func (c *Client) Pane(ctx context.Context, id string) (pane Pane, found bool, err error) {
	out, errOut, err := c.run(ctx, "pane", "get", id)
	if err != nil {
		var refusal struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && json.Unmarshal(bytes.TrimSpace(errOut), &refusal) == nil && refusal.Error.Code == "pane_not_found" {
			return Pane{}, false, nil
		}
		return Pane{}, false, cmdError("pane get", err, errOut)
	}
	var res struct {
		Result struct {
			Pane *paneJSON `json:"pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return Pane{}, false, fmt.Errorf("herdr pane get: the answer is not the expected JSON: %w", err)
	}
	if res.Result.Pane == nil {
		return Pane{}, false, errors.New("herdr pane get: the answer holds no pane")
	}
	return res.Result.Pane.pane(), true, nil
}

// Processes runs `herdr pane process-info --pane <pane>`.
func (c *Client) Processes(ctx context.Context, pane string) (Processes, error) {
	var res struct {
		Result struct {
			Info *struct {
				Group     int `json:"foreground_process_group_id"`
				Processes []struct {
					PID int `json:"pid"`
				} `json:"foreground_processes"`
				ShellPID int `json:"shell_pid"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if err := c.json(ctx, "pane process-info", []string{"pane", "process-info", "--pane", pane}, &res); err != nil {
		return Processes{}, err
	}
	if res.Result.Info == nil {
		return Processes{}, errors.New("herdr pane process-info: the answer holds no process info")
	}
	info := res.Result.Info
	out := Processes{Group: info.Group}
	for _, p := range info.Processes {
		if p.PID != 0 && !slices.Contains(out.PIDs, p.PID) {
			out.PIDs = append(out.PIDs, p.PID)
		}
	}
	if info.ShellPID != 0 && !slices.Contains(out.PIDs, info.ShellPID) {
		out.PIDs = append(out.PIDs, info.ShellPID)
	}
	return out, nil
}

// ClosePane runs `herdr pane close <pane>`.
func (c *Client) ClosePane(ctx context.Context, pane string) error {
	_, err := c.exec(ctx, "pane close", "pane", "close", pane)
	return err
}

func (c *Client) json(ctx context.Context, name string, args []string, into any) error {
	out, err := c.exec(ctx, name, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, into); err != nil {
		return fmt.Errorf("herdr %s: the answer is not the expected JSON: %w", name, err)
	}
	return nil
}

func (c *Client) exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, errOut, err := c.run(ctx, args...)
	if err != nil {
		return nil, cmdError(name, err, errOut)
	}
	return out, nil
}

// run runs herdr with args and returns its stdout and stderr; an error is not yet named for its subcommand.
func (c *Client) run(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	bin := c.Bin
	if bin == "" {
		if bin, err = Find(); err != nil {
			return nil, nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("timed out after %s", timeout)
		}
		return nil, errOut.Bytes(), err
	}
	return out.Bytes(), errOut.Bytes(), nil
}

// cmdError names the subcommand and quotes at most 300 bytes of its stderr.
func cmdError(name string, err error, stderr []byte) error {
	msg := strings.TrimSpace(string(stderr))
	if len(msg) > maxStderr {
		msg = strings.ToValidUTF8(msg[:maxStderr], "")
	}
	if msg != "" {
		return fmt.Errorf("herdr %s: %w: %s", name, err, msg)
	}
	return fmt.Errorf("herdr %s: %w", name, err)
}
