package bloom

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type pathRunner map[string]bool

func (r pathRunner) LookPath(file string) (string, error) {
	if r[file] {
		return "/bin/" + file, nil
	}
	return "", errors.New("not found")
}

func (r pathRunner) Run(context.Context, string, ...string) CommandOutput {
	return CommandOutput{}
}

func TestFilterRunnableTasksSkipsDisabledAndMissingCommands(t *testing.T) {
	tasks := []Task{
		{Name: "brew", Enabled: true, RequiredCommand: "brew"},
		{Name: "yazi", Enabled: true, RequiredCommand: "ya"},
		{Name: "npm", Enabled: false, RequiredCommand: "npm"},
	}

	gotTasks := filterRunnableTasks(tasks, pathRunner{"brew": true})
	got := []string{}
	for _, task := range gotTasks {
		got = append(got, task.Name)
	}
	want := []string{"brew"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

type ampUpdateRunner struct {
	lookups      int
	versionCalls int
}

func (r *ampUpdateRunner) LookPath(file string) (string, error) {
	if file == "amp" {
		r.lookups++
		return "/bin/amp", nil
	}
	return "", errors.New("not found")
}

func (r *ampUpdateRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	call := strings.Join(append([]string{name}, args...), " ")
	switch call {
	case "amp --version":
		r.versionCalls++
		if r.versionCalls == 1 {
			return CommandOutput{Stdout: "1.0.0\n"}
		}
		return CommandOutput{Stdout: "1.1.0\n"}
	case "amp update":
		return CommandOutput{}
	default:
		return CommandOutput{Err: errors.New("unexpected command: " + call)}
	}
}

func TestRunUpdatePrintsDoneAndSummaryWithoutBlankLine(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TaskOrder = []string{"amp"}
	cfg.ProgressWidth = 8
	cfg.Color = false
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{
		Out:    &stdout,
		Err:    &stderr,
		Runner: &ampUpdateRunner{},
	}

	code := app.Run([]string{"update", "--config", path})
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if app.Runner.(*ampUpdateRunner).lookups != 1 {
		t.Fatalf("amp lookups = %d, want 1", app.Runner.(*ampUpdateRunner).lookups)
	}

	got := strings.TrimSpace(stdout.String())
	want := "[━━━━━━━━] 100% ✓ amp changed\n[━━━━━━━━] 100% ✓ done!\n✓ amp\n   └── amp"
	if got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunCheckOutputsTSV(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"brew": true, "npm": true},
		outputs: map[string]CommandOutput{
			"brew update":                           {},
			"brew outdated --quiet --formula":       {Stdout: "bloom\n"},
			"brew list --formula --full-name":       {Stdout: "stellarjmr/tool/bloom\n"},
			"brew outdated --quiet --cask --greedy": {Stdout: "iterm2\n"},
			"npm outdated -g --json --depth=0":      {Stdout: `{"npm":{"current":"1.0.0","wanted":"1.1.0","latest":"1.1.0"}}`},
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{Out: &stdout, Err: &stderr, Runner: r}

	code := app.Run([]string{"check", "--format", "tsv"})
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}

	got := stdout.String()
	want := "brew\tstellarjmr/tool/bloom\ncask\titerm2\nnpm\tnpm\n"
	if got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunUpdatePackageFilterRunsSelectedTaskPackage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TaskOrder = []string{"brew", "npm"}
	cfg.ProgressWidth = 8
	cfg.Color = false
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	r := &recordingRunner{
		paths: map[string]bool{"brew": true, "npm": true},
		outputs: map[string]CommandOutput{
			"npm list -g --depth=0 --json": {Stdout: `{"dependencies":{"npm":{"version":"1.0.0"},"corepack":{"version":"1.0.0"}}}`},
			"npm update -g npm":            {},
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{Out: &stdout, Err: &stderr, Runner: r}

	code := app.Run([]string{"update", "--config", path, "--package", "npm:npm"})
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}

	wantCalls := []string{
		"npm list -g --depth=0 --json",
		"npm update -g npm",
		"npm list -g --depth=0 --json",
	}
	if !reflect.DeepEqual(r.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", r.calls, wantCalls)
	}
}
