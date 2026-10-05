package setup_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
	"github.com/federbenjamin/herdr-desk/internal/model"
	"github.com/federbenjamin/herdr-desk/internal/setup"
	"github.com/federbenjamin/herdr-desk/internal/testutil"
)

// recordingClient makes a client machine whose [client] command records the {home} it was expanded with and the
// request it carried, then forwards the request to the home's rpc helper. It returns the machine and the two
// record paths.
func recordingClient(t *testing.T, home *testutil.Home) (m *testutil.Machine, homeRec, reqRec string) {
	t.Helper()
	m = testutil.NewMachine(t)
	root := filepath.Dir(filepath.Dir(home.Paths.ConfigDir))
	homeRec = filepath.Join(m.Paths.StateDir, "home.rec")
	reqRec = filepath.Join(m.Paths.StateDir, "req.rec")
	if err := os.MkdirAll(m.Paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Runner.Cap = 5
	cfg.Client.Home = "old-home"
	cfg.Client.Command = []string{
		"sh", "-c", `echo "$0" > "$1"; tee "$2" | env "$3" "$4" -test.run='^$'`,
		"{home}", homeRec, reqRec, "DESK_TESTUTIL_RPC=" + root, os.Args[0],
	}
	if err := cfg.Save(m.Paths.ConfigFile()); err != nil {
		t.Fatal(err)
	}
	return m, homeRec, reqRec
}

func TestClientAddSendsStatusToTheNewHomeAndSavesOnlyAfterItAnswers(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client, homeRec, reqRec := recordingClient(t, home)

	if err := setupClientAdd(t, client, "desk@new-box"); err != nil {
		t.Fatalf("ClientAdd = %v; want nil", err)
	}

	// The probe went through the NEW home, not the one the config held before.
	if b, err := os.ReadFile(homeRec); err != nil || strings.TrimSpace(string(b)) != "desk@new-box" {
		t.Errorf("command ran with {home} = %q, %v; want desk@new-box", b, err)
	}
	var req struct {
		Method string `json:"method"`
	}
	if b, err := os.ReadFile(reqRec); err != nil || json.Unmarshal(b, &req) != nil || req.Method != "status" {
		t.Errorf("command carried %q, %v; want a status request", b, err)
	}

	got, err := config.Load(client.Paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if got.Client.Home != "desk@new-box" {
		t.Errorf("saved client.home = %q; want desk@new-box", got.Client.Home)
	}
	// Only the home changes: the command template and every other setting survive the save.
	if got.Runner.Cap != 5 || len(got.Client.Command) == 0 || got.Client.Command[0] != "sh" {
		t.Errorf("saved config = %#v; want the cap and command kept", got)
	}
}

func TestClientAddWithAnUnreachableHomeSavesNothingAndRefusesAsUnreachable(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client := testutil.NewClientMachine(t, home)
	before, err := os.ReadFile(client.Paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	home.Stop()

	err = setupClientAdd(t, client, "other-home")
	var ref *model.Refusal
	if !errors.As(err, &ref) || ref.Code != model.CodeHomeUnreachable {
		t.Fatalf("ClientAdd with the home down = %v; want a home-unreachable refusal", err)
	}
	if after, err := os.ReadFile(client.Paths.ConfigFile()); err != nil || string(after) != string(before) {
		t.Fatalf("config after a failed add = %q, %v; want it untouched", after, err)
	}
}

func TestClientAddThroughACommandThatFailsLeavesTheConfigUnrewritten(t *testing.T) {
	m := testutil.NewMachine(t)
	if err := os.MkdirAll(m.Paths.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Paths.ConfigFile(), []byte("[client]\ncommand = [\"false\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(m.Paths.ConfigFile())

	if err := setupClientAdd(t, m, "desk@box"); err == nil {
		t.Fatal("ClientAdd through a failing command = nil; want an error")
	}
	got, err := config.Load(m.Paths.ConfigFile())
	after, _ := os.Stat(m.Paths.ConfigFile())
	if err != nil || got.Client.Home != "" || !after.ModTime().Equal(info.ModTime()) {
		t.Fatalf("config after a failed add = %#v, %v; want no home and no rewrite", got.Client, err)
	}
}

func TestClientAddRefusesAnInvalidTargetAsBadInputBeforeAnyRequest(t *testing.T) {
	home := testutil.StartHome(t, testutil.HomeOptions{})
	client, homeRec, _ := recordingClient(t, home)
	before, _ := os.ReadFile(client.Paths.ConfigFile())

	for _, target := range []string{"-oProxyCommand=x", "two words", "a\nb", strings.Repeat("h", 256)} {
		err := setupClientAdd(t, client, target)
		var ref *model.Refusal
		if !errors.As(err, &ref) || ref.Code != model.CodeBadInput {
			t.Errorf("ClientAdd(%q) = %v; want a bad-input refusal", target, err)
		}
	}
	// A rejected target never reaches the command: ssh would read "-o…" as an option.
	if _, err := os.Stat(homeRec); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the [client] command ran for an invalid target (%v)", err)
	}
	if after, _ := os.ReadFile(client.Paths.ConfigFile()); string(after) != string(before) {
		t.Errorf("config changed after invalid targets:\n%s", after)
	}
}

func TestClientAddRefusesAConfigWithARemovedKeyWithoutWritingIt(t *testing.T) {
	m := testutil.NewMachine(t)
	body := []byte("[home]\nlisten = \"127.0.0.1:7411\"\n")
	if err := os.MkdirAll(m.Paths.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.Paths.ConfigFile(), body, 0o600); err != nil {
		t.Fatal(err)
	}
	err := setupClientAdd(t, m, "desk@box")
	if err == nil || !strings.Contains(err.Error(), "unknown key home") {
		t.Fatalf("ClientAdd over a [home] config = %v; want an error naming the key", err)
	}
	if after, _ := os.ReadFile(m.Paths.ConfigFile()); string(after) != string(body) {
		t.Errorf("config rewritten to %q", after)
	}
}

func setupClientAdd(t *testing.T, m *testutil.Machine, home string) error {
	t.Helper()
	return setup.ClientAdd(context.Background(), m.Paths, home)
}
