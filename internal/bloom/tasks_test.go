package bloom

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestParseYaziPlugins(t *testing.T) {
	got := parseYaziPlugins(`
Plugins:
  foo/bar (abc123)
  baz (def456)
Flavors:
  theme (zzz)
`)
	want := map[string]string{
		"foo/bar": "abc123",
		"baz":     "def456",
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

func TestDiffVersionMap(t *testing.T) {
	before := map[string]string{"a": "1", "b": "1"}
	after := map[string]string{"a": "2", "b": "1", "c": "1"}
	got := diffVersionMap("*", before, after)
	want := []string{"* a", "* c"}
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

var errNotFound = &runnerError{"not found"}

type runnerError struct {
	msg string
}

func (e *runnerError) Error() string {
	return e.msg
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
