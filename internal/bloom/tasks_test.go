package bloom

import (
	"reflect"
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
