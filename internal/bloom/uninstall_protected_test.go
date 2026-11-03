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

func TestUninstallSteamCaskRefusesZapThatReachesTheLibrary(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "dry run", false: "uninstall"}[dryRun], func(t *testing.T) {
			_, appPath, library, _ := steamTestHome(t, "Steam")
			runner := &namedCaskRunner{token: "steam", appPath: appPath, zapRemovePaths: []string{appPath, library}}

			res := UninstallApp(context.Background(), runner, AppEntry{Path: appPath, Name: "Steam", BundleID: "com.valvesoftware.steam"}, dryRun)
			if res.Err == nil || !strings.Contains(res.Err.Error(), "refusing Homebrew --zap for steam") || !strings.Contains(res.Err.Error(), library) {
				t.Fatalf("uninstall error = %v, want a zap refusal naming the library", res.Err)
			}
			if runnerCallContains(runner.calls, "brew uninstall") {
				t.Fatalf("brew zap ran against the Steam library: %#v", runner.calls)
			}
			for _, path := range []string{appPath, library} {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("refused uninstall changed %q: %v", path, err)
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
	token          string
	appPath        string
	zapRemovePaths []string
	uninstalled    bool
	calls          []string
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
	case "brew uninstall --cask --force --zap " + r.token:
		for _, path := range r.zapRemovePaths {
			_ = os.RemoveAll(path)
		}
		r.uninstalled = true
		return CommandOutput{}
	default:
		return CommandOutput{Err: errNotFound}
	}
}
