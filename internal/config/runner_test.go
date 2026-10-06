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

func TestLoadReadsAgentModelsCoordinatorAndStartRuns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "[agent]\nmodels = [\"gpt-5.6\", \"fast\"]\ncoordinator = [\"agent\", \"{session}\"]\n\n[coordinator]\nstart_runs = \"auto\"\n"
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
	if want := []string{"agent", "{session}"}; !slices.Equal(got.Agent.Coordinator, want) {
		t.Errorf("Load().Agent.Coordinator = %#v; want %#v", got.Agent.Coordinator, want)
	}
	if got.Coordinator.StartRuns != config.StartRunsAuto {
		t.Errorf("Load().Coordinator.StartRuns = %q; want auto", got.Coordinator.StartRuns)
	}
}

func TestLoadRefusesEachRemovedRunnerKeyByName(t *testing.T) {
	t.Parallel()

	// Each removed key is built from its words, so the tree's sweep for dropped names finds none here.
	for _, test := range []struct {
		table string
		words []string
		value string
	}{
		{"runner", []string{"poll", "seconds"}, "30"},
		{"runner", []string{"agents", "may", "arm"}, "true"},
		{"agent", []string{"router"}, `["claude"]`},
		{"router", []string{"system"}, `"/prompts/router.md"`},
	} {
		field := strings.Join(test.words, "_")
		key := test.table + "." + field
		text := "[" + test.table + "]\n" + field + " = " + test.value + "\n"
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(path); err == nil || !strings.Contains(err.Error(), key) {
				t.Errorf("Load(%q) error = %v; want an error naming %s", text, err, key)
			}
		})
	}
}

func TestSaveAndLoadPreserveAgentModelsAndTheCoordinator(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	want := config.Default()
	want.Agent.Models = []string{"gpt-5.6", "gpt-5.6-fast"}
	want.Agent.Coordinator = []string{"agent", "--session-id", "{session}"}
	want.Coordinator.StartRuns = config.StartRunsAuto
	if err := want.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(saved config) error = %v", err)
	}
	if !slices.Equal(got.Agent.Models, want.Agent.Models) || !slices.Equal(got.Agent.Coordinator, want.Agent.Coordinator) {
		t.Errorf("Load(saved config).Agent = %#v; want %#v", got.Agent, want.Agent)
	}
	if got.Coordinator != want.Coordinator {
		t.Errorf("Load(saved config).Coordinator = %#v; want %#v", got.Coordinator, want.Coordinator)
	}
}

func TestSaveAndLoadPreserveARootsFirstMessageAndItRendersTheTaskFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	want := config.Default()
	want.Roots = []config.Root{{Path: "/code/app", FirstMessage: "/build {task_file} {model}"}, {Path: "/code/other"}}
	if err := want.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(saved config) error = %v", err)
	}
	if !reflect.DeepEqual(got.Roots, want.Roots) {
		t.Fatalf("Load(saved config).Roots = %#v; want %#v", got.Roots, want.Roots)
	}
	if b, _ := os.ReadFile(path); strings.Count(string(b), "first_message") != 1 {
		t.Errorf("saved config = %q; want first_message written only for the root that sets it", b)
	}
	if msg := got.Roots[0].Message("/state/runs/run-{task_file}.md"); msg != "/build /state/runs/run-{task_file}.md {model}" {
		t.Errorf("Message() = %q; want {task_file} replaced once and other placeholders left as written", msg)
	}
}

func TestValidateRefusesEachBadRunnerValueByKey(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		key    string
		adjust func(*config.Config)
	}{
		{"runner.cap", func(c *config.Config) { c.Runner.Cap = 0 }},
		{"runner.max_runs_per_day", func(c *config.Config) { c.Runner.MaxRunsPerDay = 0 }},
		{"runner.max_run_minutes", func(c *config.Config) { c.Runner.MaxRunMinutes = 0 }},
		{"coordinator.start_runs", func(c *config.Config) { c.Coordinator.StartRuns = "always" }},
		{"first_message", func(c *config.Config) { c.Roots = []config.Root{{Path: "/code/app", FirstMessage: "/build {task}"}} }},
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

func TestRunnerPauseStaysInTheStateDirectory(t *testing.T) {
	t.Parallel()

	p := config.Paths{StateDir: "/state/herdr-desk"}
	if got, want := p.RunnerPause(), "/state/herdr-desk/runner-paused"; got != want {
		t.Errorf("RunnerPause() = %q; want %q", got, want)
	}
}
