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

func TestPrintCleanResultGroupsTargetsByLabel(t *testing.T) {
	res := CleanResult{
		Targets: []CleanTarget{
			{Label: "User app cache", Path: "/Users/test/Library/Caches/Zed", SizeKB: 1024},
			{Label: "npm package cache", Path: "/Users/test/.npm/_cacache/tmp", SizeKB: 16},
			{Label: "User app cache", Path: "/Users/test/Library/Caches/Homebrew", SizeKB: 2048},
		},
		Skipped: []CleanSkip{{Path: "/Users/test/Library/Caches/PassKit", Reason: "protected"}},
		TotalKB: 3096,
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	printCleanResult(&stdout, &stderr, res)
	out := stdout.String()
	if strings.Count(out, "✓ User app cache") != 1 {
		t.Fatalf("User app cache should be grouped once, output = %q", out)
	}
	if !strings.Contains(out, "✓ User app cache  3.0M  (2 items)") {
		t.Fatalf("group header did not include summed size and item count: %q", out)
	}
	for _, want := range []string{
		"   ├── /Users/test/Library/Caches/Zed",
		"   └── /Users/test/Library/Caches/Homebrew",
		"✓ npm package cache  16.0K  (1 item)",
		"Moved 3 items to Trash (3.0M)",
		"Skipped 1 protected, whitelisted, invalid, or Trash items",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %q", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
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

func TestDedupeAppEntriesByBundleIDPrefersLiveRoots(t *testing.T) {
	home := filepath.Join(string(os.PathSeparator), "Users", "test")
	apps := []AppEntry{
		{Path: "/Applications/Backup/Foo.app", Name: "Foo Backup", BundleID: "COM.EXAMPLE.FOO"},
		{Path: filepath.Join(home, "Applications", "Foo.app"), Name: "Foo Home", BundleID: "com.example.foo"},
		{Path: "/Applications/Setapp/Foo.app", Name: "Foo Setapp", BundleID: "com.example.foo"},
		{Path: "/Applications/Foo.app", Name: "Foo", BundleID: "com.example.foo"},
		{Path: "/Applications/NoID.app", Name: "NoID"},
		{Path: "/Applications/NoID Clone.app", Name: "NoID Clone"},
	}

	got := dedupeAppEntriesByBundleID(apps, home)
	if len(got) != 3 {
		t.Fatalf("deduped app count = %d, want 3: %#v", len(got), got)
	}
	if got[0].Path != "/Applications/Foo.app" {
		t.Fatalf("preferred duplicate path = %q, want /Applications/Foo.app", got[0].Path)
	}
	if got[1].Path != "/Applications/NoID.app" || got[2].Path != "/Applications/NoID Clone.app" {
		t.Fatalf("apps without bundle IDs should be preserved in order: %#v", got)
	}
}

func TestDedupeAppEntriesByBundleIDTreatsSetappAsLiveRoot(t *testing.T) {
	home := filepath.Join(string(os.PathSeparator), "Users", "test")
	apps := []AppEntry{
		{Path: "/Applications/Backup/Foo.app", Name: "Foo Backup", BundleID: "com.example.foo"},
		{Path: "/Applications/Setapp/Foo.app", Name: "Foo Setapp", BundleID: "com.example.foo"},
	}

	got := dedupeAppEntriesByBundleID(apps, home)
	if len(got) != 1 || got[0].Path != "/Applications/Setapp/Foo.app" {
		t.Fatalf("deduped apps = %#v, want Setapp app", got)
	}
}

func TestUninstallAppBlocksOfficialUninstallerApps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	appPath := filepath.Join(home, "Applications", "Falcon.app")
	if err := os.MkdirAll(appPath, 0o755); err != nil {
		t.Fatal(err)
	}

	res := UninstallApp(context.Background(), pathRunner{}, AppEntry{
		Path:     appPath,
		Name:     "Falcon",
		BundleID: "com.crowdstrike.falcon.UserAgent",
	}, false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "official CrowdStrike uninstaller") {
		t.Fatalf("error = %v, want official uninstaller block", res.Err)
	}
	if _, err := os.Stat(appPath); err != nil {
		t.Fatalf("blocked app should not be touched: %v", err)
	}
}

func TestOfficialUninstallerVendorDoesNotBlockGenericFalconNames(t *testing.T) {
	if vendor := officialUninstallerVendor(AppEntry{Name: "Falcon", BundleID: "com.example.falcon", Path: "/Applications/Falcon.app"}); vendor != "" {
		t.Fatalf("generic Falcon app was blocked as %q", vendor)
	}
}

func TestReferenceTokenMatchingAvoidsSubstringFalsePositives(t *testing.T) {
	appPath := "/Applications/Foo.app"
	if !containsAppPathReference(`<string>/Applications/Foo.app/Contents/MacOS/foo</string>`, appPath) {
		t.Fatal("app path reference with child path was not matched")
	}
	if containsAppPathReference(`<string>/Applications/Foo.app.backup</string>`, appPath) {
		t.Fatal("app path substring was treated as a reference")
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

func TestFindRelatedPathsKeepsTokenMatchesOnBoundaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := filepath.Join(home, "Applications", "TestApp.app")

	wantPaths := []string{
		appPath,
		filepath.Join(home, "Library", "Caches", "com.example.TestApp.ShipIt"),
		filepath.Join(home, "Library", "Containers", "com.example.TestApp.helper"),
		filepath.Join(home, "Library", "Application Scripts", "TEAM.com.example.TestApp.Extension"),
		filepath.Join(home, "Library", "Preferences", "ByHost", "com.example.TestApp.ABC123.plist"),
		filepath.Join(home, "Library", "LaunchAgents", "com.example.TestApp.helper.plist"),
	}
	for _, path := range wantPaths {
		if strings.HasSuffix(path, ".plist") {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("plist"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(home, "Library", "Caches", "com.example.TestApplication.ShipIt"),
		filepath.Join(home, "Library", "Containers", "com.example.TestApplication"),
		filepath.Join(home, "Library", "Application Scripts", "TEAM.com.example.TestApplication.Extension"),
		filepath.Join(home, "Library", "Preferences", "ByHost", "com.example.TestApplication.ABC123.plist"),
		filepath.Join(home, "Library", "LaunchAgents", "com.example.TestApplication.plist"),
	} {
		if strings.HasSuffix(path, ".plist") {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("plist"), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	paths := FindRelatedPaths(AppEntry{Path: appPath, Name: "TestApp", BundleID: "com.example.TestApp"})
	for _, path := range wantPaths {
		if !containsString(paths, path) {
			t.Fatalf("paths missing boundary match %q: %#v", path, paths)
		}
	}
	if strings.Contains(strings.Join(paths, "\n"), "TestApplication") {
		t.Fatalf("matched sibling bundle prefix: %#v", paths)
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

func TestStopAppSendsQuitThenTerminatesVerifiedBundleProcess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldPoll := stopAppPollInterval
	stopAppPollInterval = 0
	t.Cleanup(func() { stopAppPollInterval = oldPoll })

	appPath := filepath.Join(home, "Applications", "Visual Studio Code.app")
	writeTestInfoPlist(t, appPath, "com.microsoft.VSCode", "Code")
	launchDir := filepath.Join(home, "Library", "LaunchAgents")
	for _, name := range []string{"com.microsoft.VSCode.plist", "com.microsoft.VSCode.helper.plist", "com.microsoft.VSCodeInsiders.plist"} {
		path := filepath.Join(launchDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("plist"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := &processRunner{
		pids: map[string][]string{"Code": []string{"123"}},
		processPaths: map[string]string{
			"123": filepath.Join(appPath, "Contents", "MacOS", "Code"),
		},
	}

	stillRunning := stopApp(context.Background(), r, AppEntry{Path: appPath, Name: "Visual Studio Code", BundleID: "com.microsoft.VSCode"})

	if !runnerCallContains(r.calls, "/usr/bin/osascript -e tell application id \"com.microsoft.VSCode\" to quit") {
		t.Fatalf("osascript quit not called with bundle id: %#v", r.calls)
	}
	if stillRunning {
		t.Fatalf("stopApp should force-terminate the verified app process: %#v", r.calls)
	}
	if !runnerCallContains(r.calls, "/bin/kill -TERM 123") {
		t.Fatalf("stopApp did not send SIGTERM to verified app process: %#v", r.calls)
	}
	if runnerCallContains(r.calls, "/bin/kill -KILL") {
		t.Fatalf("stopApp sent SIGKILL even though SIGTERM exited the app: %#v", r.calls)
	}
	if runnerCallContains(r.calls, "/usr/bin/pkill") {
		t.Fatalf("stopApp should not use name-only pkill: %#v", r.calls)
	}
	if !runnerCallContains(r.calls, filepath.Join(launchDir, "com.microsoft.VSCode.plist")) || !runnerCallContains(r.calls, filepath.Join(launchDir, "com.microsoft.VSCode.helper.plist")) {
		t.Fatalf("matching LaunchAgents were not unloaded: %#v", r.calls)
	}
	if runnerCallContains(r.calls, "com.microsoft.VSCodeInsiders.plist") {
		t.Fatalf("sibling bundle LaunchAgent was unloaded: %#v", r.calls)
	}
}

func TestStopAppDoesNotKillSameNameProcessOutsideBundle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldPoll := stopAppPollInterval
	stopAppPollInterval = 0
	t.Cleanup(func() { stopAppPollInterval = oldPoll })

	appPath := filepath.Join(home, "Applications", "Visual Studio Code.app")
	writeTestInfoPlist(t, appPath, "com.microsoft.VSCode", "Code")
	r := &processRunner{
		pids: map[string][]string{"Code": []string{"123", "456"}},
		processPaths: map[string]string{
			"123": filepath.Join(appPath, "Contents", "MacOS", "Code"),
			"456": filepath.Join(home, "Other.app", "Contents", "MacOS", "Code"),
		},
	}

	stillRunning := stopApp(context.Background(), r, AppEntry{Path: appPath, Name: "Visual Studio Code", BundleID: "com.microsoft.VSCode"})

	if stillRunning {
		t.Fatalf("stopApp should not treat an unrelated same-name process as the target app: %#v", r.calls)
	}
	if !runnerCallContains(r.calls, "/bin/kill -TERM 123") {
		t.Fatalf("stopApp did not terminate the verified target process: %#v", r.calls)
	}
	if runnerCallContains(r.calls, "/bin/kill -TERM 456") || runnerCallContains(r.calls, "/bin/kill -KILL 456") {
		t.Fatalf("stopApp killed a same-name process outside the target bundle: %#v", r.calls)
	}
}

func TestStopAppEscalatesToSIGKILLWhenSIGTERMFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldPoll := stopAppPollInterval
	stopAppPollInterval = 0
	t.Cleanup(func() { stopAppPollInterval = oldPoll })

	appPath := filepath.Join(home, "Applications", "Foo.app")
	writeTestInfoPlist(t, appPath, "com.example.foo", "Foo")
	r := &processRunner{
		pids: map[string][]string{"Foo": []string{"123"}},
		processPaths: map[string]string{
			"123": filepath.Join(appPath, "Contents", "MacOS", "Foo"),
		},
		ignoreTERM: map[string]bool{"123": true},
	}

	stillRunning := stopApp(context.Background(), r, AppEntry{Path: appPath, Name: "Foo", BundleID: "com.example.foo"})

	if stillRunning {
		t.Fatalf("stopApp should report exited after SIGKILL succeeds: %#v", r.calls)
	}
	if !runnerCallContains(r.calls, "/bin/kill -TERM 123") {
		t.Fatalf("stopApp did not try SIGTERM before SIGKILL: %#v", r.calls)
	}
	if !runnerCallContains(r.calls, "/bin/kill -KILL 123") {
		t.Fatalf("stopApp did not escalate to SIGKILL: %#v", r.calls)
	}
}

func TestStopAppFallsBackToAppNameForQuitWhenBundleIDMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldPoll := stopAppPollInterval
	stopAppPollInterval = 0
	t.Cleanup(func() { stopAppPollInterval = oldPoll })

	r := &processRunner{running: map[string]bool{"Foo": true}}
	stillRunning := stopApp(context.Background(), r, AppEntry{Path: filepath.Join(home, "Applications", "Foo.app"), Name: "Foo"})
	if !runnerCallContains(r.calls, "/usr/bin/osascript -e tell application \"Foo\" to quit") {
		t.Fatalf("osascript quit did not fall back to app name: %#v", r.calls)
	}
	if !stillRunning {
		t.Fatalf("stopApp should report the app still running when graceful quit does not exit: %#v", r.calls)
	}
	if runnerCallContains(r.calls, "/usr/bin/pkill") {
		t.Fatalf("stopApp should not use name-only pkill: %#v", r.calls)
	}
	if runnerCallContains(r.calls, "/bin/kill") {
		t.Fatalf("stopApp should not kill without verifying the process path: %#v", r.calls)
	}
}

type processRunner struct {
	running      map[string]bool
	pids         map[string][]string
	processPaths map[string]string
	ignoreTERM   map[string]bool
	exited       map[string]bool
	calls        []string
}

func (r *processRunner) LookPath(file string) (string, error) {
	return "/bin/" + file, nil
}

func (r *processRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	if name == "/usr/bin/pgrep" {
		if len(args) == 0 {
			return CommandOutput{Err: errors.New("missing process name")}
		}
		matchName := args[len(args)-1]
		if pids, ok := r.pids[matchName]; ok {
			var live []string
			for _, pid := range pids {
				if !r.exited[pid] {
					live = append(live, pid)
				}
			}
			if len(live) > 0 {
				return CommandOutput{Stdout: strings.Join(live, "\n") + "\n"}
			}
			return CommandOutput{Err: errors.New("not running")}
		}
		if r.running[matchName] {
			return CommandOutput{Stdout: "123\n"}
		}
		return CommandOutput{Err: errors.New("not running")}
	}
	if name == "/bin/ps" {
		pid := ""
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-p" {
				pid = args[i+1]
				break
			}
		}
		if pid == "" || r.exited[pid] {
			return CommandOutput{Err: errors.New("not running")}
		}
		if path := r.processPaths[pid]; path != "" {
			return CommandOutput{Stdout: path + "\n"}
		}
		return CommandOutput{Err: errors.New("unknown process")}
	}
	if name == "/bin/kill" {
		if len(args) > 0 {
			pid := args[len(args)-1]
			if args[0] == "-TERM" && r.ignoreTERM[pid] {
				return CommandOutput{}
			}
			if r.exited == nil {
				r.exited = map[string]bool{}
			}
			r.exited[pid] = true
		}
		return CommandOutput{}
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
	launchAgentPath := filepath.Join(home, "Library", "LaunchAgents", "com.other.foo-helper.plist")
	for _, path := range []string{appFile, cacheFile, launchAgentPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		contents := []byte("data")
		if path == launchAgentPath {
			contents = []byte(appPath)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
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
	for _, path := range []string{appPath, cachePath, launchAgentPath} {
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
		filepath.Join(testTrash, "com.other.foo-helper.plist"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("trashed path %q missing: %v", path, err)
		}
	}
}

func TestFindRelatedPathsSkipsDotConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	appPath := filepath.Join(home, ".config", "Foo.app")
	writeTestInfoPlist(t, appPath, "com.example.foo", "foo")

	paths := FindRelatedPaths(AppEntry{Path: appPath, Name: "Foo", BundleID: "com.example.foo"})
	if containsString(paths, appPath) {
		t.Fatalf("FindRelatedPaths included ~/.config path: %#v", paths)
	}
}

func TestUninstallAppUsesBrewCaskWithZap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))

	appPath := filepath.Join(home, "Applications", "foo.app")
	writeTestInfoPlist(t, appPath, "com.example.foo", "foo")
	configPath := filepath.Join(home, ".config", "foo", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &brewCaskUninstallRunner{
		appPath:              appPath,
		uninstallRemovePaths: []string{appPath},
		zapRemovePaths:       []string{filepath.Dir(configPath)},
	}

	res := UninstallApp(context.Background(), r, AppEntry{Path: appPath, Name: "foo", BundleID: "com.example.foo"}, false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	if !runnerCallContains(r.calls, "brew uninstall --cask --force --zap foo") {
		t.Fatalf("brew uninstall with zap was not called: %#v", r.calls)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("~/.config file should be removed by brew zap; stat err = %v", err)
	}
}

func TestUninstallAppKeepsPlannedStatsWhenBrewUninstallRemovesPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))

	appPath := filepath.Join(home, "Applications", "foo.app")
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

	r := &brewCaskUninstallRunner{
		appPath:              appPath,
		uninstallRemovePaths: []string{appPath, cachePath},
	}
	res := UninstallApp(context.Background(), r, AppEntry{
		Path:     appPath,
		Name:     "foo",
		BundleID: "com.example.foo",
	}, false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	if !res.BrewRemoved {
		t.Fatal("brew removal was not recorded")
	}
	for _, path := range []string{appPath, cachePath} {
		if !containsString(res.Files, path) {
			t.Fatalf("result files missing %q after brew uninstall removed it: %#v", path, res.Files)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("original path %q still exists or stat failed unexpectedly: %v", path, err)
		}
	}
	if res.RemovedKB == 0 {
		t.Fatalf("removed size = 0, want planned size from paths removed by brew uninstall")
	}
	if !runnerCallContains(r.calls, "brew uninstall --cask --force --zap foo") {
		t.Fatalf("brew uninstall with zap was not called: %#v", r.calls)
	}
}

func TestUninstallAppBootsOutLoginItemHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))

	appPath := filepath.Join(home, "Applications", "Foo.app")
	writeTestInfoPlist(t, appPath, "com.example.foo", "foo")
	helperPath := filepath.Join(appPath, "Contents", "Library", "LoginItems", "Foo Helper.app")
	writeTestInfoPlist(t, helperPath, "com.example.foo.helper", "foo-helper")

	r := &recordingRunner{outputs: map[string]CommandOutput{}}
	res := UninstallApp(context.Background(), r, AppEntry{Path: appPath, Name: "Foo", BundleID: "com.example.foo"}, false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	want := fmt.Sprintf("/bin/launchctl bootout gui/%d/com.example.foo.helper", os.Getuid())
	if !runnerCallContains(r.calls, want) {
		t.Fatalf("login item helper was not booted out; calls = %#v", r.calls)
	}
}

func TestUninstallAppSkipsPostRemovalSideEffectsWhenBundleRemains(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trashFile := filepath.Join(home, "not-a-trash-dir")
	if err := os.WriteFile(trashFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BLOOM_TEST_TRASH_DIR", trashFile)

	appPath := filepath.Join(home, "Applications", "Foo.app")
	writeTestInfoPlist(t, appPath, "com.example.foo", "foo")
	r := &recordingRunner{outputs: map[string]CommandOutput{}}
	res := UninstallApp(context.Background(), r, AppEntry{Path: appPath, Name: "Foo", BundleID: "com.example.foo"}, false)
	if res.Err == nil {
		t.Fatal("uninstall unexpectedly succeeded with invalid Trash directory")
	}
	if res.AppRemoved {
		t.Fatal("AppRemoved = true even though bundle stayed on disk")
	}
	for _, forbidden := range []string{"/usr/bin/osascript", "/bin/launchctl bootout", lsregisterPath + " -u"} {
		if runnerCallContains(r.calls, forbidden) {
			t.Fatalf("post-removal side effect %q ran before bundle removal: %#v", forbidden, r.calls)
		}
	}
}

func TestBatchUninstallDoesNotQueryBackgroundTaskManagement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))

	appPath := filepath.Join(home, "Applications", "Foo.app")
	writeTestInfoPlist(t, appPath, "com.example.foo", "foo")
	r := &recordingRunner{paths: map[string]bool{"sfltool": true}, outputs: map[string]CommandOutput{}}
	summary := BatchUninstall(context.Background(), r, []AppEntry{{
		Path:     appPath,
		Name:     "Foo",
		BundleID: "com.example.foo",
	}}, false)

	if len(summary.Results) != 1 || summary.Results[0].Err != nil {
		t.Fatalf("uninstall summary = %#v", summary.Results)
	}
	if runnerCallContains(r.calls, "sfltool") {
		t.Fatalf("uninstall queried Background Task Management: %#v", r.calls)
	}
}

func TestRemoveAppsFromDockMatchesBundleIDAcrossDockArrays(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dockPath := dockPlistPath()
	r := &recordingRunner{outputs: map[string]CommandOutput{
		"/usr/libexec/PlistBuddy -c Print :persistent-others " + dockPath:                                    {Stdout: "Dict {\n}\n"},
		"/usr/libexec/PlistBuddy -c Print :persistent-others:0:tile-data:file-data:_CFURLString " + dockPath: {Stdout: "file:///Applications/Other.app/\n"},
		"/usr/libexec/PlistBuddy -c Print :persistent-others:0:tile-data:bundle-identifier " + dockPath:      {Stdout: "com.example.foo\n"},
		"/usr/libexec/PlistBuddy -c Delete :persistent-others:0 " + dockPath:                                 {},
	}}

	removeAppsFromDock(context.Background(), r, []AppEntry{{Path: "/Applications/Foo.app", BundleID: "com.example.foo"}})
	if !runnerCallContains(r.calls, "Delete :persistent-others:0") {
		t.Fatalf("dock tile was not deleted by bundle id; calls = %#v", r.calls)
	}
	if !runnerCallContains(r.calls, "/usr/bin/killall Dock") {
		t.Fatalf("Dock was not restarted after deletion; calls = %#v", r.calls)
	}
}

type brewCaskUninstallRunner struct {
	appPath              string
	uninstallRemovePaths []string
	zapRemovePaths       []string
	uninstalled          bool
	calls                []string
}

func (r *brewCaskUninstallRunner) LookPath(file string) (string, error) {
	if file == "brew" {
		return "/bin/brew", nil
	}
	return "", errNotFound
}

func (r *brewCaskUninstallRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	switch call {
	case "brew list --cask":
		if r.uninstalled {
			return CommandOutput{}
		}
		return CommandOutput{Stdout: "foo\n"}
	case "brew info --cask foo":
		return CommandOutput{Stdout: r.appPath + "\n"}
	case "brew uninstall --cask --force --zap foo":
		for _, path := range r.uninstallRemovePaths {
			_ = os.RemoveAll(path)
		}
		for _, path := range r.zapRemovePaths {
			_ = os.RemoveAll(path)
		}
		r.uninstalled = true
		return CommandOutput{}
	default:
		return CommandOutput{Err: errNotFound}
	}
}

func TestPrintUninstallSummaryCanHideFilesAfterConfirmation(t *testing.T) {
	summary := BatchSummary{
		Results: []UninstallResult{{
			App:       AppEntry{Name: "Foo"},
			RemovedKB: 2048,
			Files: []string{
				"/Applications/Foo.app",
				"/Users/test/Library/Containers/com.example.foo",
			},
			Failed: []string{"/Users/test/Library/Application Scripts/com.example.foo"},
		}},
		TotalRemovedKB: 2048,
		BrewAutoremove: true,
	}

	var previewOut, previewErr bytes.Buffer
	previewApp := App{Out: &previewOut, Err: &previewErr}
	processed, failures := previewApp.printUninstallSummary(summary, true, true)
	if processed != 1 || failures != 0 {
		t.Fatalf("preview processed, failures = %d, %d; want 1, 0", processed, failures)
	}
	if !strings.Contains(previewOut.String(), "   · /Applications/Foo.app") {
		t.Fatalf("preview output did not include file list: %q", previewOut.String())
	}
	if !strings.Contains(previewOut.String(), "would run brew autoremove") {
		t.Fatalf("preview output did not describe pending brew autoremove: %q", previewOut.String())
	}
	if !strings.Contains(previewErr.String(), "could not move to Trash") {
		t.Fatalf("preview stderr did not include failures: %q", previewErr.String())
	}

	var resultOut, resultErr bytes.Buffer
	resultApp := App{Out: &resultOut, Err: &resultErr}
	processed, failures = resultApp.printUninstallSummary(summary, false, false)
	if processed != 1 || failures != 0 {
		t.Fatalf("result processed, failures = %d, %d; want 1, 0", processed, failures)
	}
	out := resultOut.String()
	if strings.Contains(out, "/Applications/Foo.app") || strings.Contains(out, "/Users/test/Library/Containers/com.example.foo") {
		t.Fatalf("result output repeated file list: %q", out)
	}
	if !strings.Contains(out, "✓ Foo") || !strings.Contains(out, "Uninstalled 1 apps, moved 2.0M to Trash") {
		t.Fatalf("result output missing app line or summary: %q", out)
	}
	if !strings.Contains(out, "ran brew autoremove") {
		t.Fatalf("result output did not report completed brew autoremove: %q", out)
	}
	if !strings.Contains(resultErr.String(), "could not move to Trash") {
		t.Fatalf("result stderr did not include failures: %q", resultErr.String())
	}
}

func TestPrintUninstallSummaryWarnsWhenAppStillRunning(t *testing.T) {
	summary := BatchSummary{
		Results: []UninstallResult{{
			App:          AppEntry{Name: "Foo"},
			Files:        []string{"/Applications/Foo.app"},
			StillRunning: true,
		}},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := App{Out: &stdout, Err: &stderr}
	processed, failures := app.printUninstallSummary(summary, false, false)
	if processed != 1 || failures != 0 {
		t.Fatalf("processed, failures = %d, %d; want 1, 0", processed, failures)
	}
	if !strings.Contains(stderr.String(), "Foo may still be running") {
		t.Fatalf("stderr missing still-running warning: %q", stderr.String())
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

func TestMovePathToTrashDoesNotUseInteractiveHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", "")
	trashFile := filepath.Join(home, ".Trash")
	if err := os.WriteFile(trashFile, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recordingRunner{
		paths:   map[string]bool{"trash": true},
		outputs: map[string]CommandOutput{"trash " + target: {}},
	}

	if err := movePathToTrash(context.Background(), r, target); err == nil {
		t.Fatal("movePathToTrash succeeded with invalid Trash directory")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target should remain after Trash failure: %v", err)
	}
	for _, call := range r.calls {
		if strings.Contains(call, "trash ") || strings.Contains(call, "/usr/bin/osascript") {
			t.Fatalf("interactive Trash helper was used: calls = %#v", r.calls)
		}
	}
}
