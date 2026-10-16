package bloom

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestLoadConfigFailsClosedWhenConfigLinkIsBroken(t *testing.T) {
	dir := t.TempDir()
	linked := filepath.Join(dir, "config.toml")
	if err := os.Symlink(filepath.Join(dir, "unmounted", "config.toml"), linked); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(linked); err == nil || !strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("LoadConfig(broken file link) error = %v, want broken symlink", err)
	}

	linkedDir := filepath.Join(dir, "dotfiles")
	if err := os.Symlink(filepath.Join(dir, "gone"), linkedDir); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(filepath.Join(linkedDir, "bloom", "config.toml")); err == nil || !strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("LoadConfig(broken directory link) error = %v, want broken symlink", err)
	}

	notDir := filepath.Join(dir, "plain-file")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(filepath.Join(notDir, "config.toml")); err == nil {
		t.Fatal("LoadConfig below a regular file used defaults")
	}
}

func TestLoadConfigFollowsSymlinksBeforeParentComponents(t *testing.T) {
	dir := t.TempDir()
	lexical := filepath.Join(dir, "a")
	physical := filepath.Join(dir, "b", "subdir")
	for _, path := range []string{lexical, physical} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(physical, filepath.Join(lexical, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "gone"), filepath.Join(lexical, "broken")); err != nil {
		t.Fatal(err)
	}
	// Built by concatenation: filepath.Join would clean away the "..".
	viaLink := filepath.Join(lexical, "link") + "/../config.toml"
	viaBrokenLink := filepath.Join(lexical, "broken") + "/../config.toml"

	// a/link/.. is b, whose config.toml is missing: defaults apply.
	if _, err := LoadConfig(viaLink); err != nil {
		t.Fatalf("LoadConfig(%q) error = %v, want defaults for the physically missing file", viaLink, err)
	}
	// The kernel cannot resolve a broken link, so no lexical shortcut may skip it.
	if _, err := LoadConfig(viaBrokenLink); err == nil || !strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("LoadConfig(%q) error = %v, want broken symlink", viaBrokenLink, err)
	}

	// A decoy at the lexically cleaned path must not change either verdict.
	if err := os.WriteFile(filepath.Join(lexical, "config.toml"), []byte("[settings]\ncolor = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(viaLink)
	if err != nil || !cfg.Color {
		t.Fatalf("LoadConfig(%q) = color %v, err %v; want defaults, not the lexical decoy", viaLink, cfg.Color, err)
	}
	if _, err := LoadConfig(viaBrokenLink); err == nil || !strings.Contains(err.Error(), "broken symlink") {
		t.Fatalf("LoadConfig(%q) with decoy error = %v, want broken symlink", viaBrokenLink, err)
	}
}

func TestLoadConfigUsesDefaultsOnlyWhenConfigIsMissing(t *testing.T) {
	dir := t.TempDir()
	liveTarget := filepath.Join(dir, "dotfiles")
	if err := os.MkdirAll(liveTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	liveLink := filepath.Join(dir, "config-link")
	if err := os.Symlink(liveTarget, liveLink); err != nil {
		t.Fatal(err)
	}
	want := effectiveCleanWhitelist(DefaultCleanWhitelist())
	for _, path := range []string{
		filepath.Join(dir, "config.toml"),
		filepath.Join(dir, "absent", "bloom", "config.toml"),
		filepath.Join(liveLink, "bloom", "config.toml"),
	} {
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig(%q) error = %v, want defaults", path, err)
		}
		if !reflect.DeepEqual(cfg.Clean.Whitelist, want) {
			t.Fatalf("LoadConfig(%q) whitelist = %#v, want defaults", path, cfg.Clean.Whitelist)
		}
	}
}

func TestLoadConfigMergesHardCleanSafetyWhitelist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[clean]
whitelist = ["~/.cache/custom-keep/*"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(cfg.Clean.Whitelist, "~/.cache/custom-keep/*") {
		t.Fatalf("custom clean whitelist entry was lost: %#v", cfg.Clean.Whitelist)
	}
	if !containsString(cfg.Clean.Whitelist, cleanFinderMetadataSentinel) {
		t.Fatalf("hard safety whitelist was not merged: %#v", cfg.Clean.Whitelist)
	}
}

func TestConfigTextPersistsHardCleanSafetyWhitelist(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = []string{"~/.cache/custom-keep/*"}

	text := ConfigText(cfg)
	if !strings.Contains(text, `"FINDER_METADATA"`) {
		t.Fatalf("ConfigText omitted hard safety whitelist:\n%s", text)
	}
}

func TestNoColorEnvironmentAppliesAtRenderTimeOnly(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	path := filepath.Join(t.TempDir(), "config.toml")
	content := `
[settings]
color = true
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Color {
		t.Fatal("LoadConfig must not bake the NO_COLOR override into the config")
	}

	// Load-modify-save flows must not persist the env override to disk.
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "color = true") {
		t.Fatalf("NO_COLOR was persisted into the config file:\n%s", saved)
	}

	// Rendering must honor NO_COLOR.
	var buf strings.Builder
	progress := NewProgress(&buf, cfg)
	progress.Render(1, 1, TaskResult{Name: "brew", Status: StatusOK})
	if strings.Contains(buf.String(), "\033[") {
		t.Fatalf("progress output contains ANSI styling despite NO_COLOR: %q", buf.String())
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
