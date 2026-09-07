package bloom

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestParseAppleLanguagesNormalizesAndRejectsUnsafeTags(t *testing.T) {
	got := parseAppleLanguages(`(
    "zh-hans-cn",
    "en_gb",
    "EN-GB",
    "../private"
)`)
	want := []string{"zh-Hans-CN", "en-GB"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("languages = %#v, want %#v", got, want)
	}
}

func TestLocalizationDirectoryCandidatesCoverCommonBundleSpellings(t *testing.T) {
	got := localizationDirectoryCandidates("zh-Hans-CN")
	want := []string{"zh-Hans-CN", "zh_Hans_CN", "zh-Hans", "zh_Hans", "zh_CN", "zh-CN", "zh", "Chinese"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
}

func TestAppBundleBaseNameHandlesCaseVariantSuffixes(t *testing.T) {
	for _, path := range []string{
		"/Applications/Example.app",
		"/Applications/Example.APP",
		"/Applications/Example.ApP",
	} {
		if got := appBundleBaseName(path); got != "Example" {
			t.Errorf("appBundleBaseName(%q) = %q, want Example", path, got)
		}
	}
	if got := (AppEntry{Path: "/Applications/Example.APP"}).displayName(); got != "Example" {
		t.Fatalf("display fallback = %q, want Example", got)
	}
}

func TestReadAppDisplayNameUsesPreferredLocalization(t *testing.T) {
	appPath := filepath.Join(t.TempDir(), "VideoFusion-macOS.app")
	writeDisplayTestInfoPlist(t, appPath, "VideoFusion-macOS", "CapCut", "en")
	writeDisplayTestStrings(t, filepath.Join(appPath, "Contents", "Resources", "zh-Hans.lproj"), "剪映专业版", "")

	if got := readAppDisplayName(context.Background(), appPath, []string{"zh-Hans", "en"}); got != "剪映专业版" {
		t.Fatalf("localized display name = %q, want 剪映专业版", got)
	}
	if got := readAppDisplayName(context.Background(), appPath, []string{"en-GB"}); got != "VideoFusion-macOS" {
		t.Fatalf("unlocalized fallback = %q, want VideoFusion-macOS", got)
	}
}

func TestReadAppDisplayNameUsesBaseForDevelopmentLanguage(t *testing.T) {
	appPath := filepath.Join(t.TempDir(), "BaseTool.app")
	writeDisplayTestInfoPlist(t, appPath, "Base Tool", "BaseTool", "English")
	writeDisplayTestStrings(t, filepath.Join(appPath, "Contents", "Resources", "Base.lproj"), "Localized Base Tool", "")

	if got := readAppDisplayName(context.Background(), appPath, []string{"en-GB"}); got != "Localized Base Tool" {
		t.Fatalf("Base localization = %q, want Localized Base Tool", got)
	}
}

func TestReadAppDisplayNameSupportsBinaryPlistsOnMacOS(t *testing.T) {
	if _, err := os.Stat("/usr/bin/plutil"); err != nil {
		t.Skip("plutil is only available on macOS")
	}
	appPath := filepath.Join(t.TempDir(), "BinaryTool.app")
	writeDisplayTestInfoPlist(t, appPath, "Binary Tool", "BinaryTool", "en")
	plist := filepath.Join(appPath, "Contents", "Info.plist")
	if out, err := exec.Command("/usr/bin/plutil", "-convert", "binary1", "--", plist).CombinedOutput(); err != nil {
		t.Fatalf("convert test plist to binary: %v: %s", err, out)
	}

	if got := readAppDisplayName(context.Background(), appPath, nil); got != "Binary Tool" {
		t.Fatalf("binary plist display name = %q, want Binary Tool", got)
	}
}

func TestReadAppDisplayNameDoesNotFallThroughToUnrequestedLocalization(t *testing.T) {
	appPath := filepath.Join(t.TempDir(), "MiaoYan.app")
	writeDisplayTestInfoPlist(t, appPath, "MiaoYan", "MiaoYan", "en")
	if err := os.MkdirAll(filepath.Join(appPath, "Contents", "Resources", "en.lproj"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDisplayTestStrings(t, filepath.Join(appPath, "Contents", "Resources", "zh-Hans.lproj"), "妙言", "")

	if got := readAppDisplayName(context.Background(), appPath, []string{"en", "zh-Hans"}); got != "MiaoYan" {
		t.Fatalf("display name = %q, want unlocalized MiaoYan", got)
	}
}

func TestPrintAppListUsesSanitizedDisplayNameAndWidth(t *testing.T) {
	var out bytes.Buffer
	app := AppEntry{
		Path:          "/Applications/VideoFusion-macOS.app",
		Name:          "VideoFusion-macOS",
		DisplayName:   "  剪映\t专业版\n",
		BundleID:      "com.lemon.lvpro",
		SizeKB:        42,
		LastUsedEpoch: 123,
	}

	PrintAppList(&out, []AppEntry{app})
	fields := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\t")
	if len(fields) != 6 {
		t.Fatalf("TSV fields = %#v, want 6", fields)
	}
	if fields[1] != "剪映 专业版" {
		t.Fatalf("listed name = %q, want sanitized localized name", fields[1])
	}
	if fields[5] != strconv.Itoa(DisplayWidth(fields[1])) {
		t.Fatalf("listed width = %q, want %d", fields[5], DisplayWidth(fields[1]))
	}
}

func TestLocalizedDisplayNameNeverDrivesLeftoverMatching(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := filepath.Join(home, "Applications", "Canonical Tool.app")
	canonicalCache := filepath.Join(home, "Library", "Caches", "Canonical Tool")
	localizedCache := filepath.Join(home, "Library", "Caches", "Localized Tool")
	for _, path := range []string{appPath, canonicalCache, localizedCache} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	paths := FindRelatedPaths(AppEntry{
		Path:        appPath,
		Name:        "Canonical Tool",
		DisplayName: "Localized Tool",
	})
	if !containsString(paths, canonicalCache) {
		t.Fatalf("canonical leftover missing: %#v", paths)
	}
	if containsString(paths, localizedCache) {
		t.Fatalf("localized UI label was used for leftover matching: %#v", paths)
	}
}

func TestUninstallSummaryUsesLocalizedDisplayName(t *testing.T) {
	summary := BatchSummary{Results: []UninstallResult{{
		App: AppEntry{Name: "VideoFusion-macOS", DisplayName: "剪映专业版"},
	}}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := App{Out: &stdout, Err: &stderr}

	processed, failures := app.printUninstallSummary(summary, false, false)
	if processed != 1 || failures != 0 {
		t.Fatalf("processed, failures = %d, %d; want 1, 0", processed, failures)
	}
	if !strings.Contains(stdout.String(), "✓ 剪映专业版") || strings.Contains(stdout.String(), "VideoFusion-macOS") {
		t.Fatalf("summary did not use localized label: %q", stdout.String())
	}
}

func writeDisplayTestInfoPlist(t *testing.T, appPath, displayName, bundleName, developmentRegion string) {
	t.Helper()
	contents := filepath.Join(appPath, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "Resources"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.display</string>
<key>CFBundleDisplayName</key><string>%s</string>
<key>CFBundleName</key><string>%s</string>
<key>CFBundleDevelopmentRegion</key><string>%s</string>
</dict></plist>`, displayName, bundleName, developmentRegion)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDisplayTestStrings(t *testing.T, lproj, displayName, bundleName string) {
	t.Helper()
	if err := os.MkdirAll(lproj, 0o755); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleDisplayName</key><string>%s</string>
<key>CFBundleName</key><string>%s</string>
</dict></plist>`, displayName, bundleName)
	if err := os.WriteFile(filepath.Join(lproj, "InfoPlist.strings"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
