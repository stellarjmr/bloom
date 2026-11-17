package bloom

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFamilyTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func familyTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	useUninstallSiblingRoots(t, "~/Applications")
	return home
}

func TestAppFamilyBaseName(t *testing.T) {
	cases := map[string]string{
		"Firefox":                       "Firefox",
		"Firefox Nightly":               "Firefox",
		"Firefox Developer Edition":     "Firefox",
		"Zed Preview":                   "Zed",
		"Google Chrome Canary":          "Google Chrome",
		"Visual Studio Code - Insiders": "Visual Studio Code",
		"Safari Technology Preview":     "Safari",
		"Microsoft Edge":                "Microsoft Edge",
		"Microsoft Edge Beta":           "Microsoft Edge",
		"ZedPreview":                    "ZedPreview",
		"Zedit":                         "Zedit",
		"Go Beta":                       "Go Beta",
		"Beta":                          "Beta",
	}
	for name, want := range cases {
		if got := appFamilyBaseName(name); got != want {
			t.Errorf("appFamilyBaseName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRelateBundleIDs(t *testing.T) {
	cases := []struct {
		candidate, selected string
		want                bundleIDRelation
	}{
		{"com.google.Chrome.canary", "com.google.Chrome", bundleIDExtendsSelected},
		{"com.google.chrome", "com.google.Chrome.canary", bundleIDExtendedBySelected},
		{"com.google.Chrome", "com.google.Chrome", bundleIDSame},
		{"com.example.AppPro", "com.example.App", bundleIDUnrelated},
		{"com.example.App-beta", "com.example.App", bundleIDUnrelated},
		// A vendor prefix is never a build of the app, in either direction.
		{"com.google", "com.google.Chrome", bundleIDUnrelated},
		{"com.example.foo", "com.example", bundleIDUnrelated},
		{"org.mozilla.nightly", "org.mozilla.firefox", bundleIDUnrelated},
	}
	for _, tt := range cases {
		if got := relateBundleIDs(tt.candidate, tt.selected); got != tt.want {
			t.Errorf("relateBundleIDs(%q, %q) = %v, want %v", tt.candidate, tt.selected, got, tt.want)
		}
	}
}

func TestScanFindsFamilyBuildsAndNotLookAlikes(t *testing.T) {
	home := familyTestHome(t)
	apps := filepath.Join(home, "Applications")
	selected := filepath.Join(apps, "Firefox.app")
	writeTestInfoPlist(t, selected, "org.mozilla.firefox", "firefox")
	nightly := filepath.Join(apps, "Firefox Nightly.app")
	writeTestInfoPlist(t, nightly, "org.mozilla.nightly", "firefox")
	for name, id := range map[string]string{
		"Thunderbird.app":      "org.mozilla.thunderbird",
		"Firefoxy.app":         "com.example.firefoxy",
		"Firefox Helper.app":   "com.example.firefox-helper",
		"Microsoft Word.app":   "com.microsoft.Word",
		"Cleaner for Ff.app":   "com.example.cleaner",
		"FirefoxNightly.app":   "com.example.ffnightly",
		"Firefox Portable.app": "com.example.portable",
	} {
		writeTestInfoPlist(t, filepath.Join(apps, name), id, "x")
	}

	scan := scanUninstallBundleSiblings(context.Background(), AppEntry{Path: selected, Name: "Firefox", BundleID: "org.mozilla.firefox"})
	if !scan.Complete || len(scan.Paths) != 0 {
		t.Fatalf("scan = %#v, want a complete scan without same-ID copies", scan)
	}
	if !reflect.DeepEqual(scan.Family, []string{nightly}) || !scan.NameFamily {
		t.Fatalf("family = %#v (name family %v), want only Firefox Nightly", scan.Family, scan.NameFamily)
	}
	if !reflect.DeepEqual(scan.FamilyExecutables, []string{"firefox"}) {
		t.Fatalf("family executables = %#v", scan.FamilyExecutables)
	}
}

func TestUninstallStableAppKeepsFoldersAChannelBuildUses(t *testing.T) {
	home := familyTestHome(t)
	selected := filepath.Join(home, "Applications", "Firefox.app")
	writeTestInfoPlist(t, selected, "org.mozilla.firefox", "firefox")
	nightly := filepath.Join(home, "Applications", "Firefox Nightly.app")
	writeTestInfoPlist(t, nightly, "org.mozilla.nightly", "firefox")
	library := filepath.Join(home, "Library")
	profiles := filepath.Join(library, "Application Support", "Firefox", "profiles.ini")
	sharedCache := filepath.Join(library, "Caches", "Firefox")
	ownCache := filepath.Join(library, "Caches", "org.mozilla.firefox")
	ownPrefs := filepath.Join(library, "Preferences", "org.mozilla.firefox.plist")
	crash := filepath.Join(library, "Logs", "DiagnosticReports", "firefox-2026-11-01-101010.ips")
	for _, path := range []string{profiles, filepath.Join(sharedCache, "c"), filepath.Join(ownCache, "c"), ownPrefs, crash} {
		writeFamilyTestFile(t, path)
	}

	res := UninstallApp(context.Background(), &recordingRunner{outputs: map[string]CommandOutput{}},
		AppEntry{Path: selected, Name: "Firefox", BundleID: "org.mozilla.firefox"}, false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	for _, kept := range []string{profiles, sharedCache, crash, nightly} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("data another build uses was moved: %q: %v", kept, err)
		}
	}
	for _, moved := range []string{selected, ownCache, ownPrefs} {
		if _, err := os.Lstat(moved); !os.IsNotExist(err) {
			t.Fatalf("the app's own leftover %q stayed: %v", moved, err)
		}
	}
	// The shared profiles are browser profiles, which every uninstall keeps;
	// the shared cache stays for the other build.
	if !reflect.DeepEqual(res.FamilyBuilds, []string{nightly}) || !reflect.DeepEqual(res.FamilyKept, []string{sharedCache}) {
		t.Fatalf("family builds = %#v, kept = %#v", res.FamilyBuilds, res.FamilyKept)
	}
	if len(res.KeptData) != 1 || res.KeptData[0].Path != filepath.Dir(profiles) {
		t.Fatalf("KeptData = %#v, want the Firefox profiles", res.KeptData)
	}
	if res.SharedBundleSibling || res.SharedBundleUncertain {
		t.Fatalf("a family build must not switch to shared-bundle mode: %#v", res)
	}
}

func TestUninstallChannelBuildStillRemovesItsOwnNamedFolders(t *testing.T) {
	home := familyTestHome(t)
	writeTestInfoPlist(t, filepath.Join(home, "Applications", "Firefox.app"), "org.mozilla.firefox", "firefox")
	selected := filepath.Join(home, "Applications", "Firefox Nightly.app")
	writeTestInfoPlist(t, selected, "org.mozilla.nightly", "firefox")
	ownSupport := filepath.Join(home, "Library", "Application Support", "Firefox Nightly")
	shared := filepath.Join(home, "Library", "Application Support", "Firefox")
	writeFamilyTestFile(t, filepath.Join(ownSupport, "state"))
	writeFamilyTestFile(t, filepath.Join(shared, "profiles.ini"))

	related := findRelatedPaths(AppEntry{Path: selected, Name: "Firefox Nightly", BundleID: "org.mozilla.nightly"},
		leftoverFamilyFromScan(AppEntry{Name: "Firefox Nightly"}, uninstallSiblingScan{NameFamily: true}))
	if !containsString(related.paths, ownSupport) || containsString(related.paths, shared) {
		t.Fatalf("paths = %#v, want the channel's own folder and not the shared one", related.paths)
	}
}

func TestUninstallChromeKeepsCanaryLeftoversAndCanaryKeepsNothingOfChrome(t *testing.T) {
	home := familyTestHome(t)
	chrome := filepath.Join(home, "Applications", "Google Chrome.app")
	writeTestInfoPlist(t, chrome, "com.google.Chrome", "Google Chrome")
	canary := filepath.Join(home, "Applications", "Google Chrome Canary.app")
	writeTestInfoPlist(t, canary, "com.google.Chrome.canary", "Google Chrome Canary")
	library := filepath.Join(home, "Library")
	chromeCache := filepath.Join(library, "Caches", "com.google.Chrome")
	chromeHelper := filepath.Join(library, "Caches", "com.google.Chrome.helper")
	canaryCache := filepath.Join(library, "Caches", "com.google.Chrome.canary")
	canaryState := filepath.Join(library, "Saved Application State", "com.google.Chrome.canary.savedState")
	canaryByHost := filepath.Join(library, "Preferences", "ByHost", "com.google.Chrome.canary.0000-1111.plist")
	for _, path := range []string{filepath.Join(chromeCache, "c"), filepath.Join(chromeHelper, "c"), filepath.Join(canaryCache, "c"), filepath.Join(canaryState, "s"), canaryByHost} {
		writeFamilyTestFile(t, path)
	}

	chromeApp := AppEntry{Path: chrome, Name: "Google Chrome", BundleID: "com.google.Chrome"}
	scan := scanUninstallBundleSiblings(context.Background(), chromeApp)
	if !reflect.DeepEqual(scan.ExtendedIDs, []string{"com.google.Chrome.canary"}) {
		t.Fatalf("extended IDs = %#v", scan.ExtendedIDs)
	}
	related := findRelatedPaths(chromeApp, leftoverFamilyFromScan(chromeApp, scan))
	for _, path := range []string{canaryCache, canaryState, canaryByHost} {
		if containsString(related.paths, path) || !containsString(related.familyKept, path) {
			t.Fatalf("Chrome claimed Canary's %q: paths=%#v kept=%#v", path, related.paths, related.familyKept)
		}
	}
	for _, path := range []string{chromeCache, chromeHelper} {
		if !containsString(related.paths, path) {
			t.Fatalf("Chrome lost its own %q: %#v", path, related.paths)
		}
	}

	// The reverse relation keeps nothing: Canary's own rows still go.
	canaryApp := AppEntry{Path: canary, Name: "Google Chrome Canary", BundleID: "com.google.Chrome.canary"}
	reverse := scanUninstallBundleSiblings(context.Background(), canaryApp)
	if !reflect.DeepEqual(reverse.Family, []string{chrome}) || len(reverse.ExtendedIDs) != 0 {
		t.Fatalf("reverse scan = %#v", reverse)
	}
	canaryRelated := findRelatedPaths(canaryApp, leftoverFamilyFromScan(canaryApp, reverse))
	for _, path := range []string{canaryCache, canaryState, canaryByHost} {
		if !containsString(canaryRelated.paths, path) {
			t.Fatalf("Canary lost its own %q: %#v", path, canaryRelated.paths)
		}
	}
	if containsString(canaryRelated.paths, chromeCache) {
		t.Fatalf("Canary claimed Chrome's cache: %#v", canaryRelated.paths)
	}
}

func TestUninstallCaskRefusesZapWhileAnotherBuildIsInstalled(t *testing.T) {
	for _, tt := range []struct {
		name, selected, selectedID, other, otherID string
	}{
		{name: "stable with a channel build", selected: "foo.app", selectedID: "com.example.foo", other: "foo Nightly.app", otherID: "com.example.foo.nightly"},
		{name: "channel build with the stable app", selected: "foo.app", selectedID: "com.example.foo.nightly", other: "foo-stable.app", otherID: "com.example.foo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := familyTestHome(t)
			selected := filepath.Join(home, "Applications", tt.selected)
			writeTestInfoPlist(t, selected, tt.selectedID, "foo")
			other := filepath.Join(home, "Applications", tt.other)
			writeTestInfoPlist(t, other, tt.otherID, "foo")
			runner := &brewCaskUninstallRunner{appPath: selected, uninstallRemovePaths: []string{selected}}

			for _, dryRun := range []bool{true, false} {
				res := UninstallApp(context.Background(), runner, AppEntry{Path: selected, Name: "foo", BundleID: tt.selectedID}, dryRun)
				if res.Err == nil || !strings.Contains(res.Err.Error(), "another installed build of this app") || !strings.Contains(res.Err.Error(), other) {
					t.Fatalf("dryRun=%v error = %v, want a family zap refusal naming %q", dryRun, res.Err, other)
				}
			}
			if runnerCallContains(runner.calls, "brew uninstall") {
				t.Fatalf("brew zap ran while another build is installed: %#v", runner.calls)
			}
		})
	}
}

func TestUninstallStopsWhenAFamilyBuildAppearsBeforeTheAction(t *testing.T) {
	home := familyTestHome(t)
	selected := filepath.Join(home, "Applications", "Zed.app")
	writeTestInfoPlist(t, selected, "dev.zed.Zed", "zed")
	shared := filepath.Join(home, "Library", "Application Support", "Zed")
	writeFamilyTestFile(t, filepath.Join(shared, "db"))
	preview := filepath.Join(home, "Applications", "Zed Preview.app")
	// The planning scan has run when Bloom first looks for brew.
	runner := &siblingCreatingRunner{onBrewLookup: func() {
		writeTestInfoPlist(t, preview, "dev.zed.Zed-Preview", "zed")
	}}

	res := UninstallApp(context.Background(), runner, AppEntry{Path: selected, Name: "Zed", BundleID: "dev.zed.Zed"}, false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "installed app copies changed") {
		t.Fatalf("uninstall error = %v, want a stop for the new build", res.Err)
	}
	if !containsString(res.FamilyBuilds, preview) {
		t.Fatalf("the new build was not reported: %#v", res.FamilyBuilds)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("side effects ran after a build appeared: %#v", runner.calls)
	}
	for _, path := range []string{selected, shared} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%q changed: %v", path, err)
		}
	}
}

func TestStopAppIgnoresAnotherBuildRunningUnderTheSameExecutableName(t *testing.T) {
	home := familyTestHome(t)
	appPath := filepath.Join(home, "Applications", "Firefox.app")
	writeTestInfoPlist(t, appPath, "org.mozilla.firefox", "firefox")
	nightlyExecutable := filepath.Join(home, "Applications", "Firefox Nightly.app", "Contents", "MacOS", "firefox")
	oldPoll := stopAppPollInterval
	stopAppPollInterval = 0
	t.Cleanup(func() { stopAppPollInterval = oldPoll })
	r := &processRunner{
		pids:         map[string][]string{"firefox": {"4242"}},
		processPaths: map[string]string{"4242": nightlyExecutable},
	}

	if stopApp(context.Background(), r, AppEntry{Path: appPath, Name: "Firefox", BundleID: "org.mozilla.firefox"}, []string{"firefox"}) {
		t.Fatal("Firefox Nightly's process kept the stable app counted as running")
	}
	for _, call := range r.calls {
		if strings.Contains(call, "osascript") || strings.Contains(call, "/bin/kill") {
			t.Fatalf("stopApp acted on another build's process: %#v", r.calls)
		}
	}
}

func TestScanStaysIncompleteWhenOneOfACandidatesPlistsCannotBeRead(t *testing.T) {
	home := familyTestHome(t)
	selected := filepath.Join(home, "Applications", "Foo.app")
	writeTestInfoPlist(t, selected, "com.example.foo", "foo")
	// A readable plist with an unrelated ID must not vouch for an unreadable
	// wrapped one, which could name the selected ID.
	wrapper := filepath.Join(home, "Applications", "Wrapped.app")
	writeTestInfoPlist(t, wrapper, "com.example.other", "other")
	broken := filepath.Join(wrapper, "Wrapper", "Inner.app", "Info.plist")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte(`<?xml version="1.0"?><plist><dict><key>CFBundleIdentifier</key><string>com.example.foo`), 0o644); err != nil {
		t.Fatal(err)
	}

	scan := scanUninstallBundleSiblings(context.Background(), AppEntry{Path: selected, Name: "Foo", BundleID: "com.example.foo"})
	if scan.Complete {
		t.Fatalf("scan = %#v, want it incomplete while a candidate plist is unreadable", scan)
	}

	// A plist naming the selected ID decides the candidate on its own.
	writeTestInfoPlist(t, wrapper, "com.example.foo", "foo")
	decided := scanUninstallBundleSiblings(context.Background(), AppEntry{Path: selected, Name: "Foo", BundleID: "com.example.foo"})
	if !decided.Complete || !reflect.DeepEqual(decided.Paths, []string{wrapper}) {
		t.Fatalf("scan = %#v, want the same-ID copy found despite the unreadable plist", decided)
	}
}

func TestScanIgnoresAppsThatOnlyShareATwoComponentID(t *testing.T) {
	home := familyTestHome(t)
	selected := filepath.Join(home, "Applications", "Ovito.app")
	writeTestInfoPlist(t, selected, "org.ovito", "Ovito")
	writeTestInfoPlist(t, filepath.Join(home, "Applications", "Lab Tool.app"), "org.ovito.labtool", "labtool")

	scan := scanUninstallBundleSiblings(context.Background(), AppEntry{Path: selected, Name: "Ovito", BundleID: "org.ovito"})
	if !scan.Complete || len(scan.Family) != 0 || len(scan.ExtendedIDs) != 0 {
		t.Fatalf("scan = %#v, want no family build for a two-component ID", scan)
	}
}
