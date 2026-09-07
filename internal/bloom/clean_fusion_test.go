package bloom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fusionTestApp struct {
	bundleID   string
	version    string
	executable string
}

type fusionTestRunner struct {
	processTables []string
	psCalls       int
	onPS          func(call int)
	lsofAvailable bool
	lsofOutput    CommandOutput
	aliasTarget   string
	apps          map[string]fusionTestApp
	aliasCalls    [][]string
}

func (r *fusionTestRunner) LookPath(file string) (string, error) {
	if file == "ps" || file == "lsof" && r.lsofAvailable {
		return "/usr/bin/" + file, nil
	}
	return "", os.ErrNotExist
}

func (r *fusionTestRunner) Run(ctx context.Context, name string, args ...string) CommandOutput {
	switch filepath.Base(name) {
	case "ps":
		index := r.psCalls
		r.psCalls++
		if r.onPS != nil {
			r.onPS(r.psCalls)
		}
		if len(r.processTables) == 0 {
			return CommandOutput{Stdout: "/sbin/launchd\n"}
		}
		if index >= len(r.processTables) {
			index = len(r.processTables) - 1
		}
		return CommandOutput{Stdout: r.processTables[index]}
	case "lsof":
		if cleanTestLsofVisibilityProbe(args) {
			return CommandOutput{Stdout: "p1\nu0\n"}
		}
		return r.lsofOutput
	case "osascript":
		r.aliasCalls = append(r.aliasCalls, append([]string{}, args...))
		if r.aliasTarget == "" {
			return CommandOutput{Err: errors.New("alias unresolved")}
		}
		return CommandOutput{Stdout: r.aliasTarget + "\n"}
	case "PlistBuddy":
		if len(args) != 3 {
			return CommandOutput{Err: errors.New("unexpected PlistBuddy arguments")}
		}
		appPath := filepath.Dir(filepath.Dir(args[2]))
		app, ok := r.apps[appPath]
		if !ok {
			return CommandOutput{Err: errors.New("unknown app")}
		}
		switch args[1] {
		case "Print :CFBundleIdentifier":
			return CommandOutput{Stdout: app.bundleID + "\n"}
		case "Print :CFBundleVersion":
			return CommandOutput{Stdout: app.version + "\n"}
		case "Print :CFBundleExecutable":
			return CommandOutput{Stdout: app.executable + "\n"}
		default:
			return CommandOutput{Err: errors.New("unknown plist key")}
		}
	case "du", "mdls":
		return OSRunner{}.Run(ctx, name, args...)
	default:
		return CommandOutput{Err: errors.New("unexpected command: " + name)}
	}
}

func makeFusionTestVersion(t *testing.T, root, hash, version string) (string, string, fusionTestApp) {
	t.Helper()
	dir := filepath.Join(root, hash)
	appPath := filepath.Join(dir, "Autodesk Fusion.app")
	executable := "Autodesk Fusion"
	macOSDir := filepath.Join(appPath, "Contents", "MacOS")
	if err := os.MkdirAll(macOSDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appPath, "Contents", "Info.plist"), []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macOSDir, executable), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, appPath, fusionTestApp{
		bundleID:   "com.autodesk.fusion360",
		version:    version,
		executable: executable,
	}
}

func makeFusionCurrentSymlink(t *testing.T, root, appPath string) string {
	t.Helper()
	alias := filepath.Join(root, "Autodesk Fusion.app")
	if err := os.Symlink(appPath, alias); err != nil {
		t.Fatal(err)
	}
	return alias
}

func newFusionTestRunner(t *testing.T, apps map[string]fusionTestApp) *fusionTestRunner {
	t.Helper()
	return &fusionTestRunner{
		lsofAvailable: true,
		lsofOutput:    codexClosedLsofOutput(t),
		apps:          apps,
	}
}

func TestRunCleanMovesOnlyVerifiedOlderAutodeskFusionBundleToTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trash := filepath.Join(home, "trash-stub")
	t.Setenv("BLOOM_TEST_TRASH_DIR", trash)
	root := autodeskFusionProductionRoot()

	oldDir, oldApp, oldMetadata := makeFusionTestVersion(t, root, strings.Repeat("a", 40), "2.9.0")
	currentDir, currentApp, currentMetadata := makeFusionTestVersion(t, root, strings.Repeat("b", 40), "3.0.0")
	newerDir, newerApp, newerMetadata := makeFusionTestVersion(t, root, strings.Repeat("c", 40), "3.1.0")
	equalDir, equalApp, equalMetadata := makeFusionTestVersion(t, root, strings.Repeat("d", 40), "3.0")
	invalidDir, invalidApp, invalidMetadata := makeFusionTestVersion(t, root, strings.Repeat("e", 40), "1.0.0")
	invalidMetadata.bundleID = "com.example.not-fusion"
	nonHashDir, nonHashApp, nonHashMetadata := makeFusionTestVersion(t, root, "old-version", "1.0.0")
	makeFusionCurrentSymlink(t, root, currentApp)

	runner := newFusionTestRunner(t, map[string]fusionTestApp{
		oldApp:     oldMetadata,
		currentApp: currentMetadata,
		newerApp:   newerMetadata,
		equalApp:   equalMetadata,
		invalidApp: invalidMetadata,
		nonHashApp: nonHashMetadata,
	})
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{Config: cfg, Runner: runner})
	if len(res.Failed) != 0 {
		t.Fatalf("Fusion cleanup failed: %#v", res.Failed)
	}
	if !cleanResultContains(res, oldDir) {
		t.Fatalf("verified old Fusion bundle missing: targets=%#v skipped=%#v", res.Targets, res.Skipped)
	}
	if _, err := os.Lstat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old Fusion directory should have moved to Trash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, filepath.Base(oldDir), "Autodesk Fusion.app", "Contents", "MacOS", "Autodesk Fusion")); err != nil {
		t.Fatalf("old Fusion bundle missing from Trash: %v", err)
	}
	for _, preserved := range []string{currentDir, newerDir, equalDir, invalidDir, nonHashDir} {
		if _, err := os.Stat(preserved); err != nil {
			t.Errorf("preserved Fusion directory %q was touched: %v", preserved, err)
		}
		if cleanResultContains(res, preserved) {
			t.Errorf("preserved Fusion directory appeared in targets: %q", preserved)
		}
	}
}

func TestRunCleanDefersAutodeskFusionWhileHelperIsActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := autodeskFusionProductionRoot()
	oldDir, oldApp, oldMetadata := makeFusionTestVersion(t, root, strings.Repeat("1", 40), "1.0")
	_, currentApp, currentMetadata := makeFusionTestVersion(t, root, strings.Repeat("2", 40), "2.0")
	makeFusionCurrentSymlink(t, root, currentApp)
	runner := newFusionTestRunner(t, map[string]fusionTestApp{oldApp: oldMetadata, currentApp: currentMetadata})
	runner.processTables = []string{"/Applications/Autodesk Fusion.app/Contents/MacOS/AcCoreConsole\n"}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg, Runner: runner})
	if cleanResultContains(res, oldDir) {
		t.Fatalf("active Fusion bundle appeared in targets: %#v", res.Targets)
	}
	if !cleanResultSkippedFor(res, oldDir, "Autodesk Fusion is running") {
		t.Fatalf("active Fusion skip missing: %#v", res.Skipped)
	}
}

func TestRunCleanFailsClosedWhenFusionOpenFileStateIsUnknown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := autodeskFusionProductionRoot()
	oldDir, oldApp, oldMetadata := makeFusionTestVersion(t, root, strings.Repeat("3", 40), "1.0")
	_, currentApp, currentMetadata := makeFusionTestVersion(t, root, strings.Repeat("4", 40), "2.0")
	makeFusionCurrentSymlink(t, root, currentApp)
	runner := newFusionTestRunner(t, map[string]fusionTestApp{oldApp: oldMetadata, currentApp: currentMetadata})
	runner.lsofAvailable = false

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg, Runner: runner})
	if cleanResultContains(res, oldDir) {
		t.Fatalf("Fusion bundle with unknown open-file state appeared in targets: %#v", res.Targets)
	}
	if !cleanResultSkippedFor(res, oldDir, "Autodesk Fusion open-file state unknown") {
		t.Fatalf("unknown open-file skip missing: %#v", res.Skipped)
	}
}

func TestRunCleanStopsWhenFusionCurrentAliasChangesAtActionBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	root := autodeskFusionProductionRoot()
	oldDir, oldApp, oldMetadata := makeFusionTestVersion(t, root, strings.Repeat("5", 40), "1.0")
	_, currentApp, currentMetadata := makeFusionTestVersion(t, root, strings.Repeat("6", 40), "2.0")
	_, newerApp, newerMetadata := makeFusionTestVersion(t, root, strings.Repeat("7", 40), "3.0")
	alias := makeFusionCurrentSymlink(t, root, currentApp)
	runner := newFusionTestRunner(t, map[string]fusionTestApp{
		oldApp: oldMetadata, currentApp: currentMetadata, newerApp: newerMetadata,
	})
	runner.onPS = func(call int) {
		if call != 5 {
			return
		}
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(newerApp, alias); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{Config: cfg, Runner: runner})
	if runner.psCalls < 5 {
		t.Fatalf("process probes = %d, want action-boundary recheck", runner.psCalls)
	}
	if cleanResultContains(res, oldDir) {
		t.Fatalf("old Fusion bundle moved after current alias changed: %#v", res.Targets)
	}
	if !cleanResultSkippedFor(res, oldDir, "Autodesk Fusion current version changed") {
		t.Fatalf("current-version change skip missing: %#v", res.Skipped)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("old Fusion bundle was touched after alias change: %v", err)
	}
}

func TestRunCleanRejectsReplacedFusionCandidateAtActionBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	root := autodeskFusionProductionRoot()
	hash := strings.Repeat("a", 40)
	oldDir, oldApp, oldMetadata := makeFusionTestVersion(t, root, hash, "1.0")
	_, currentApp, currentMetadata := makeFusionTestVersion(t, root, strings.Repeat("b", 40), "2.0")
	makeFusionCurrentSymlink(t, root, currentApp)
	runner := newFusionTestRunner(t, map[string]fusionTestApp{oldApp: oldMetadata, currentApp: currentMetadata})
	runner.onPS = func(call int) {
		if call != 5 {
			return
		}
		if err := os.Rename(oldDir, oldDir+".original"); err != nil {
			t.Fatal(err)
		}
		_, replacementApp, replacementMetadata := makeFusionTestVersion(t, root, hash, "1.0")
		runner.apps[replacementApp] = replacementMetadata
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{Config: cfg, Runner: runner})
	if cleanResultContains(res, oldDir) {
		t.Fatalf("replaced Fusion candidate moved to Trash: %#v", res.Targets)
	}
	if !cleanResultSkippedFor(res, oldDir, "path changed during clean") {
		t.Fatalf("replacement identity skip missing: %#v", res.Skipped)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("replacement Fusion candidate was touched: %v", err)
	}
}

func TestDiscoverAutodeskFusionResolvesFinderAliasWithoutFinderAutomation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := autodeskFusionProductionRoot()
	oldDir, oldApp, oldMetadata := makeFusionTestVersion(t, root, strings.Repeat("8", 40), "1.0")
	_, currentApp, currentMetadata := makeFusionTestVersion(t, root, strings.Repeat("9", 40), "2.0")
	alias := filepath.Join(root, "Autodesk Fusion.app")
	if err := os.WriteFile(alias, []byte("finder alias fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := newFusionTestRunner(t, map[string]fusionTestApp{oldApp: oldMetadata, currentApp: currentMetadata})
	runner.aliasTarget = currentApp

	targets := discoverAutodeskFusionTargets(context.Background(), runner)
	if len(targets) != 1 || targets[0].Path != oldDir {
		t.Fatalf("Finder alias discovery targets = %#v, want %q", targets, oldDir)
	}
	if len(runner.aliasCalls) < 2 {
		t.Fatalf("Finder alias was not rebound around metadata: %#v", runner.aliasCalls)
	}
	for _, args := range runner.aliasCalls {
		joined := strings.Join(args, " ")
		if len(args) != 5 || args[0] != "-l" || args[1] != "JavaScript" || args[2] != "-e" || args[4] != alias {
			t.Fatalf("unexpected osascript invocation: %#v", args)
		}
		if strings.Contains(strings.ToLower(joined), "tell application \"finder\"") {
			t.Fatalf("Finder automation appeared in alias resolver: %q", joined)
		}
	}
}

func TestDiscoverAutodeskFusionRejectsSymlinkedProductionAncestor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	applicationSupport := filepath.Join(home, "Library", "Application Support")
	physicalAutodesk := filepath.Join(home, "physical-autodesk")
	if err := os.MkdirAll(applicationSupport, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(physicalAutodesk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physicalAutodesk, filepath.Join(applicationSupport, "Autodesk")); err != nil {
		t.Skipf("cannot create symlink fixture: %v", err)
	}
	root := autodeskFusionProductionRoot()
	_, currentApp, currentMetadata := makeFusionTestVersion(t, root, strings.Repeat("a", 40), "2.0")
	makeFusionCurrentSymlink(t, root, currentApp)
	runner := newFusionTestRunner(t, map[string]fusionTestApp{currentApp: currentMetadata})
	if targets := discoverAutodeskFusionTargets(context.Background(), runner); len(targets) != 0 {
		t.Fatalf("symlinked production root yielded targets: %#v", targets)
	}
}

func TestCompareAutodeskFusionVersions(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      int
		ok        bool
	}{
		{candidate: "2.9.9", current: "3.0", want: -1, ok: true},
		{candidate: "3.0.0", current: "3", want: 0, ok: true},
		{candidate: "3.0.1", current: "3", want: 1, ok: true},
		{candidate: "3.beta", current: "3.0", ok: false},
		{candidate: "9999999999", current: "3.0", ok: false},
	}
	for _, test := range tests {
		got, ok := compareAutodeskFusionVersions(test.candidate, test.current)
		if ok != test.ok || ok && got != test.want {
			t.Errorf("compare(%q, %q) = (%d, %v), want (%d, %v)", test.candidate, test.current, got, ok, test.want, test.ok)
		}
	}
}
