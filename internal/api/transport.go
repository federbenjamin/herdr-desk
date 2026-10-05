package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/runner"
)

// Transport carries one request to the home and returns its RPCResponse, encoded. An error is the request not
// reaching the home: a *model.Refusal with the code home-unreachable when it could not be reached, any other error
// when the home's answer could not be read.
type Transport interface {
	RoundTrip(ctx context.Context, method string, params []byte) ([]byte, error)
	Close() error // the local transport closes its runner and store; the command transport has nothing to close
}

// localTransport answers on the home, in this process.
type localTransport struct {
	p config.Paths
	c config.Config

	mu  sync.Mutex
	r   *runner.Runner
	srv *Server
}

// NewLocalTransport answers each request in this process. It opens the store with runner.Open on its first request,
// once, and keeps it until Close: a board refreshes several requests every few seconds.
func NewLocalTransport(p config.Paths, c config.Config) Transport {
	return &localTransport{p: p, c: c}
}

func (l *localTransport) RoundTrip(ctx context.Context, method string, params []byte) ([]byte, error) {
	l.mu.Lock()
	if l.srv == nil {
		r, err := runner.Open(l.p, l.c)
		if err != nil {
			l.mu.Unlock()
			return nil, err
		}
		l.r, l.srv = r, homeServer(r, l.p, l.c)
	}
	srv := l.srv
	l.mu.Unlock()
	return srv.RoundTrip(ctx, method, params)
}

func (l *localTransport) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.r == nil {
		return nil
	}
	err := l.r.Close()
	l.r, l.srv = nil, nil
	return err
}

// commandTransport runs the [client] command once per request.
type commandTransport struct {
	p       config.Paths
	c       config.Config
	timeout time.Duration
}

// The longest stretch of the home command's stderr an error quotes.
const maxStderr = 300

// waitDelay bounds how long a request waits for the command's pipes once the command has exited: an ssh master it
// started in the background may hold them open.
const waitDelay = time.Second

// NewCommandTransport runs c's [client] command (DefaultClientCommand when empty) for each request, with {home} and
// {control} expanded, the request on its stdin and the response read from its stdout. Each request but the untimed
// ones is bounded by timeout (0 → none).
func NewCommandTransport(p config.Paths, c config.Config, timeout time.Duration) Transport {
	return &commandTransport{p: p, c: c, timeout: timeout}
}

// untimed reports whether method runs with no per-request timeout: backup.run pushes to a remote, and runs.start may
// make a git worktree and a herdr workspace; cutting either leaves the home's work half done. ssh's connect timeout
// still bounds reaching the home.
func untimed(method string) bool { return method == MethodBackupRun || method == MethodRunsStart }

func (t *commandTransport) Close() error { return nil }

func (t *commandTransport) RoundTrip(ctx context.Context, method string, params []byte) ([]byte, error) {
	template := t.c.Client.Command
	if len(template) == 0 {
		template = config.DefaultClientCommand()
	}
	argv := config.Expand(template, map[string]string{"home": t.c.Client.Home, "control": t.p.ControlPath()})
	if argv[0] == "" {
		return nil, errors.New("[client] command names no program")
	}
	req, err := json.Marshal(RPCRequest{Version: WireVersion, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	// ssh makes its control socket in this folder and refuses one others can write.
	if err := os.MkdirAll(t.p.StateDir, 0o700); err != nil {
		return nil, err
	}
	if t.timeout > 0 && !untimed(method) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(req)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = waitDelay
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return nil, t.unreachable(ctx.Err())
	case errors.As(err, &exit) && exit.ExitCode() == 255:
		return nil, t.unreachable(fmt.Errorf("%s exited 255: %s", argv[0], tail(stderr.Bytes())))
	case errors.As(err, &exit):
		return nil, fmt.Errorf("the home command %s exited %d: %s", argv[0], exit.ExitCode(), tail(stderr.Bytes()))
	case err != nil && !errors.Is(err, exec.ErrWaitDelay):
		return nil, t.unreachable(err)
	}
	var resp RPCResponse
	if json.Unmarshal(stdout.Bytes(), &resp) != nil || (resp.Result == nil && resp.Refusal == nil && resp.Error == nil) {
		return nil, fmt.Errorf("the home command %s exited 0 without one response: %s", argv[0], tail(stderr.Bytes()))
	}
	return stdout.Bytes(), nil
}

func (t *commandTransport) unreachable(err error) error {
	return unreachable("the home at "+t.c.Client.Home, err)
}

// tail is the end of a command's stderr, at most maxStderr bytes, or a note that it wrote none.
func tail(b []byte) string {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return "no stderr"
	}
	if len(b) > maxStderr {
		b = b[len(b)-maxStderr:]
	}
	return string(b)
}

// ServeRPC reads one RPCRequest from r, answers it against the home's store, and writes one RPCResponse to w. A
// request over 1 MiB, one that does not parse, or one with a field the request does not have gets a bad-request
// error. A request of another wire version gets an error that is not a bad request, naming both versions, so a
// client keeps what it queued until the two binaries match; so does a store that cannot be opened. It returns an
// error only when the response cannot be written.
func ServeRPC(ctx context.Context, p config.Paths, c config.Config, r io.Reader, w io.Writer) error {
	resp := serveRPC(ctx, p, c, r)
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func serveRPC(ctx context.Context, p config.Paths, c config.Config, r io.Reader) RPCResponse {
	body, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	switch {
	case err != nil:
		return RPCResponse{Error: &RPCError{BadRequest: true, Message: "read the request: " + err.Error()}}
	case len(body) > maxBody:
		return RPCResponse{Error: &RPCError{BadRequest: true, Message: fmt.Sprintf("the request is over %d bytes", maxBody)}}
	}
	// The version is read first, leniently: a request of another version may carry fields this one does not know.
	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return RPCResponse{Error: &RPCError{BadRequest: true, Message: "the request does not parse: " + err.Error()}}
	}
	if version.Version != WireVersion {
		return RPCResponse{Error: &RPCError{Message: fmt.Sprintf(
			"the client speaks wire version %d and this home speaks %d: install the same herdr-desk on both", version.Version, WireVersion)}}
	}
	var req RPCRequest
	if err := decodeStrict(body, &req); err != nil {
		return RPCResponse{Error: &RPCError{BadRequest: true, Message: "the request does not parse: " + err.Error()}}
	}
	rn, err := runner.Open(p, c)
	if err != nil {
		return RPCResponse{Error: &RPCError{Message: err.Error()}}
	}
	defer rn.Close()
	return homeServer(rn, p, c).answer(ctx, req.Method, req.Params)
}

// unreachable is the refusal for a home that did not answer.
func unreachable(where string, err error) error {
	return &model.Refusal{Code: model.CodeHomeUnreachable, Msg: fmt.Sprintf("%s did not answer: %v", where, err)}
}
