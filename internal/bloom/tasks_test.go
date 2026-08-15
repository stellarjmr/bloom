package bloom

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseYaziPackages(t *testing.T) {
	got := parseYaziPackages(`
Plugins:
  foo/bar (abc123)
  baz (def456)
Flavors:
  theme (zzz)
`)
	want := map[string]string{
		"foo/bar": "abc123",
		"baz":     "def456",
		"theme":   "zzz",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseNPMGlobals(t *testing.T) {
	got := parseNPMGlobals(`{
  "dependencies": {
    "npm": { "version": "11.13.0" },
    "@scope/tool": { "version": "1.2.3" }
  }
}`)
	want := map[string]string{
		"npm":         "11.13.0",
		"@scope/tool": "1.2.3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseNPMOutdated(t *testing.T) {
	got, err := parseNPMOutdated(`{
  "@scope/tool": { "current": "1.0.0", "wanted": "1.1.0", "latest": "1.1.0" },
  "npm": { "current": "10.0.0", "wanted": "11.0.0", "latest": "11.0.0" }
}`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"@scope/tool", "npm"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestDiffVersionMap(t *testing.T) {
	before := map[string]string{"a": "1", "b": "1"}
	after := map[string]string{"a": "2", "b": "1", "c": "1"}
	got := diffVersionMap(before, after)
	want := []string{"a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestFilterNamesIncludeExclude(t *testing.T) {
	values := []string{"a", "b", "c"}
	cfg := TaskConfig{Include: []string{"c", "a", "missing"}, Exclude: []string{"a"}}
	got := filterNames(values, cfg)
	want := []string{"c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestProgressBarNoColor(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Color = false
	cfg.ProgressWidth = 8
	bar := NewProgress(nil, cfg).Bar(1, 4)
	want := "[━╸──────]  25%"
	if bar != want {
		t.Fatalf("bar = %q, want %q", bar, want)
	}
}

func TestProgressRenderStartDoesNotDuplicateNonTerminalOutput(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Color = false
	cfg.ProgressWidth = 8
	var out bytes.Buffer
	progress := NewProgress(&out, cfg)

	progress.RenderStart(0, 1, TaskResult{Name: "npm", Status: StatusRunning})
	progress.Render(1, 1, TaskResult{Name: "npm", Status: StatusDryRun, Message: "1 package"})

	got := strings.TrimSpace(out.String())
	want := "[━━━━━━━━] 100% … npm 1 package"
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestProgressRunningMarkerUsesSpinnerFrame(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Color = false
	cfg.ProgressWidth = 8
	var out bytes.Buffer
	NewProgress(&out, cfg).Render(0, 1, TaskResult{Name: "npm", Status: StatusRunning})

	got := strings.TrimSpace(out.String())
	want := "[────────]   0% ⠋ npm"
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestSummaryLinesDoNotUseIconPrefix(t *testing.T) {
	got := summaryLines([]string{"npm", "@scope/tool"})
	want := []string{"npm", "@scope/tool"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestProgressMarkersUsePortableSymbols(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Color = false
	cfg.ProgressWidth = 8
	cases := []struct {
		name string
		res  TaskResult
		want string
	}{
		{name: "ok", res: TaskResult{Name: "brew", Status: StatusOK}, want: "[━━━━━━━━] 100% ✓ brew"},
		{name: "skipped", res: TaskResult{Name: "brew", Status: StatusSkipped}, want: "[━━━━━━━━] 100% · brew"},
		{name: "dry-run", res: TaskResult{Name: "brew", Status: StatusDryRun}, want: "[━━━━━━━━] 100% … brew"},
		{name: "failed", res: TaskResult{Name: "brew", Err: errNotFound}, want: "[━━━━━━━━] 100% ✗ brew"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			NewProgress(&out, cfg).Render(1, 1, tt.res)
			got := strings.TrimSpace(out.String())
			if got != tt.want {
				t.Fatalf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

type recordingRunner struct {
	paths   map[string]bool
	outputs map[string]CommandOutput
	calls   []string
}

func (r *recordingRunner) LookPath(file string) (string, error) {
	if r.paths[file] {
		return "/bin/" + file, nil
	}
	return "", errNotFound
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	return r.outputs[call]
}

type sequenceRunner struct {
	paths   map[string]bool
	outputs map[string][]CommandOutput
	calls   []string
}

func (r *sequenceRunner) LookPath(file string) (string, error) {
	if r.paths[file] {
		return "/bin/" + file, nil
	}
	return "", errNotFound
}

func (r *sequenceRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	seq := r.outputs[call]
	if len(seq) == 0 {
		return CommandOutput{}
	}
	out := seq[0]
	r.outputs[call] = seq[1:]
	return out
}

var errNotFound = &runnerError{"not found"}

type runnerError struct {
	msg string
}

func (e *runnerError) Error() string {
	return e.msg
}

func TestNvimLuaErrorRequiresSentinel(t *testing.T) {
	if err := nvimLuaError(CommandOutput{Stdout: "MASON_DONE\n"}, "mason update", "MASON_DONE"); err != nil {
		t.Fatalf("sentinel present should pass, got %v", err)
	}
	if err := nvimLuaError(CommandOutput{Stdout: "LAZY_MISSING\n"}, "lazy.nvim sync", "LAZY_DONE", "LAZY_MISSING"); err != nil {
		t.Fatalf("alternate marker should pass, got %v", err)
	}
	err := nvimLuaError(CommandOutput{Stderr: "E5108: Error executing lua: boom\n"}, "mason update", "MASON_DONE")
	if err == nil {
		t.Fatal("missing sentinel must fail even with exit code 0")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should carry the nvim output, got %v", err)
	}
	if err := nvimLuaError(CommandOutput{Err: errNotFound}, "mason update", "MASON_DONE"); err == nil {
		t.Fatal("non-zero exit must fail")
	}
}

type nvimStubRunner struct {
	output CommandOutput
}

func (r *nvimStubRunner) LookPath(file string) (string, error) {
	if file == "nvim" {
		return "/bin/nvim", nil
	}
	return "", errNotFound
}

func (r *nvimStubRunner) Run(_ context.Context, name string, _ ...string) CommandOutput {
	if name == "nvim" {
		return r.output
	}
	return CommandOutput{Err: errNotFound}
}

func TestRemovePackagesMasonFailsWithoutSentinel(t *testing.T) {
	refs := []PackageRef{{Task: "mason", Name: "lua-language-server"}}

	failedRun := &nvimStubRunner{output: CommandOutput{Stderr: "E5108: Error executing lua: lua-language-server: not found\n"}}
	results := RemovePackages(context.Background(), failedRun, refs, false)
	if results[0].Err == nil {
		t.Fatal("mason removal without sentinel must report an error")
	}

	okRun := &nvimStubRunner{output: CommandOutput{Stdout: "MASON_REMOVED:lua-language-server\nMASON_DONE\n"}}
	results = RemovePackages(context.Background(), okRun, refs, false)
	if results[0].Err != nil {
		t.Fatalf("successful mason removal reported error: %v", results[0].Err)
	}
}

func TestRunBrewFormulaeUpdatesMetadataBeforeOutdated(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"brew": true},
		outputs: map[string]CommandOutput{
			"brew update":                        {},
			"brew outdated --quiet --formula":    {Stdout: "bloom\n"},
			"brew list --formula --full-name":    {Stdout: "stellarjmr/tool/bloom\n"},
			"brew upgrade stellarjmr/tool/bloom": {},
		},
	}

	res := runBrewFormulae(context.Background(), r, UpdateOptions{Config: DefaultConfig()})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := []string{
		"brew update",
		"brew outdated --quiet --formula",
		"brew list --formula --full-name",
		"brew upgrade stellarjmr/tool/bloom",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
}

func TestRunBrewFormulaeAndCasksShareMetadataUpdate(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"brew": true},
		outputs: map[string]CommandOutput{
			"brew update":                           {},
			"brew outdated --quiet --formula":       {Stdout: "bloom\n"},
			"brew list --formula --full-name":       {Stdout: "stellarjmr/tool/bloom\n"},
			"brew upgrade stellarjmr/tool/bloom":    {},
			"brew outdated --quiet --cask --greedy": {Stdout: "iterm2\n"},
			"brew upgrade --cask --greedy iterm2":   {},
		},
	}
	metadataUpdated := false
	opts := UpdateOptions{Config: DefaultConfig(), HomebrewMetadataUpdated: &metadataUpdated}

	brew := runBrewFormulae(context.Background(), r, opts)
	if brew.Err != nil {
		t.Fatal(brew.Err)
	}
	cask := runBrewCasks(context.Background(), r, opts)
	if cask.Err != nil {
		t.Fatal(cask.Err)
	}

	want := []string{
		"brew update",
		"brew outdated --quiet --formula",
		"brew list --formula --full-name",
		"brew upgrade stellarjmr/tool/bloom",
		"brew outdated --quiet --cask --greedy",
		"brew upgrade --cask --greedy iterm2",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
	if !metadataUpdated {
		t.Fatal("metadata update state was not recorded")
	}
}

func TestRunHomebrewUpdateRetriesBusyLock(t *testing.T) {
	oldDelay := homebrewUpdateLockRetryDelay
	oldRetries := homebrewUpdateLockMaxRetries
	homebrewUpdateLockRetryDelay = 0
	homebrewUpdateLockMaxRetries = 1
	t.Cleanup(func() {
		homebrewUpdateLockRetryDelay = oldDelay
		homebrewUpdateLockMaxRetries = oldRetries
	})
	r := &sequenceRunner{
		paths: map[string]bool{"brew": true},
		outputs: map[string][]CommandOutput{
			"brew update": {
				{Err: errors.New("exit status 1"), Stderr: "lockf: 200: already locked\nError: Another `brew update` process is already running."},
				{},
			},
		},
	}

	out := runHomebrewUpdate(context.Background(), r)
	if out.Err != nil {
		t.Fatalf("runHomebrewUpdate returned error after retry: %v", out.Err)
	}
	want := []string{"brew update", "brew update"}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
}

func TestRunBrewFormulaeDryRunDoesNotUpdateMetadata(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"brew": true},
		outputs: map[string]CommandOutput{
			"brew outdated --quiet --formula": {Stdout: "bloom\n"},
			"brew list --formula --full-name": {Stdout: "stellarjmr/tool/bloom\n"},
		},
	}

	res := runBrewFormulae(context.Background(), r, UpdateOptions{DryRun: true, Config: DefaultConfig()})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := []string{
		"brew outdated --quiet --formula",
		"brew list --formula --full-name",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
}

func TestCheckUpdatesUsesOfficialCheckCommands(t *testing.T) {
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

	got, err := CheckUpdates(context.Background(), r, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	wantItems := []UpdateItem{
		{Task: "brew", Name: "stellarjmr/tool/bloom"},
		{Task: "cask", Name: "iterm2"},
		{Task: "npm", Name: "npm"},
	}
	if !reflect.DeepEqual(got, wantItems) {
		t.Fatalf("items = %#v, want %#v", got, wantItems)
	}

	wantCalls := []string{
		"brew update",
		"brew outdated --quiet --formula",
		"brew list --formula --full-name",
		"brew outdated --quiet --cask --greedy",
		"npm outdated -g --json --depth=0",
	}
	if !reflect.DeepEqual(r.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", r.calls, wantCalls)
	}
}

func TestRunBrewCasksUsesOfficialHomebrewCommands(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"brew": true},
		outputs: map[string]CommandOutput{
			"brew update":                           {},
			"brew outdated --quiet --cask --greedy": {Stdout: "iterm2\n"},
			"brew upgrade --cask --greedy iterm2":   {},
		},
	}

	res := runBrewCasks(context.Background(), r, UpdateOptions{Config: DefaultConfig()})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := []string{
		"brew update",
		"brew outdated --quiet --cask --greedy",
		"brew upgrade --cask --greedy iterm2",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
}

func TestRunAmpUsesOfficialUpdateCommand(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"amp": true},
		outputs: map[string]CommandOutput{
			"amp --version": {Stdout: "1.0.0\n"},
			"amp update":    {},
		},
	}

	res := runAmp(context.Background(), r, UpdateOptions{Config: DefaultConfig()})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := []string{
		"amp --version",
		"amp update",
		"amp --version",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
}

func TestRunYaziUpdatesAllPackagesByDefault(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"ya": true},
		outputs: map[string]CommandOutput{
			"ya pkg list":    {Stdout: "Plugins:\n  foo/bar (abc123)\nFlavors:\n  foo/theme (def456)\n"},
			"ya pkg upgrade": {},
		},
	}

	res := runYazi(context.Background(), r, UpdateOptions{Config: DefaultConfig()})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := []string{
		"ya pkg list",
		"ya pkg upgrade",
		"ya pkg list",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
}

func TestRunYaziPassesPackageIDsWhenFiltered(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"ya": true},
		outputs: map[string]CommandOutput{
			"ya pkg list":              {Stdout: "Plugins:\n  foo/bar (abc123)\nFlavors:\n  foo/theme (def456)\n"},
			"ya pkg upgrade foo/theme": {},
		},
	}
	cfg := DefaultConfig()
	taskCfg := cfg.Tasks["yazi"]
	taskCfg.Include = []string{"foo/theme"}
	cfg.Tasks["yazi"] = taskCfg

	res := runYazi(context.Background(), r, UpdateOptions{Config: cfg})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := []string{
		"ya pkg list",
		"ya pkg upgrade foo/theme",
		"ya pkg list",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
}

func TestRemovePackagesUsesOfficialPackageManagerCommands(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"brew": true, "ya": true, "npm": true},
		outputs: map[string]CommandOutput{
			"brew uninstall ripgrep":       {},
			"ya pkg delete foo/bar":        {},
			"npm uninstall -g @scope/tool": {},
		},
	}
	refs := []PackageRef{
		{Task: "brew", Name: "ripgrep"},
		{Task: "yazi", Name: "foo/bar"},
		{Task: "npm", Name: "@scope/tool"},
	}

	results := RemovePackages(context.Background(), r, refs, false)
	for _, res := range results {
		if res.Err != nil {
			t.Fatalf("%s:%s failed: %v", res.Package.Task, res.Package.Name, res.Err)
		}
	}
	want := []string{
		"brew uninstall ripgrep",
		"ya pkg delete foo/bar",
		"npm uninstall -g @scope/tool",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
}

func TestRunNPMUsesOfficialGlobalUpdateCommand(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"npm": true},
		outputs: map[string]CommandOutput{
			"npm list -g --depth=0 --json": {Stdout: `{"dependencies":{"npm":{"version":"1.0.0"}}}`},
			"npm update -g":                {},
		},
	}

	res := runNPM(context.Background(), r, UpdateOptions{Config: DefaultConfig()})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := []string{
		"npm list -g --depth=0 --json",
		"npm update -g",
		"npm list -g --depth=0 --json",
	}
	if !reflect.DeepEqual(r.calls, want) {
		t.Fatalf("calls = %#v, want %#v", r.calls, want)
	}
}
