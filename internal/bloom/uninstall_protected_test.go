package bloom

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// steamTestHome lays out a Steam install: the app, its bundle-ID cache, and
// the library folder under Application Support with a game and a save.
func steamTestHome(t *testing.T, libraryName string) (home, appPath, library, cache string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	useUninstallSiblingRoots(t, "~/Applications")

	appPath = filepath.Join(home, "Applications", "Steam.app")
	writeTestInfoPlist(t, appPath, "com.valvesoftware.steam", "steam_osx")
	library = filepath.Join(home, "Library", "Application Support", libraryName)
	cache = filepath.Join(home, "Library", "Caches", "com.valvesoftware.steam")
	for _, path := range []string{
		filepath.Join(library, "steamapps", "common", "Game", "game.bin"),
		filepath.Join(library, "userdata", "1", "save.dat"),
		filepath.Join(cache, "cache.bin"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home, appPath, library, cache
}

func TestFindRelatedPathsKeepsTheSteamLibrary(t *testing.T) {
	for _, libraryName := range []string{"Steam", "steam"} {
		t.Run(libraryName, func(t *testing.T) {
			_, appPath, library, cache := steamTestHome(t, libraryName)

			related := findRelatedPaths(AppEntry{Path: appPath, Name: "Steam", BundleID: "com.valvesoftware.steam"}, leftoverFamily{})
			paths, kept := related.paths, related.keptData
			for _, p := range paths {
				if _, ok := protectedUninstallData(p); ok || strings.EqualFold(p, library) {
					t.Fatalf("leftovers include the Steam library: %#v", paths)
				}
			}
			if !containsString(paths, cache) {
				t.Fatalf("leftovers lost Steam's own cache %q: %#v", cache, paths)
			}
			if len(kept) != 1 || !strings.EqualFold(kept[0].Path, library) {
				t.Fatalf("kept = %#v, want the Steam library %q", kept, library)
			}
		})
	}
}

func TestUninstallAppKeepsTheSteamLibrary(t *testing.T) {
	_, appPath, library, cache := steamTestHome(t, "Steam")

	res := UninstallApp(context.Background(), &recordingRunner{outputs: map[string]CommandOutput{}},
		AppEntry{Path: appPath, Name: "Steam", BundleID: "com.valvesoftware.steam"}, false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	if _, err := os.Lstat(appPath); !os.IsNotExist(err) {
		t.Fatalf("Steam.app was not moved: %v", err)
	}
	if _, err := os.Lstat(cache); !os.IsNotExist(err) {
		t.Fatalf("Steam's cache was not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(library, "steamapps", "common", "Game", "game.bin")); err != nil {
		t.Fatalf("the Steam library was touched: %v", err)
	}
	if len(res.KeptData) != 1 || res.KeptData[0].Path != library {
		t.Fatalf("KeptData = %#v, want the Steam library", res.KeptData)
	}
}

// The steam cask's zap trashes the library, so Homebrew's uninstall runs
// without --zap and the library stays; Bloom moves the rest.
func TestUninstallSteamCaskLeavesOutZapThatReachesTheLibrary(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "dry run", false: "uninstall"}[dryRun], func(t *testing.T) {
			_, appPath, library, cache := steamTestHome(t, "Steam")
			runner := &namedCaskRunner{token: "steam", appPath: appPath, uninstallRemovePaths: []string{appPath}, zapRemovePaths: []string{library}}

			res := UninstallApp(context.Background(), runner, AppEntry{Path: appPath, Name: "Steam", BundleID: "com.valvesoftware.steam"}, dryRun)
			if res.Err != nil {
				t.Fatalf("uninstall error = %v", res.Err)
			}
			if !res.ZapSkipped || brewCaskUninstallCommand(res) != "brew uninstall --cask --force steam" {
				t.Fatalf("zap left out = %v, command = %q", res.ZapSkipped, brewCaskUninstallCommand(res))
			}
			if runnerCallContains(runner.calls, "--zap") {
				t.Fatalf("brew zap ran against the Steam library: %#v", runner.calls)
			}
			if _, err := os.Stat(filepath.Join(library, "steamapps", "common", "Game", "game.bin")); err != nil {
				t.Fatalf("the Steam library was touched: %v", err)
			}
			if len(res.KeptData) != 1 || res.KeptData[0].Path != library {
				t.Fatalf("KeptData = %#v, want the Steam library", res.KeptData)
			}
			if dryRun {
				if runnerCallContains(runner.calls, "brew uninstall") {
					t.Fatalf("a dry run ran brew uninstall: %#v", runner.calls)
				}
				return
			}
			if !runnerCallContains(runner.calls, "brew uninstall --cask --force steam") {
				t.Fatalf("brew uninstall did not run: %#v", runner.calls)
			}
			for _, path := range []string{appPath, cache} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("%q stayed: %v", path, err)
				}
			}
		})
	}
}

func TestUninstallTrashSinkRefusesTheSteamLibrary(t *testing.T) {
	_, _, library, _ := steamTestHome(t, "Steam")

	for _, target := range []string{library, filepath.Join(library, "steamapps")} {
		if err := movePathToTrashStubborn(context.Background(), &recordingRunner{outputs: map[string]CommandOutput{}}, target); err == nil {
			t.Fatalf("the uninstall Trash sink moved %q", target)
		}
		if _, err := os.Lstat(target); err != nil {
			t.Fatalf("%q changed: %v", target, err)
		}
	}
}

// namedCaskRunner answers Homebrew's cask queries for one installed cask
// token, so a test can use the real token of the app it models.
type namedCaskRunner struct {
	token   string
	appPath string
	// uninstallRemovePaths go on any brew uninstall, zapRemovePaths only
	// with --zap.
	uninstallRemovePaths []string
	zapRemovePaths       []string
	uninstalled          bool
	calls                []string
}

func (r *namedCaskRunner) LookPath(file string) (string, error) {
	if file == "brew" {
		return "/bin/brew", nil
	}
	return "", errNotFound
}

func (r *namedCaskRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	switch call {
	case "brew list --cask":
		if r.uninstalled {
			return CommandOutput{}
		}
		return CommandOutput{Stdout: r.token + "\n"}
	case "brew info --cask " + r.token:
		return CommandOutput{Stdout: r.appPath + "\n"}
	case "brew info --json=v2 --cask " + r.token:
		return caskInfoJSON(r.token, "")
	case "brew uninstall --cask --force --zap " + r.token:
		for _, path := range append(append([]string{}, r.uninstallRemovePaths...), r.zapRemovePaths...) {
			_ = os.RemoveAll(path)
		}
		r.uninstalled = true
		return CommandOutput{}
	case "brew uninstall --cask --force " + r.token:
		for _, path := range r.uninstallRemovePaths {
			_ = os.RemoveAll(path)
		}
		r.uninstalled = true
		return CommandOutput{}
	default:
		return CommandOutput{Err: errNotFound}
	}
}

// Uninstall keeps personal data the app's leftover rules match: models,
// virtual machines, Windows bottles, game libraries, browser profiles, and
// the stores that live under Caches. A leftover that holds such a folder
// stays whole.
func TestFindRelatedPathsKeepsPersonalData(t *testing.T) {
	cases := []struct {
		name, appName, bundleID, data, want, reason string
	}{
		{"Buzz models", "Buzz", "com.chidiwilliams.buzz", "Library/Caches/Buzz/models/whisper/base.pt", "Library/Caches/Buzz", "Buzz's Whisper models and plugins"},
		{"UTM virtual machines", "UTM", "com.utmapp.UTM", "Library/Containers/com.utmapp.UTM/Data/Documents/Linux.utm/config.plist", "Library/Containers/com.utmapp.UTM", "UTM's virtual machines"},
		{"CrossOver bottles", "CrossOver", "com.codeweavers.CrossOver", "Library/Application Support/CrossOver/Bottles/Steam/drive_c/x", "Library/Application Support/CrossOver", "CrossOver bottles with installed Windows apps"},
		{"OpenEmu library", "OpenEmu", "org.openemu.OpenEmu", "Library/Application Support/OpenEmu/Game Library/roms/game.nes", "Library/Application Support/OpenEmu", "game libraries and saves"},
		{"Firefox profiles", "Firefox", "org.mozilla.firefox", "Library/Application Support/Firefox/Profiles/abc.default/key4.db", "Library/Application Support/Firefox", "browser profiles with passwords, bookmarks, and history"},
		{"Spotify offline music", "Spotify", "com.spotify.client", "Library/Caches/com.spotify.client/Storage/track.file", "Library/Caches/com.spotify.client", "Spotify's offline music"},
		{"a folder holding Chrome's profiles", "Google", "com.example.google", "Library/Application Support/Google/Chrome/Default/Login Data", "Library/Application Support/Google", "holding browser profiles with passwords, bookmarks, and history"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			appPath := filepath.Join(home, "Applications", tt.appName+".app")
			writeTestInfoPlist(t, appPath, tt.bundleID, tt.appName)
			data := filepath.Join(home, tt.data)
			if err := os.MkdirAll(filepath.Dir(data), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(data, []byte("data"), 0o644); err != nil {
				t.Fatal(err)
			}

			related := findRelatedPaths(AppEntry{Path: appPath, Name: tt.appName, BundleID: tt.bundleID}, leftoverFamily{})
			want := filepath.Join(home, tt.want)
			for _, p := range related.paths {
				if pathAtOrBelowFold(p, want) || pathAtOrBelowFold(want, p) {
					t.Fatalf("leftovers include personal data %q: %#v", p, related.paths)
				}
			}
			if len(related.keptData) != 1 || !strings.EqualFold(related.keptData[0].Path, want) || related.keptData[0].Reason != tt.reason {
				t.Fatalf("kept = %#v, want %q (%s)", related.keptData, want, tt.reason)
			}
			if err := movePathToTrashStubborn(context.Background(), &recordingRunner{outputs: map[string]CommandOutput{}}, want); err == nil {
				t.Fatalf("the uninstall Trash sink moved %q", want)
			}
			if _, err := os.Lstat(data); err != nil {
				t.Fatalf("%q changed: %v", data, err)
			}
		})
	}
}
