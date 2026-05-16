package bloom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
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

func TestProtectedAppPathSkipsSystemRootsAndSymlinkTargets(t *testing.T) {
	if !isProtectedAppPath("/System/Library/CoreServices/Applications/Feedback Assistant.app") {
		t.Fatal("system-root app was not protected")
	}
	if !isProtectedAppPath("/Applications/Utilities/Feedback Assistant.app") {
		t.Fatal("Feedback Assistant was not protected by name")
	}

	link := filepath.Join(t.TempDir(), "Mole.app")
	if err := os.Symlink("/System/Library/CoreServices/Applications/Feedback Assistant.app", link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if !isProtectedAppPath(link) {
		t.Fatal("symlink to system app was not protected")
	}
}

func TestProtectedAppPathAllowsUserInstalledAppleApps(t *testing.T) {
	for _, path := range []string{
		"/Applications/Xcode.app",
		"/Applications/Final Cut Pro.app",
		"/Applications/GarageBand.app",
	} {
		if isProtectedAppPath(path) {
			t.Fatalf("%s should not be protected", path)
		}
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

func TestRunRemoveListOutputsTSV(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	r := &recordingRunner{
		paths: map[string]bool{"brew": true, "npm": true},
		outputs: map[string]CommandOutput{
			"brew list --formula --full-name": {Stdout: "ripgrep\n"},
			"npm list -g --depth=0 --json":    {Stdout: `{"dependencies":{"npm":{"version":"1.0.0"}}}`},
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{Out: &stdout, Err: &stderr, Runner: r}

	code := app.Run([]string{"remove", "--list"})
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}

	got := stdout.String()
	want := "brew\tripgrep\nnpm\tnpm\n"
	if got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	wantCalls := []string{
		"brew list --formula --full-name",
		"npm list -g --depth=0 --json",
	}
	if !reflect.DeepEqual(r.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", r.calls, wantCalls)
	}
}

func TestRunRemovePackageUsesOfficialCommands(t *testing.T) {
	r := &recordingRunner{
		paths: map[string]bool{"brew": true, "npm": true},
		outputs: map[string]CommandOutput{
			"brew uninstall ripgrep":       {},
			"npm uninstall -g @scope/tool": {},
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{Out: &stdout, Err: &stderr, Runner: r}

	code := app.Run([]string{"remove", "--package", "brew:ripgrep", "--package", "npm:@scope/tool"})
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}

	wantCalls := []string{
		"brew uninstall ripgrep",
		"npm uninstall -g @scope/tool",
	}
	if !reflect.DeepEqual(r.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", r.calls, wantCalls)
	}
	got := strings.TrimSpace(stdout.String())
	want := "✓ brew:ripgrep\n✓ npm:@scope/tool\n\nRemoved 2 packages"
	if got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunRemoveRejectsCaskAndNvimBeforeRemoving(t *testing.T) {
	for _, tt := range []struct {
		name    string
		pkg     string
		message string
	}{
		{name: "cask", pkg: "cask:iterm2", message: "use bm uninstall"},
		{name: "nvim", pkg: "nvim:foo.nvim", message: "managed by your Neovim config"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := &recordingRunner{
				paths: map[string]bool{"brew": true},
				outputs: map[string]CommandOutput{
					"brew uninstall ripgrep": {},
				},
			}
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			app := &App{Out: &stdout, Err: &stderr, Runner: r}

			code := app.Run([]string{"remove", "--package", "brew:ripgrep", "--package", tt.pkg})
			if code != 2 {
				t.Fatalf("code = %d, want 2; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.message) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), tt.message)
			}
			if len(r.calls) != 0 {
				t.Fatalf("calls = %#v, want none", r.calls)
			}
		})
	}
}

func TestPackageFiltersStillAllowCaskAndNvim(t *testing.T) {
	cfg := DefaultConfig()
	only, err := applyPackageFilters(&cfg, []string{"cask:iterm2", "nvim:lazy.nvim"})
	if err != nil {
		t.Fatal(err)
	}
	wantOnly := []string{"cask", "nvim"}
	if !reflect.DeepEqual(only, wantOnly) {
		t.Fatalf("only = %#v, want %#v", only, wantOnly)
	}
	if !reflect.DeepEqual(cfg.Tasks["cask"].Include, []string{"iterm2"}) {
		t.Fatalf("cask include = %#v", cfg.Tasks["cask"].Include)
	}
	if !reflect.DeepEqual(cfg.Tasks["nvim"].Include, []string{"lazy.nvim"}) {
		t.Fatalf("nvim include = %#v", cfg.Tasks["nvim"].Include)
	}
}

func TestGroupContainerMatchingUsesTeamAndDomain(t *testing.T) {
	if !groupContainerMatches("TEAMID.com.example", "TEAMID", "com.example", false) {
		t.Fatal("expected direct TeamID domain prefix match")
	}
	if !groupContainerMatches("TEAMID.group.com.example", "TEAMID", "com.example", false) {
		t.Fatal("expected TeamID group domain prefix match")
	}
	if groupContainerMatches("OTHER.com.example", "TEAMID", "com.example", false) {
		t.Fatal("matched another team's group container")
	}
	if groupContainerMatches("OTHER.com.example", "", "com.example", false) {
		t.Fatal("domain fallback should require explicit fallback mode")
	}
	if !groupContainerMatches("OTHER.com.example", "", "com.example", true) {
		t.Fatal("expected explicit domain fallback match")
	}
}

func TestFindRelatedPathsIncludesDiagnostics(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := filepath.Join(home, "Applications", "Foo App.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}
	diagnostic := filepath.Join(home, "Library", "Logs", "DiagnosticReports", "fooapp_2026-05-12.crash")
	if err := os.MkdirAll(filepath.Dir(diagnostic), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(diagnostic, []byte("crash"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := FindRelatedPaths(AppEntry{Path: appPath, Name: "Foo App", BundleID: "com.example.foo"})
	if !containsString(paths, diagnostic) {
		t.Fatalf("paths missing diagnostic %q: %#v", diagnostic, paths)
	}
}

func TestFindRelatedPathsIncludesVSCodeStablePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := filepath.Join(home, "Applications", "Visual Studio Code.app")
	wantPaths := []string{
		appPath,
		filepath.Join(home, ".vscode"),
		filepath.Join(home, "Library", "Application Support", "Code"),
		filepath.Join(home, "Library", "Caches", "com.microsoft.VSCode"),
		filepath.Join(home, "Library", "Caches", "com.microsoft.VSCode.ShipIt"),
	}
	for _, path := range wantPaths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	insidersSupport := filepath.Join(home, "Library", "Application Support", "Code - Insiders")
	if err := os.MkdirAll(insidersSupport, 0o755); err != nil {
		t.Fatal(err)
	}

	paths := FindRelatedPaths(AppEntry{Path: appPath, Name: "Visual Studio Code", BundleID: "com.microsoft.VSCode"})
	for _, path := range wantPaths {
		if !containsString(paths, path) {
			t.Fatalf("paths missing VS Code path %q: %#v", path, paths)
		}
	}
	if containsString(paths, insidersSupport) {
		t.Fatalf("stable VS Code matched Insiders Application Support: %#v", paths)
	}
}

func TestFindRelatedPathsIncludesVSCodeInsidersPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := filepath.Join(home, "Applications", "Visual Studio Code - Insiders.app")
	wantPaths := []string{
		appPath,
		filepath.Join(home, ".vscode-insiders"),
		filepath.Join(home, "Library", "Application Support", "Code - Insiders"),
		filepath.Join(home, "Library", "Caches", "com.microsoft.VSCodeInsiders"),
		filepath.Join(home, "Library", "Caches", "com.microsoft.VSCodeInsiders.ShipIt"),
	}
	for _, path := range wantPaths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stableSupport := filepath.Join(home, "Library", "Application Support", "Code")
	if err := os.MkdirAll(stableSupport, 0o755); err != nil {
		t.Fatal(err)
	}

	paths := FindRelatedPaths(AppEntry{Path: appPath, Name: "Visual Studio Code - Insiders", BundleID: "com.microsoft.VSCodeInsiders"})
	for _, path := range wantPaths {
		if !containsString(paths, path) {
			t.Fatalf("paths missing VS Code Insiders path %q: %#v", path, paths)
		}
	}
	if containsString(paths, stableSupport) {
		t.Fatalf("VS Code Insiders matched stable Application Support: %#v", paths)
	}
}

func TestLooksLikeBundleIDRequiresReverseDNSComponents(t *testing.T) {
	valid := []string{
		"com.example.app",
		"com.microsoft.VSCode",
		"dev.zed.Zed-Nightly",
		"org.keepassxc.KeePassXC",
	}
	for _, id := range valid {
		if !looksLikeBundleID(id) {
			t.Fatalf("looksLikeBundleID(%q) = false, want true", id)
		}
	}

	invalid := []string{
		"",
		"com",
		".com.example",
		"com..example",
		"com.example.",
		"com/example/app",
		"com.*.app",
		"-com.example.app",
		"com.-example.app",
		"com.example_app",
	}
	for _, id := range invalid {
		if looksLikeBundleID(id) {
			t.Fatalf("looksLikeBundleID(%q) = true, want false", id)
		}
	}
}

func TestBundleDomainPrefixRequiresProductComponent(t *testing.T) {
	if got := bundleDomainPrefix("com.example.foo"); got != "com.example" {
		t.Fatalf("bundleDomainPrefix valid = %q, want com.example", got)
	}
	for _, id := range []string{"com", "com.example", "com..example.foo", "com.*.foo"} {
		if got := bundleDomainPrefix(id); got != "" {
			t.Fatalf("bundleDomainPrefix(%q) = %q, want empty", id, got)
		}
	}
}

func TestStopAppUsesBundleExecutableForProcessKill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldPoll := stopAppPollInterval
	stopAppPollInterval = 0
	t.Cleanup(func() { stopAppPollInterval = oldPoll })

	appPath := filepath.Join(home, "Applications", "Visual Studio Code.app")
	writeTestInfoPlist(t, appPath, "com.microsoft.VSCode", "Code")
	r := &processRunner{running: map[string]bool{"Code": true}}

	stopApp(context.Background(), r, AppEntry{Path: appPath, Name: "Visual Studio Code", BundleID: "com.microsoft.VSCode"})

	if !runnerCallContains(r.calls, `/usr/bin/osascript -e tell application id "com.microsoft.VSCode" to quit`) {
		t.Fatalf("osascript quit not called with bundle id: %#v", r.calls)
	}
	if !runnerCallContains(r.calls, "/usr/bin/pkill -x Code") {
		t.Fatalf("pkill not called with CFBundleExecutable: %#v", r.calls)
	}
	if runnerCallContains(r.calls, "/usr/bin/pkill -x Visual Studio Code") {
		t.Fatalf("pkill used display name instead of executable: %#v", r.calls)
	}
}

func TestStopAppFallsBackToAppNameWhenExecutableMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldPoll := stopAppPollInterval
	stopAppPollInterval = 0
	t.Cleanup(func() { stopAppPollInterval = oldPoll })

	r := &processRunner{running: map[string]bool{"Foo": true}}
	stopApp(context.Background(), r, AppEntry{Path: filepath.Join(home, "Applications", "Foo.app"), Name: "Foo"})
	if !runnerCallContains(r.calls, "/usr/bin/pkill -x Foo") {
		t.Fatalf("pkill did not fall back to app name: %#v", r.calls)
	}
}

type processRunner struct {
	running map[string]bool
	calls   []string
}

func (r *processRunner) LookPath(file string) (string, error) {
	return "/bin/" + file, nil
}

func (r *processRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	if name == "/usr/bin/pgrep" {
		if len(args) > 0 && r.running[args[len(args)-1]] {
			return CommandOutput{Stdout: "123\n"}
		}
		return CommandOutput{Err: errors.New("not running")}
	}
	if name == "/usr/bin/pkill" && len(args) > 0 {
		r.running[args[len(args)-1]] = false
	}
	return CommandOutput{}
}

func writeTestInfoPlist(t *testing.T, appPath, bundleID, executable string) {
	t.Helper()
	contents := filepath.Join(appPath, "Contents")
	if err := os.MkdirAll(contents, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key>
  <string>%s</string>
  <key>CFBundleExecutable</key>
  <string>%s</string>
</dict>
</plist>
`, bundleID, executable)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runnerCallContains(calls []string, want string) bool {
	for _, call := range calls {
		if strings.Contains(call, want) {
			return true
		}
	}
	return false
}

func TestBatchUninstallMovesAppAndRelatedFilesToTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	testTrash := filepath.Join(home, "trash-stub")
	t.Setenv("BLOOM_TEST_TRASH_DIR", testTrash)

	appPath := filepath.Join(home, "Applications", "Foo.app")
	appFile := filepath.Join(appPath, "Contents", "MacOS", "foo")
	cachePath := filepath.Join(home, "Library", "Caches", "com.example.foo")
	cacheFile := filepath.Join(cachePath, "cache.db")
	for _, path := range []string{appFile, cacheFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	summary := BatchUninstall(context.Background(), pathRunner{}, []AppEntry{{
		Path:     appPath,
		Name:     "Foo",
		BundleID: "com.example.foo",
	}}, false)
	if len(summary.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(summary.Results))
	}
	res := summary.Results[0]
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("failed paths = %#v", res.Failed)
	}
	for _, path := range []string{appPath, cachePath} {
		if !containsString(res.Files, path) {
			t.Fatalf("result files missing %q: %#v", path, res.Files)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("original path %q still exists or stat failed unexpectedly: %v", path, err)
		}
	}
	for _, path := range []string{
		filepath.Join(testTrash, "Foo.app", "Contents", "MacOS", "foo"),
		filepath.Join(testTrash, "com.example.foo", "cache.db"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("trashed path %q missing: %v", path, err)
		}
	}
}

func TestMovePathToTrashStubbornNeverPermanentDeletesOnTrashFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trashFile := filepath.Join(home, "not-a-trash-dir")
	if err := os.WriteFile(trashFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BLOOM_TEST_TRASH_DIR", trashFile)

	target := filepath.Join(home, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recordingRunner{outputs: map[string]CommandOutput{}}
	if err := movePathToTrashStubborn(context.Background(), r, target); err == nil {
		t.Fatal("movePathToTrashStubborn succeeded with invalid Trash directory")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target should remain after Trash failure: %v", err)
	}
	for _, call := range r.calls {
		if strings.Contains(call, "/bin/rm") || strings.HasPrefix(call, "sudo ") {
			t.Fatalf("permanent deletion command was used: calls = %#v", r.calls)
		}
	}
}
