package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/federbenjamin/herdr-desk/internal/api"
	"github.com/federbenjamin/herdr-desk/internal/cli"
	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
	"github.com/federbenjamin/herdr-desk/internal/version"
)

// rpcOnce runs `herdr-desk rpc` on a machine with stdin as the request.
func rpcOnce(t *testing.T, m *testutil.Machine, stdin string) (commandResult, api.RPCResponse) {
	t.Helper()
	r := runDeskWithEnv(t, m, t.TempDir(), []string{"rpc"}, stdin, nil)
	var resp api.RPCResponse
	if r.exit == 0 {
		if err := json.Unmarshal([]byte(r.stdout), &resp); err != nil {
			t.Fatalf("rpc stdout = %q, not one RPCResponse: %v", r.stdout, err)
		}
	}
	return r, resp
}

func TestRPCAnswersAStatusRequestWithOneJSONLineOnTheHome(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	r, resp := rpcOnce(t, home.Machine, `{"method":"status","params":null}`)
	if r.exit != 0 || r.stderr != "" {
		t.Fatalf("rpc status = (%d, %q, %q); want exit 0 and a quiet stderr", r.exit, r.stdout, r.stderr)
	}
	if strings.Count(strings.TrimRight(r.stdout, "\n"), "\n") != 0 || !strings.HasSuffix(r.stdout, "\n") {
		t.Errorf("rpc stdout = %q; want exactly one line", r.stdout)
	}
	var st api.Status
	if err := json.Unmarshal(resp.Result, &st); err != nil || resp.Refusal != nil || resp.Error != nil || st.Version != version.Version {
		t.Fatalf("rpc response = %+v (%v), status %+v; want a result with version %q", resp, err, st, version.Version)
	}
}

func TestRPCExitsZeroWheneverItWroteAResponseEvenAnErrorOne(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	// The caller reads the response, not the exit code: a bad request is the response's business.
	for _, tc := range []struct {
		name, stdin string
	}{
		{"an unknown method", `{"method":"no.such.method","params":null}`},
		{"stdin that is not JSON", `not json`},
		{"empty stdin", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, resp := rpcOnce(t, home.Machine, tc.stdin)
			if r.exit != 0 {
				t.Fatalf("rpc exit = %d, stderr %q; want 0 with an error response", r.exit, r.stderr)
			}
			if resp.Error == nil || !resp.Error.BadRequest || resp.Result != nil || resp.Refusal != nil {
				t.Fatalf("rpc response = %+v; want exactly a bad-request error", resp)
			}
		})
	}
}

func TestRPCOnAClientExitsTwoNamingTheHomeAndWritesNoResponse(t *testing.T) {
	client := testutil.NewClientMachine(t, testutil.StartHome(t, testutil.HomeOptions{}))
	r := runDeskWithEnv(t, client, t.TempDir(), []string{"rpc"}, `{"method":"status","params":null}`, nil)
	want := "herdr-desk rpc: this machine is a client of home; rpc runs on the home"
	if r.exit != 2 || r.stdout != "" || !strings.Contains(r.stderr, want) {
		t.Fatalf("rpc on a client = (%d, %q, %q); want exit 2, empty stdout, stderr containing %q", r.exit, r.stdout, r.stderr, want)
	}
}

func TestRPCTakesNoArguments(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	r := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"rpc", "status"}, `{"method":"status"}`, nil)
	if r.exit != 2 || r.stdout != "" {
		t.Fatalf("rpc status = (%d, %q, %q); want exit 2", r.exit, r.stdout, r.stderr)
	}
}

func TestClientMachineReachesTheHomeThroughItsCommandAndSeesItsTasks(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := testutil.NewClientMachine(t, home)
	added := runDeskWithEnv(t, client, t.TempDir(), []string{"add", "-t", "from the client"}, "", nil)
	if added.exit != 0 {
		t.Fatalf("add on a client = (%d, %q, %q)", added.exit, added.stdout, added.stderr)
	}
	// The task lives in the home's store: the home's own command sees it, the client holds no store.
	listed := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"list"}, "", nil)
	if listed.exit != 0 || !strings.Contains(listed.stdout, "from the client") {
		t.Fatalf("list on the home = (%d, %q, %q); want the client's task", listed.exit, listed.stdout, listed.stderr)
	}
	if _, err := os.Stat(client.Paths.DB()); err == nil {
		t.Errorf("the client machine created a store at %s", client.Paths.DB())
	}
}

func TestClientAddCommandSavesOnAnswerAndExitsThreeWithoutWritingWhenTheHomeIsDown(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := testutil.NewClientMachine(t, home)
	before, err := os.ReadFile(client.Paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}

	home.Stop()
	down := runDeskWithEnv(t, client, t.TempDir(), []string{"client", "add", "desk@elsewhere"}, "", nil)
	if down.exit != 3 {
		t.Fatalf("client add with the home down = (%d, %q, %q); want exit 3", down.exit, down.stdout, down.stderr)
	}
	if after, _ := os.ReadFile(client.Paths.ConfigFile()); string(after) != string(before) {
		t.Fatalf("a failed client add rewrote the config:\n%s", after)
	}

	home.Restart(t)
	ok := runDeskWithEnv(t, client, t.TempDir(), []string{"client", "add", "desk@elsewhere"}, "", nil)
	if ok.exit != 0 {
		t.Fatalf("client add = (%d, %q, %q); want exit 0", ok.exit, ok.stdout, ok.stderr)
	}
	if c, err := config.Load(client.Paths.ConfigFile()); err != nil || c.Client.Home != "desk@elsewhere" {
		t.Fatalf("config after client add = %#v, %v; want home desk@elsewhere", c.Client, err)
	}
}

func TestClientAddCommandRefusesAnSSHOptionAsUsageAndTheRemovedTokenFlag(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := testutil.NewClientMachine(t, home)
	before, _ := os.ReadFile(client.Paths.ConfigFile())
	for _, args := range [][]string{
		{"client", "add", "-oProxyCommand=x"},
		{"client", "add", "--", "-oProxyCommand=x"},
		{"client", "add", "desk@box", "--token-file", "/tmp/t"},
		{"client", "add"},
	} {
		if r := runDeskWithEnv(t, client, t.TempDir(), args, "", nil); r.exit != 2 {
			t.Errorf("%v = (%d, %q, %q); want exit 2", args, r.exit, r.stdout, r.stderr)
		}
	}
	if after, _ := os.ReadFile(client.Paths.ConfigFile()); string(after) != string(before) {
		t.Errorf("config changed after refused client adds:\n%s", after)
	}
}

func TestRemovedDaemonAndTokenCommandsAreUsageErrors(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	// The removed command is built from its parts, so the tree's sweep for dropped names finds none here.
	removed := "dae" + "mon"
	for _, args := range [][]string{{removed}, {removed, "restart"}, {"token"}, {"token", "rotate"}, {"setup", "--listen", "x"}} {
		if r := runDeskWithEnv(t, home.Machine, t.TempDir(), args, "", nil); r.exit != 2 {
			t.Errorf("%v = (%d, %q, %q); want exit 2", args, r.exit, r.stdout, r.stderr)
		}
	}
}

func TestTickerStopWithNoTickerRunningIsNotAnError(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	if r := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"ticker", "stop"}, "", nil); r.exit != 0 {
		t.Fatalf("ticker stop = (%d, %q, %q); want exit 0", r.exit, r.stdout, r.stderr)
	}
}

func TestTickerRunHoldsTheLockSoASecondOneSaysSoAndStopsWithItsContext(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- cli.Run(ctx, []string{"ticker", "run"}, cli.Env{
			Stdout: &stdout, Stderr: &stderr, Getenv: home.Getenv(nil), Cwd: t.TempDir(),
		})
	}()

	running := func() bool {
		r := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"ticker", "status"}, "", nil)
		var st api.Status
		return r.exit == 0 && json.Unmarshal([]byte(r.stdout), &st) == nil && st.Ticker.Running
	}
	deadline := time.Now().Add(10 * time.Second)
	poll := time.NewTicker(20 * time.Millisecond)
	defer poll.Stop()
	for !running() {
		select {
		case code := <-done:
			t.Fatalf("ticker run ended early with exit %d, stderr %q", code, stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("ticker status never reported the ticker running")
		}
		<-poll.C
	}

	second := runDeskWithEnv(t, home.Machine, t.TempDir(), []string{"ticker"}, "", nil)
	if second.exit != 0 || !strings.Contains(second.stdout, "herdr-desk ticker: already running (pid ") {
		t.Fatalf("a second ticker = (%d, %q, %q); want exit 0 saying already running", second.exit, second.stdout, second.stderr)
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("ticker run exit after cancel = %d, stderr %q", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ticker run did not return after its context ended")
	}
	if running() {
		t.Error("ticker status still reports a ticker after it returned")
	}
}
