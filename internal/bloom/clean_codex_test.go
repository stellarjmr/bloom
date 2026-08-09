package bloom

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type codexTestApp struct {
	bundleID string
	version  string
}

type codexTestRunner struct {
	processTables []string
	psCalls       int
	onPS          func(call int)
	lsofOutput    CommandOutput
	mdfindOutput  CommandOutput
	apps          map[string]codexTestApp
}

func (r *codexTestRunner) LookPath(file string) (string, error) {
	if file == "ps" || file == "lsof" {
		return "/usr/bin/" + file, nil
	}
	return "", os.ErrNotExist
}

func (r *codexTestRunner) Run(ctx context.Context, name string, args ...string) CommandOutput {
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
		return r.lsofOutput
	case "mdfind":
		return r.mdfindOutput
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
		default:
			return CommandOutput{Err: errors.New("unknown plist key")}
		}
	case "du", "mdls":
		return OSRunner{}.Run(ctx, name, args...)
	default:
		return CommandOutput{Err: errors.New("unexpected command: " + name)}
	}
}

func codexClosedLsofOutput(t *testing.T) CommandOutput {
	t.Helper()
	err := exec.Command("/usr/bin/false").Run()
	if err == nil {
		t.Fatal("false unexpectedly succeeded")
	}
	return CommandOutput{Err: err}
}

func makeCodexStagingDir(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "payload"), []byte("staging"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func makeCodexTestApp(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRunCleanTargetsOnlyAgedCodexMarketplaceStaging(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Now()
	old := now.Add(-31 * 24 * time.Hour)
	young := now.Add(-24 * time.Hour)
	bundledRoot := filepath.Join(home, ".codex", ".tmp", "bundled-marketplaces")
	marketRoot := filepath.Join(home, ".codex", ".tmp", "marketplaces", ".staging")
	cleanable := []string{
		filepath.Join(bundledRoot, "openai-bundled.staging-old"),
		filepath.Join(marketRoot, "marketplace-upgrade-old"),
		filepath.Join(marketRoot, "marketplace-add-old"),
	}
	preserved := []string{
		filepath.Join(bundledRoot, "openai-bundled"),
		filepath.Join(bundledRoot, "openai-bundled.staging-young"),
		filepath.Join(marketRoot, "marketplace-backup-old"),
		filepath.Join(marketRoot, "configured-marketplace"),
	}
	for _, path := range cleanable {
		makeCodexStagingDir(t, path, old)
	}
	for _, path := range preserved {
		age := old
		if strings.HasSuffix(path, "staging-young") {
			age = young
		}
		makeCodexStagingDir(t, path, age)
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	runner := &codexTestRunner{lsofOutput: codexClosedLsofOutput(t)}
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg, Runner: runner})
	for _, path := range cleanable {
		if !cleanResultContains(res, path) {
			t.Errorf("aged Codex staging target missing: %q (targets=%#v skipped=%#v failed=%#v)", path, res.Targets, res.Skipped, res.Failed)
		}
	}
	for _, path := range preserved {
		if cleanResultCovers(res, path) {
			t.Errorf("completed, backup, or young marketplace was targeted: %q", path)
		}
	}
}

func TestRunCleanMovesAgedCodexMarketplaceStagingOnlyToTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trash := filepath.Join(home, "trash-stub")
	t.Setenv("BLOOM_TEST_TRASH_DIR", trash)
	target := filepath.Join(home, ".codex", ".tmp", "bundled-marketplaces", "openai-bundled.staging-old")
	makeCodexStagingDir(t, target, time.Now().Add(-31*24*time.Hour))
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	runner := &codexTestRunner{lsofOutput: codexClosedLsofOutput(t)}

	res := RunClean(context.Background(), CleanOptions{Config: cfg, Runner: runner})
	if len(res.Failed) > 0 {
		t.Fatalf("Codex staging clean failed: %#v", res.Failed)
	}
	if !cleanResultContains(res, target) {
		t.Fatalf("moved Codex staging target missing: %#v", res.Targets)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("source staging directory still exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, filepath.Base(target), "payload")); err != nil {
		t.Fatalf("Codex staging was not moved to test Trash: %v", err)
	}
}

func TestRunCleanTargetsOnlyAgedCodexSparkleStaging(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "Library", "Caches", "com.openai.codex", "org.sparkle-project.Sparkle", "Installation")
	oldEntry := filepath.Join(root, "old-update")
	youngEntry := filepath.Join(root, "pending-download")
	makeCodexStagingDir(t, oldEntry, time.Now().Add(-31*24*time.Hour))
	makeCodexStagingDir(t, youngEntry, time.Now().Add(-24*time.Hour))
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{
		DryRun: true,
		Config: cfg,
		Runner: &codexTestRunner{lsofOutput: codexClosedLsofOutput(t)},
	})
	if !cleanResultContains(res, oldEntry) {
		t.Fatalf("aged Sparkle staging missing: targets=%#v skipped=%#v failed=%#v", res.Targets, res.Skipped, res.Failed)
	}
	if cleanResultContains(res, youngEntry) {
		t.Fatalf("young Sparkle staging was targeted: %#v", res.Targets)
	}
}

func TestRunCleanSkipsCodexStagingWhileActiveOrOpen(t *testing.T) {
	for _, test := range []struct {
		name      string
		processes string
		lsof      func(*testing.T, string) CommandOutput
		reason    string
	}{
		{
			name:      "Codex CLI active",
			processes: "/usr/local/bin/codex resume\n",
			lsof:      func(t *testing.T, _ string) CommandOutput { return codexClosedLsofOutput(t) },
			reason:    "Codex is running",
		},
		{
			name: "open staging file",
			lsof: func(_ *testing.T, path string) CommandOutput {
				return CommandOutput{Stdout: "p123\nn" + filepath.Join(path, "payload") + "\n"}
			},
			reason: "Codex staging files are open",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			target := filepath.Join(home, ".codex", ".tmp", "bundled-marketplaces", "openai-bundled.staging-old")
			makeCodexStagingDir(t, target, time.Now().Add(-31*24*time.Hour))
			cfg := DefaultConfig()
			cfg.Clean.Whitelist = nil
			var processTables []string
			if test.processes != "" {
				processTables = []string{test.processes}
			}
			runner := &codexTestRunner{
				processTables: processTables,
				lsofOutput:    test.lsof(t, target),
			}
			res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg, Runner: runner})
			if cleanResultContains(res, target) {
				t.Fatalf("active/open Codex staging appeared in targets: %#v", res.Targets)
			}
			if !cleanResultSkippedFor(res, target, test.reason) {
				t.Fatalf("skip reason %q missing: %#v", test.reason, res.Skipped)
			}
		})
	}
}

func TestRunCleanRechecksCodexStagingAgeAfterSizing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	target := filepath.Join(home, ".codex", ".tmp", "bundled-marketplaces", "openai-bundled.staging-old")
	makeCodexStagingDir(t, target, time.Now().Add(-31*24*time.Hour))
	runner := &codexTestRunner{lsofOutput: codexClosedLsofOutput(t)}
	runner.onPS = func(call int) {
		if call == 2 {
			now := time.Now()
			if err := os.Chtimes(target, now, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{Config: cfg, Runner: runner})
	if !cleanResultSkippedFor(res, target, "Codex staging entry is no longer stale") {
		t.Fatalf("changed-age skip missing: %#v", res.Skipped)
	}
	if _, err := os.Stat(filepath.Join(target, "payload")); err != nil {
		t.Fatalf("rejuvenated staging entry was touched: %v", err)
	}
}

func TestRunCleanFailsClosedWhenCodexStagingOpenFileProbeIsUnavailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".codex", ".tmp", "bundled-marketplaces", "openai-bundled.staging-old")
	makeCodexStagingDir(t, target, time.Now().Add(-31*24*time.Hour))
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{
		DryRun: true,
		Config: cfg,
		Runner: &cleanProbeRunner{processTables: []string{"/sbin/launchd\n"}},
	})
	if !cleanResultSkippedFor(res, target, "Codex staging open-file state unknown") {
		t.Fatalf("missing lsof did not fail closed: %#v", res.Skipped)
	}
}

func TestRunCleanHonorsWhitelistForCodexStagingException(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".codex", ".tmp", "bundled-marketplaces", "openai-bundled.staging-old")
	makeCodexStagingDir(t, target, time.Now().Add(-31*24*time.Hour))
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = []string{"~/.codex"}
	res := RunClean(context.Background(), CleanOptions{
		DryRun: true,
		Config: cfg,
		Runner: &codexTestRunner{lsofOutput: codexClosedLsofOutput(t)},
	})
	if cleanResultContains(res, target) {
		t.Fatalf("whitelisted Codex staging appeared in targets: %#v", res.Targets)
	}
	if !cleanResultSkippedFor(res, target, "whitelist") {
		t.Fatalf("Codex staging whitelist skip missing: %#v", res.Skipped)
	}
}

func TestCodexSparkleVersionEligibility(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "entry")
	stagedApp := filepath.Join(entry, "Codex.app")
	makeCodexTestApp(t, stagedApp)
	now := time.Now()
	runner := &codexTestRunner{apps: map[string]codexTestApp{
		stagedApp: {bundleID: "com.openai.codex", version: "200"},
	}}

	if mode, ok := cleanCodexSparkleEligibility(context.Background(), runner, entry, 100, true, now); ok || mode != "" {
		t.Fatalf("newer pending build was eligible: mode=%q ok=%v", mode, ok)
	}
	if mode, ok := cleanCodexSparkleEligibility(context.Background(), runner, entry, 200, true, now); !ok || mode != codexEligibilitySuperseded {
		t.Fatalf("superseded build eligibility = mode %q, ok %v", mode, ok)
	}

	runner.apps[stagedApp] = codexTestApp{bundleID: "com.example.other", version: "1"}
	old := now.Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(entry, old, old); err != nil {
		t.Fatal(err)
	}
	if mode, ok := cleanCodexSparkleEligibility(context.Background(), runner, entry, 200, true, now); !ok || mode != codexEligibilityAge {
		t.Fatalf("unknown staged identity did not fall back to age: mode=%q ok=%v", mode, ok)
	}

	runner.apps[stagedApp] = codexTestApp{bundleID: "com.openai.codex", version: "0000000123"}
	if build, ok := cleanCodexAppBuild(context.Background(), runner, stagedApp); !ok || build != 123 {
		t.Fatalf("base-10 build normalization = %d, %v", build, ok)
	}
	runner.apps[stagedApp] = codexTestApp{bundleID: "com.openai.codex", version: "12345678901"}
	if _, ok := cleanCodexAppBuild(context.Background(), runner, stagedApp); ok {
		t.Fatal("overlong build number was accepted")
	}
}

func TestCodexInstalledBuildRequiresSuccessfulCompleteUniqueScan(t *testing.T) {
	root := t.TempDir()
	appOne := filepath.Join(root, "Codex.app")
	appTwo := filepath.Join(root, "Copy", "Codex.app")
	makeCodexTestApp(t, appOne)
	makeCodexTestApp(t, appTwo)
	runner := &codexTestRunner{
		apps: map[string]codexTestApp{
			appOne: {bundleID: "com.openai.codex", version: "42"},
			appTwo: {bundleID: "com.openai.codex", version: "42"},
		},
		mdfindOutput: CommandOutput{Stdout: appTwo + "\n"},
	}
	if build, ok := cleanCodexInstalledBuildFromCandidates(context.Background(), runner, []string{appOne}); !ok || build != 42 {
		t.Fatalf("unique installed build = %d, %v", build, ok)
	}

	runner.apps[appTwo] = codexTestApp{bundleID: "com.openai.codex", version: "43"}
	if _, ok := cleanCodexInstalledBuildFromCandidates(context.Background(), runner, []string{appOne}); ok {
		t.Fatal("conflicting installed builds were treated as unique")
	}
	runner.apps[appTwo] = codexTestApp{bundleID: "com.openai.codex", version: "42"}
	runner.mdfindOutput = CommandOutput{Err: errors.New("mdfind failed")}
	if _, ok := cleanCodexInstalledBuildFromCandidates(context.Background(), runner, []string{appOne}); ok {
		t.Fatal("failed installed-app search authorized a version comparison")
	}
}

func TestCodexStagingProcessGuardsRecognizeDesktopCLIAndSparkle(t *testing.T) {
	if !cleanCodexRuntimeActive(
		"/Applications/ChatGPT.app/Contents/MacOS/ChatGPT\n",
		cleanSpecialCodexSparkle,
	) {
		t.Fatal("ChatGPT desktop process was not recognized as Codex Desktop")
	}
	if cleanCodexRuntimeActive("/usr/local/bin/codex resume\n", cleanSpecialCodexSparkle) {
		t.Fatal("Codex CLI incorrectly blocked the independent desktop Sparkle cache")
	}
	if !cleanCodexRuntimeActive("/usr/local/bin/codex resume\n", cleanSpecialCodexMarketplace) {
		t.Fatal("Codex CLI did not protect marketplace staging")
	}
	if !cleanCodexSparkleUpdaterActive(
		"/Applications/Codex.app/Contents/Frameworks/Sparkle.framework/Versions/B/Autoupdate\n",
	) {
		t.Fatal("Sparkle updater process was not recognized")
	}
}

func TestCodexStagingPathGateRejectsSymlinkComponents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	outside := filepath.Join(home, "outside")
	makeCodexStagingDir(t, filepath.Join(outside, "openai-bundled.staging-old"), time.Now().Add(-31*24*time.Hour))
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".codex", ".tmp")); err != nil {
		t.Skipf("cannot create symlink fixture: %v", err)
	}
	root := filepath.Join(home, ".codex", ".tmp", "bundled-marketplaces")
	if cleanCodexStagingPathSafe(root, root) {
		t.Fatal("Codex staging root with a symlink component was accepted")
	}
}
