package bloom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scriptedCaskRunner models one installed cask whose `brew uninstall` returns
// uninstallOut, after which `brew list --cask` returns listAfter.
type scriptedCaskRunner struct {
	token             string
	appPath           string
	uninstallOut      CommandOutput
	removeOnUninstall []string
	listAfter         CommandOutput
	ranUninstall      bool
	calls             []string
}

func (r *scriptedCaskRunner) LookPath(file string) (string, error) {
	if file == "brew" {
		return "/bin/brew", nil
	}
	return "", errNotFound
}

func (r *scriptedCaskRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	switch call {
	case "brew list --cask":
		if r.ranUninstall {
			return r.listAfter
		}
		return CommandOutput{Stdout: r.token + "\n"}
	case "brew info --cask " + r.token:
		return CommandOutput{Stdout: r.appPath + "\n"}
	case "brew uninstall --cask --force --zap " + r.token:
		r.ranUninstall = true
		for _, path := range r.removeOnUninstall {
			_ = os.RemoveAll(path)
		}
		return r.uninstallOut
	default:
		return CommandOutput{Err: errNotFound}
	}
}

func caskUninstallTestApp(t *testing.T) (appPath, cache string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	useUninstallSiblingRoots(t, "~/Applications")
	appPath = filepath.Join(home, "Applications", "foo.app")
	writeTestInfoPlist(t, appPath, "com.example.foo", "foo")
	cache = filepath.Join(home, "Library", "Caches", "com.example.foo")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	return appPath, cache
}

var xcodeLicenseFailure = CommandOutput{
	Err:    errors.New("exit status 1"),
	Stderr: "Warning: some formulae are outdated\nError: You have not agreed to the Xcode license. Please resolve this by running:\n  sudo xcodebuild -license accept\n",
}

func TestUninstallAppStopsWhenHomebrewKeepsTheCask(t *testing.T) {
	cases := []struct {
		name      string
		uninstall CommandOutput
		listAfter CommandOutput
		want      string
	}{
		{
			name:      "brew cannot run",
			uninstall: xcodeLicenseFailure,
			listAfter: xcodeLicenseFailure,
			want:      "brew uninstall --cask foo failed, so Bloom moved nothing: You have not agreed to the Xcode license. Please resolve this by running:",
		},
		{
			name:      "brew fails and still lists the cask",
			uninstall: CommandOutput{Err: errors.New("exit status 1"), Stderr: "Error: Another active Homebrew process is already in progress.\n"},
			listAfter: CommandOutput{Stdout: "foo\n"},
			want:      "brew uninstall --cask foo failed, so Bloom moved nothing: Another active Homebrew process is already in progress.",
		},
		{
			name:      "brew succeeds but still lists the cask",
			listAfter: CommandOutput{Stdout: "bar\nfoo\n"},
			want:      "Homebrew still lists cask foo after brew uninstall, so Bloom moved nothing",
		},
		{
			name:      "the cask list cannot be read",
			listAfter: CommandOutput{Err: errors.New("exit status 1"), Stderr: "Error: Permission denied @ dir_s_mkdir\n"},
			want:      "could not confirm that Homebrew removed cask foo, so Bloom moved nothing: Permission denied @ dir_s_mkdir",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			appPath, cache := caskUninstallTestApp(t)
			runner := &scriptedCaskRunner{token: "foo", appPath: appPath, uninstallOut: tt.uninstall, listAfter: tt.listAfter}

			res := UninstallApp(context.Background(), runner, AppEntry{Path: appPath, Name: "foo", BundleID: "com.example.foo"}, false)
			if res.Err == nil || res.Err.Error() != tt.want {
				t.Fatalf("uninstall error = %v, want %q", res.Err, tt.want)
			}
			if res.BrewRemoved || res.AppRemoved || len(res.Moved) != 0 {
				t.Fatalf("a failed brew uninstall still counted as removal: %#v", res)
			}
			for _, path := range []string{appPath, cache} {
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("Bloom moved %q after brew failed: %v", path, err)
				}
			}
		})
	}
}

func TestUninstallAppContinuesWhenHomebrewDroppedTheCaskDespiteAnError(t *testing.T) {
	appPath, cache := caskUninstallTestApp(t)
	runner := &scriptedCaskRunner{
		token:             "foo",
		appPath:           appPath,
		uninstallOut:      CommandOutput{Err: errors.New("exit status 1"), Stderr: "Error: It seems there is already an App at a zap path.\n"},
		removeOnUninstall: []string{appPath},
		listAfter:         CommandOutput{Stdout: "bar\n"},
	}

	res := UninstallApp(context.Background(), runner, AppEntry{Path: appPath, Name: "foo", BundleID: "com.example.foo"}, false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v, want the uninstall to finish once Homebrew dropped the cask", res.Err)
	}
	if !res.BrewRemoved || !res.AppRemoved {
		t.Fatalf("result = %#v, want the cask and app counted as removed", res)
	}
	if _, err := os.Lstat(cache); !os.IsNotExist(err) {
		t.Fatalf("leftover cache was not moved: %v", err)
	}
}

func TestBrewErrorLinePrefersHomebrewsErrorLine(t *testing.T) {
	cases := map[string]struct {
		out  CommandOutput
		want string
	}{
		"error after a warning": {out: xcodeLicenseFailure, want: "You have not agreed to the Xcode license. Please resolve this by running:"},
		"plain output":          {out: CommandOutput{Err: errors.New("exit status 1"), Stdout: "\nsomething broke\n"}, want: "something broke"},
		"silent failure":        {out: CommandOutput{Err: errors.New("exit status 7")}, want: "exit status 7"},
	}
	for name, tt := range cases {
		if got := brewErrorLine(tt.out); got != tt.want {
			t.Errorf("%s: brewErrorLine = %q, want %q", name, got, tt.want)
		}
	}
}
