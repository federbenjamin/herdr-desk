package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/federbenjamin/herdr-desk/internal/config"
)

func TestLoadReadsAgentModelsAndRouterFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[agent]\nmodels = [\"gpt-5.6\", \"fast\"]\n\n[router]\nsystem = \"/prompts/router.md\"\nschema = \"/schemas/route.json\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := []string{"gpt-5.6", "fast"}; !slices.Equal(got.Agent.Models, want) {
		t.Errorf("Load().Agent.Models = %#v; want %#v", got.Agent.Models, want)
	}
	if got.Router.System != "/prompts/router.md" || got.Router.Schema != "/schemas/route.json" {
		t.Errorf("Load().Router = %#v; want system and schema paths", got.Router)
	}
}

func TestSaveAndLoadPreserveAgentModelsAndRouterFiles(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	want := config.Default()
	want.Agent.Models = []string{"gpt-5.6", "gpt-5.6-fast"}
	want.Router = config.RouterFiles{System: "/prompts/custom-router.md", Schema: "/schemas/custom-route.json"}
	if err := want.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(saved config) error = %v", err)
	}
	if !slices.Equal(got.Agent.Models, want.Agent.Models) {
		t.Errorf("Load(saved config).Agent.Models = %#v; want %#v", got.Agent.Models, want.Agent.Models)
	}
	if got.Router != want.Router {
		t.Errorf("Load(saved config).Router = %#v; want %#v", got.Router, want.Router)
	}
}

func TestValidateRefusesEachRunnerLimitBelowOneByKey(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		key    string
		adjust func(*config.Config)
	}{
		{"runner.cap", func(c *config.Config) { c.Runner.Cap = 0 }},
		{"runner.max_runs_per_day", func(c *config.Config) { c.Runner.MaxRunsPerDay = 0 }},
		{"runner.max_run_minutes", func(c *config.Config) { c.Runner.MaxRunMinutes = 0 }},
		{"runner.poll_seconds", func(c *config.Config) { c.Runner.PollSeconds = 0 }},
	} {
		t.Run(test.key, func(t *testing.T) {
			c := config.Default()
			test.adjust(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil; want error naming %s", test.key)
			}
			if !strings.Contains(err.Error(), test.key) {
				t.Errorf("Validate() error = %q; want it to name %q", err, test.key)
			}
		})
	}
}

func TestValidateAcceptsDefaultRunnerLimits(t *testing.T) {
	t.Parallel()

	if err := config.Default().Validate(); err != nil {
		t.Errorf("Default().Validate() error = %v; want nil", err)
	}
}

func TestExpandSubstitutesEachArgumentOnlyOnce(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		template []string
		vars     map[string]string
		want     []string
	}{
		{
			name:     "several placeholders in one argument",
			template: []string{"run", "{task}-{model}-{session}"},
			vars:     map[string]string{"task": "T12", "model": "gpt-5.6", "session": "abc"},
			want:     []string{"run", "T12-gpt-5.6-abc"},
		},
		{
			name:     "a substituted value is not scanned again",
			template: []string{"{message}"},
			vars:     map[string]string{"message": "keep {model}", "model": "gpt-5.6"},
			want:     []string{"keep {model}"},
		},
		{
			name:     "unknown placeholder remains written",
			template: []string{"{task}", "{missing}"},
			vars:     map[string]string{"task": "T12"},
			want:     []string{"T12", "{missing}"},
		},
		{
			name:     "empty template gives an empty result",
			template: []string{},
			vars:     map[string]string{"task": "T12"},
			want:     []string{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := slices.Clone(test.template)
			got := config.Expand(test.template, test.vars)
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("Expand(%#v, %#v) = %#v; want %#v", test.template, test.vars, got, test.want)
			}
			if !reflect.DeepEqual(test.template, original) {
				t.Errorf("Expand() changed template to %#v; want %#v", test.template, original)
			}
		})
	}
}

func TestPathsEnvRoundTripsThroughResolvePathsInXDGOrder(t *testing.T) {
	t.Parallel()

	want := config.Paths{
		ConfigDir: "/config/herdr-desk",
		StateDir:  "/state/herdr-desk",
		DataDir:   "/data/herdr-desk",
		CacheDir:  "/cache/herdr-desk",
	}
	gotEnv := want.Env()
	wantEnv := []string{
		"XDG_CONFIG_HOME=/config",
		"XDG_STATE_HOME=/state",
		"XDG_DATA_HOME=/data",
		"XDG_CACHE_HOME=/cache",
	}
	if !slices.Equal(gotEnv, wantEnv) {
		t.Fatalf("Paths.Env() = %#v; want %#v", gotEnv, wantEnv)
	}

	env := make(map[string]string, len(gotEnv))
	for _, entry := range gotEnv {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("Paths.Env() entry %q has no equals sign", entry)
		}
		env[key] = value
	}
	if got := config.ResolvePaths(func(key string) string { return env[key] }); got != want {
		t.Errorf("ResolvePaths(Paths.Env()) = %#v; want %#v", got, want)
	}
}

func TestRouterAndRunnerPathsStayInTheStateDirectory(t *testing.T) {
	t.Parallel()

	p := config.Paths{StateDir: "/state/herdr-desk"}
	if got, want := p.RouterSystemFile(), "/state/herdr-desk/router-system.md"; got != want {
		t.Errorf("RouterSystemFile() = %q; want %q", got, want)
	}
	if got, want := p.RunnerPause(), "/state/herdr-desk/runner-paused"; got != want {
		t.Errorf("RunnerPause() = %q; want %q", got, want)
	}
}
