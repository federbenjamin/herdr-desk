package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
)

func TestDefaultClientCommandIsThePlansArgvVerbatim(t *testing.T) {
	t.Parallel()

	want := []string{
		"ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
		"-o", "ControlMaster=auto", "-o", "ControlPath={control}", "-o", "ControlPersist=60",
		"{home}", "herdr-desk", "rpc",
	}
	if got := config.DefaultClientCommand(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultClientCommand() = %q; want %q", got, want)
	}
}

func TestDefaultClientCommandReturnsACopyNoCallerCanCorrupt(t *testing.T) {
	t.Parallel()

	first := config.DefaultClientCommand()
	first[0] = "scp"
	first[len(first)-1] = "evil"
	if got := config.DefaultClientCommand(); got[0] != "ssh" || got[len(got)-1] != "rpc" {
		t.Fatalf("a caller's edit of one result leaked into the next: %q", got)
	}
}

func TestLoadKeepsAClientsHomeAndCommandAndLeavesAnAbsentCommandEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	withCommand := filepath.Join(dir, "with.toml")
	body := "[client]\nhome = \"desk@box\"\ncommand = [\"ssh\", \"{home}\", \"herdr-desk\", \"rpc\"]\n"
	if err := os.WriteFile(withCommand, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(withCommand)
	if err != nil {
		t.Fatalf("Load(client with command) = %v", err)
	}
	if c.Client.Home != "desk@box" || !reflect.DeepEqual(c.Client.Command, []string{"ssh", "{home}", "herdr-desk", "rpc"}) || !c.IsClient() {
		t.Errorf("Client = %#v, IsClient %v; want the file's home and command", c.Client, c.IsClient())
	}

	// A config that sets no command stores none: the client falls back to DefaultClientCommand at use.
	without := filepath.Join(dir, "without.toml")
	if err := os.WriteFile(without, []byte("[client]\nhome = \"desk@box\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = config.Load(without)
	if err != nil || len(c.Client.Command) != 0 || c.Client.Home != "desk@box" {
		t.Errorf("Load(client without command) = %#v, %v; want an empty Command", c.Client, err)
	}

	roundTrip := filepath.Join(dir, "saved.toml")
	if err := c.Save(roundTrip); err != nil {
		t.Fatal(err)
	}
	if back, err := config.Load(roundTrip); err != nil || back.Client.Home != "desk@box" {
		t.Errorf("Save then Load = %#v, %v; want home desk@box", back.Client, err)
	}
}

func TestValidateChecksClientHomeAsOneSSHArgument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		home string
		ok   bool
	}{
		{"empty is a home", "", true},
		{"a bare host", "box", true},
		{"user at host", "desk@box.example.com", true},
		{"an ssh config alias with a dash inside", "my-desk", true},
		{"exactly 255 bytes", strings.Repeat("a", 255), true},
		// ssh would read a leading dash as an option: -oProxyCommand=… runs a local command.
		{"a leading dash is an ssh option", "-oProxyCommand=touch /tmp/x", false},
		{"a lone dash", "-", false},
		{"256 bytes", strings.Repeat("a", 256), false},
		{"a space", "desk box", false},
		{"a tab", "desk\tbox", false},
		{"a newline", "desk\nbox", false},
		{"a NUL", "desk\x00box", false},
		{"an escape", "desk\x1bbox", false},
		{"a non-breaking space", "desk\u00a0box", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := config.Default()
			c.Client.Home = tc.home
			err := c.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate(home %q) = %v; want nil", tc.home, err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("Validate(home %q) = nil; want an error", tc.home)
				}
				if !strings.Contains(err.Error(), "client.home") {
					t.Errorf("Validate(home %q) = %q; want it to name client.home", tc.home, err)
				}
			}
		})
	}
}

func TestLoadRefusesABadClientHomeNamingTheFileAndTheKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[client]\nhome = \"-oProxyCommand=x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "client.home") {
		t.Fatalf("Load(bad client home) = %v; want an error naming the file and client.home", err)
	}
}

func TestLoadFailsNamingEachRemovedKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		key  string
	}{
		{"the [home] table", "[home]\nlisten = \"127.0.0.1:7411\"\n", "home"},
		{"an unknown client key", "[client]\ntoken_file = \"/x\"\n", "client.token_file"},
		{"an unknown top-level key", "listen = \"127.0.0.1:7411\"\n", "listen"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := config.Load(path)
			if err == nil || !strings.Contains(err.Error(), "unknown key "+tc.key) {
				t.Fatalf("Load = %v; want an error naming unknown key %q", err, tc.key)
			}
		})
	}
}

func TestTheHomeSectionAndTheStaleConfigDigestAreGone(t *testing.T) {
	t.Parallel()

	ct := reflect.TypeOf(config.Config{})
	for _, name := range []string{"Home"} {
		if _, ok := ct.FieldByName(name); ok {
			t.Errorf("Config still has a %s field", name)
		}
	}
	for _, name := range []string{"Digest", "ConfigChanged"} {
		if _, ok := ct.MethodByName(name); ok {
			t.Errorf("Config still has a %s method", name)
		}
	}
	if _, ok := reflect.TypeOf(config.Paths{}).MethodByName("TokenFile"); ok {
		t.Error("Paths still has a TokenFile method")
	}

	// A saved default never writes a [home] table or a stale-config key.
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.Default().Save(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"[home]", "listen", "digest"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("saved default config contains %q:\n%s", banned, b)
		}
	}
}
