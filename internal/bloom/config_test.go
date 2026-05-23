package bloom

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadConfigOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[settings]
progress_width = 32
color = false

[tasks]
order = ["npm", "nvim"]

[tasks.npm]
enabled = false
include = ["npm", "@scope/tool"]
exclude = ["legacy"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProgressWidth != 32 {
		t.Fatalf("ProgressWidth = %d, want 32", cfg.ProgressWidth)
	}
	if cfg.Color {
		t.Fatalf("Color = true, want false")
	}
	if !reflect.DeepEqual(cfg.TaskOrder, []string{"npm", "nvim"}) {
		t.Fatalf("TaskOrder = %#v", cfg.TaskOrder)
	}
	if cfg.Tasks["npm"].Enabled {
		t.Fatalf("npm enabled = true, want false")
	}
	if !reflect.DeepEqual(cfg.Tasks["npm"].Include, []string{"npm", "@scope/tool"}) {
		t.Fatalf("npm include = %#v", cfg.Tasks["npm"].Include)
	}
	if !reflect.DeepEqual(cfg.Tasks["npm"].Exclude, []string{"legacy"}) {
		t.Fatalf("npm exclude = %#v", cfg.Tasks["npm"].Exclude)
	}
}

func TestLoadConfigHonorsNoColorEnvironment(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Color {
		t.Fatal("NO_COLOR did not disable default color output")
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[settings]
color = true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Color {
		t.Fatal("NO_COLOR did not override config color=true")
	}
}

func TestBuildTasksUsesConfiguredOrderOnly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TaskOrder = []string{"npm", "nvim"}

	tasks, err := BuildTasks(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{tasks[0].Name, tasks[1].Name}
	want := []string{"npm", "nvim"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tasks = %#v, want %#v", got, want)
	}
}

func TestWriteDefaultConfigDoesNotOverwriteWithoutForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteDefaultConfig(path, false); err == nil {
		t.Fatal("expected overwrite error")
	}
	if err := WriteDefaultConfig(path, true); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) == "existing" {
		t.Fatal("config was not overwritten with force")
	}
}

func TestSaveConfigRoundTripsEnabledTasksAndInclude(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := DefaultConfig()
	if err := SetEnabledTasks(&cfg, []string{"brew", "npm"}); err != nil {
		t.Fatal(err)
	}
	if err := SetTaskInclude(&cfg, "npm", []string{"npm", "corepack", "npm"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.TaskOrder, DefaultTaskNames()) {
		t.Fatalf("TaskOrder = %#v, want %#v", got.TaskOrder, DefaultTaskNames())
	}
	if !got.Tasks["brew"].Enabled || !got.Tasks["npm"].Enabled {
		t.Fatalf("brew/npm should be enabled: %#v", got.Tasks)
	}
	if got.Tasks["yazi"].Enabled {
		t.Fatalf("yazi should be disabled")
	}
	if !reflect.DeepEqual(got.Tasks["npm"].Include, []string{"npm", "corepack"}) {
		t.Fatalf("npm include = %#v", got.Tasks["npm"].Include)
	}
}
