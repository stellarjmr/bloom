package bloom

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func useUninstallSiblingRoots(t *testing.T, roots ...string) {
	t.Helper()
	previous := uninstallSiblingAppDirs
	uninstallSiblingAppDirs = append([]string(nil), roots...)
	t.Cleanup(func() { uninstallSiblingAppDirs = previous })
}

func TestScanUninstallBundleSiblingsMatchesBundleIDCaseInsensitively(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	useUninstallSiblingRoots(t, "~/Applications")

	selected := filepath.Join(home, "Applications", "Foo.app")
	sibling := filepath.Join(home, "Applications", "Foo Preview.app")
	unrelated := filepath.Join(home, "Applications", "Other.app")
	writeTestInfoPlist(t, selected, "com.example.Foo", "foo")
	writeTestInfoPlist(t, sibling, "COM.EXAMPLE.FOO", "foo-preview")
	writeTestInfoPlist(t, unrelated, "com.example.other", "other")

	scan := scanUninstallBundleSiblings(context.Background(), AppEntry{
		Path:     selected,
		Name:     "Foo",
		BundleID: "com.example.foo",
	})
	if !scan.Complete {
		t.Fatal("sibling scan unexpectedly incomplete")
	}
	if len(scan.Paths) != 1 || scan.Paths[0] != sibling {
		t.Fatalf("sibling paths = %#v, want [%q]", scan.Paths, sibling)
	}
}

func TestScanUninstallBundleSiblingsReportsIncompleteRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	selected := filepath.Join(home, "Applications", "Foo.app")
	writeTestInfoPlist(t, selected, "com.example.foo", "foo")
	invalidRoot := filepath.Join(home, "not-a-directory")
	if err := os.WriteFile(invalidRoot, []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	useUninstallSiblingRoots(t, "~/Applications", invalidRoot)

	scan := scanUninstallBundleSiblings(context.Background(), AppEntry{
		Path:     selected,
		Name:     "Foo",
		BundleID: "com.example.foo",
	})
	if scan.Complete {
		t.Fatal("sibling scan should be incomplete when an application root is unreadable")
	}
}

func TestBatchUninstallWithBundleSiblingMovesOnlySelectedBundle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	useUninstallSiblingRoots(t, "~/Applications")

	selected := filepath.Join(home, "Applications", "Foo.app")
	sibling := filepath.Join(home, "Applications", "Foo Preview.app")
	writeTestInfoPlist(t, selected, "com.example.foo", "foo")
	writeTestInfoPlist(t, sibling, "com.example.foo", "foo-preview")
	cache := filepath.Join(home, "Library", "Caches", "com.example.foo")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "cache.db"), []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &recordingRunner{outputs: map[string]CommandOutput{}}
	summary := BatchUninstall(context.Background(), runner, []AppEntry{{
		Path:     selected,
		Name:     "Foo",
		BundleID: "com.example.stale",
	}}, false)
	if len(summary.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(summary.Results))
	}
	res := summary.Results[0]
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	if !res.AppRemoved || !res.SharedBundleSibling || res.SharedBundleUncertain {
		t.Fatalf("unexpected shared-bundle result: %#v", res)
	}
	if res.App.BundleID != "com.example.foo" {
		t.Fatalf("bound bundle ID = %q, want actual Info.plist value", res.App.BundleID)
	}
	if len(res.Files) != 1 || res.Files[0] != selected {
		t.Fatalf("removed files = %#v, want selected bundle only", res.Files)
	}
	if _, err := os.Lstat(selected); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected app should be in Trash, stat error = %v", err)
	}
	for _, path := range []string{sibling, cache} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("shared path should remain %q: %v", path, err)
		}
	}
	for _, forbidden := range []string{"/usr/bin/pgrep", "/usr/bin/osascript", "/bin/launchctl", "/usr/libexec/PlistBuddy"} {
		if runnerCallContains(runner.calls, forbidden) {
			t.Fatalf("shared cleanup side effect %q was invoked: %#v", forbidden, runner.calls)
		}
	}
	if !runnerCallContains(runner.calls, lsregisterPath+" -u "+selected) ||
		!runnerCallContains(runner.calls, lsregisterPath+" -gc") {
		t.Fatalf("selected LaunchServices entry was not refreshed: %#v", runner.calls)
	}
}

func TestUninstallHomebrewCaskWithBundleSiblingRefusesZap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	useUninstallSiblingRoots(t, "~/Applications")

	selected := filepath.Join(home, "Applications", "foo.app")
	sibling := filepath.Join(home, "Applications", "foo-preview.app")
	writeTestInfoPlist(t, selected, "com.example.foo", "foo")
	writeTestInfoPlist(t, sibling, "com.example.foo", "foo-preview")
	cache := filepath.Join(home, "Library", "Caches", "com.example.foo")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &brewCaskUninstallRunner{appPath: selected, uninstallRemovePaths: []string{selected, cache}}
	res := UninstallApp(context.Background(), runner, AppEntry{
		Path:     selected,
		Name:     "foo",
		BundleID: "com.example.foo",
	}, false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "refusing Homebrew --zap") {
		t.Fatalf("uninstall error = %v, want shared-bundle zap refusal", res.Err)
	}
	if !res.SharedBundleSibling || res.BrewCask != "foo" || res.BrewRemoved || res.AppRemoved {
		t.Fatalf("unexpected refusal result: %#v", res)
	}
	if runnerCallContains(runner.calls, "brew uninstall") {
		t.Fatalf("brew zap ran despite sibling app: %#v", runner.calls)
	}
	for _, path := range []string{selected, sibling, cache} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("refused uninstall changed %q: %v", path, err)
		}
	}
}

type siblingCreatingRunner struct {
	onBrewLookup func()
	created      bool
	calls        []string
}

func (r *siblingCreatingRunner) LookPath(file string) (string, error) {
	if file == "brew" && !r.created {
		r.created = true
		r.onBrewLookup()
	}
	return "", errNotFound
}

func (r *siblingCreatingRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	return CommandOutput{}
}

func TestUninstallAppAbortsWhenSiblingAppearsAtActionBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	useUninstallSiblingRoots(t, "~/Applications")

	selected := filepath.Join(home, "Applications", "Foo.app")
	sibling := filepath.Join(home, "Applications", "Foo Preview.app")
	writeTestInfoPlist(t, selected, "com.example.foo", "foo")
	runner := &siblingCreatingRunner{onBrewLookup: func() {
		writeTestInfoPlist(t, sibling, "com.example.foo", "foo-preview")
	}}

	res := UninstallApp(context.Background(), runner, AppEntry{
		Path:     selected,
		Name:     "Foo",
		BundleID: "com.example.foo",
	}, false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "installed app copies changed") {
		t.Fatalf("uninstall error = %v, want action-boundary rescan failure", res.Err)
	}
	if !res.SharedBundleSibling || len(res.SiblingPaths) != 1 || res.SiblingPaths[0] != sibling {
		t.Fatalf("new sibling was not reported: %#v", res)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("side effects ran after sibling appeared: %#v", runner.calls)
	}
	for _, path := range []string{selected, sibling} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("action-boundary refusal changed %q: %v", path, err)
		}
	}
}

func TestUninstallAppAbortsWhenSelectedBundleIdentityChanges(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	useUninstallSiblingRoots(t, "~/Applications")

	selected := filepath.Join(home, "Applications", "Foo.app")
	writeTestInfoPlist(t, selected, "com.example.foo", "foo")
	replacementMarker := filepath.Join(selected, "replacement")
	runner := &siblingCreatingRunner{onBrewLookup: func() {
		if err := os.RemoveAll(selected); err != nil {
			t.Fatal(err)
		}
		writeTestInfoPlist(t, selected, "com.example.foo", "foo")
		if err := os.WriteFile(replacementMarker, []byte("replacement"), 0o644); err != nil {
			t.Fatal(err)
		}
	}}

	res := UninstallApp(context.Background(), runner, AppEntry{
		Path:     selected,
		Name:     "Foo",
		BundleID: "com.example.foo",
	}, false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "app bundle changed") {
		t.Fatalf("uninstall error = %v, want identity-change refusal", res.Err)
	}
	if res.AppRemoved || len(res.Files) != 0 || len(res.Moved) != 0 {
		t.Fatalf("replacement bundle was removed: %#v", res)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("side effects ran after bundle replacement: %#v", runner.calls)
	}
	if _, err := os.Lstat(replacementMarker); err != nil {
		t.Fatalf("replacement bundle should remain: %v", err)
	}
}

func TestPrintUninstallSummaryExplainsBundleOnlyMode(t *testing.T) {
	sibling := "/Applications/Foo Preview.app"
	summary := BatchSummary{Results: []UninstallResult{{
		App:                 AppEntry{Name: "Foo"},
		Files:               []string{"/Applications/Foo.app"},
		SharedBundleSibling: true,
		SiblingPaths:        []string{sibling},
	}}}
	var stdout, stderr bytes.Buffer
	app := App{Out: &stdout, Err: &stderr}
	processed, failures := app.printUninstallSummary(summary, true, true)
	if processed != 1 || failures != 0 {
		t.Fatalf("processed, failures = %d, %d; want 1, 0", processed, failures)
	}
	if !strings.Contains(stdout.String(), "kept shared app data") || !strings.Contains(stdout.String(), sibling) {
		t.Fatalf("shared-bundle explanation missing: %q", stdout.String())
	}
}
