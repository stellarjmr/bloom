package bloom

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// caskInfoJSON is brew info --json=v2 --cask output for token whose
// artifacts end with one zap stanza; an empty zap means no zap artifact.
func caskInfoJSON(token, zap string) CommandOutput {
	artifacts := `[{"app":["` + token + `.app"]}]`
	if zap != "" {
		artifacts = `[{"app":["` + token + `.app"]},{"zap":[` + zap + `]}]`
	}
	return caskInfoCaskJSON(token, `"artifacts":`+artifacts)
}

// caskInfoCaskJSON is brew info --json=v2 --cask output for token whose
// cask object holds fields, written as JSON object members.
func caskInfoCaskJSON(token, fields string) CommandOutput {
	return CommandOutput{Stdout: `{"formulae":[],"casks":[{"token":"` + token + `","full_token":"` + token + `",` + fields + `}]}`}
}

// zapStanzaRunner models one installed cask whose brew info --json=v2
// answers come from infos in turn, the last one repeating.
type zapStanzaRunner struct {
	token     string
	appPath   string
	infos     []CommandOutput
	infoCalls int
	removed   bool
	calls     []string
	// onQuitCheck runs when stopApp looks for the app's processes.
	onQuitCheck func()
}

func (r *zapStanzaRunner) LookPath(file string) (string, error) {
	if file == "brew" {
		return "/bin/brew", nil
	}
	return "", errNotFound
}

func (r *zapStanzaRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	if name == "/usr/bin/pgrep" && r.onQuitCheck != nil {
		r.onQuitCheck()
		r.onQuitCheck = nil
	}
	switch call {
	case "brew list --cask":
		if r.removed {
			return CommandOutput{}
		}
		return CommandOutput{Stdout: r.token + "\n"}
	case "brew info --cask " + r.token:
		return CommandOutput{Stdout: r.appPath + "\n"}
	case "brew info --json=v2 --cask " + r.token:
		i := r.infoCalls
		if i >= len(r.infos) {
			i = len(r.infos) - 1
		}
		r.infoCalls++
		return r.infos[i]
	case "brew uninstall --cask --force --zap " + r.token, "brew uninstall --cask --force " + r.token:
		r.removed = true
		return CommandOutput{}
	default:
		return CommandOutput{Err: errNotFound}
	}
}

func useTestCaskroom(t *testing.T) string {
	t.Helper()
	room := filepath.Join(t.TempDir(), "Caskroom")
	previous := caskroomDirs
	caskroomDirs = []string{room}
	t.Cleanup(func() { caskroomDirs = previous })
	return room
}

func writeCaskReceipt(t *testing.T, room, token, body string) {
	t.Helper()
	dir := filepath.Join(room, token, ".metadata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "INSTALL_RECEIPT.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZapTestFiles(t *testing.T, home string, rels []string) {
	t.Helper()
	for _, rel := range rels {
		path := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("user data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// linkSupportFolderIntoDocuments makes ~/Library/Application Support/FooData
// a link to ~/Documents/FooData.
func linkSupportFolderIntoDocuments(t *testing.T, home string) {
	t.Helper()
	support := filepath.Join(home, "Library", "Application Support")
	target := filepath.Join(home, "Documents", "FooData")
	for _, dir := range []string{support, target} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, filepath.Join(support, "FooData")); err != nil {
		t.Fatal(err)
	}
}

func zapTestApp(appPath string) AppEntry {
	return AppEntry{Path: appPath, Name: "foo", BundleID: "com.example.foo"}
}

// requireZapLeftOut checks that the uninstall went ahead without --zap and
// kept want, a "path (reason)" fragment: a dry run calls no brew uninstall,
// a real one calls it without --zap.
func requireZapLeftOut(t *testing.T, res UninstallResult, runner *zapStanzaRunner, dryRun bool, want string) {
	t.Helper()
	if res.Err != nil {
		t.Fatalf("dryRun=%v: uninstall error = %v, want the uninstall to go ahead without --zap", dryRun, res.Err)
	}
	if !res.ZapSkipped || !keptListContains(res.ZapKept, want) {
		t.Fatalf("dryRun=%v: zap left out = %v, kept = %#v, want %q kept", dryRun, res.ZapSkipped, res.ZapKept, want)
	}
	if runnerCallContains(runner.calls, "--zap") {
		t.Fatalf("dryRun=%v: --zap ran: %v", dryRun, runner.calls)
	}
	if ran := runnerCallContains(runner.calls, "brew uninstall --cask --force "+runner.token); ran == dryRun {
		t.Fatalf("dryRun=%v: brew uninstall ran = %v: %v", dryRun, ran, runner.calls)
	}
	if got := brewCaskUninstallCommand(res); got != "brew uninstall --cask --force "+runner.token {
		t.Fatalf("dryRun=%v: command = %q", dryRun, got)
	}
}

func keptListContains(items []UninstallKeptPath, want string) bool {
	for _, item := range items {
		if strings.Contains(item.Path+" ("+item.Reason+")", want) {
			return true
		}
	}
	return false
}

// requireZapNotRun checks a refusal: no brew uninstall ran and the app
// bundle stayed.
func requireZapNotRun(t *testing.T, runner *zapStanzaRunner, appPath string) {
	t.Helper()
	if runnerCallContains(runner.calls, "brew uninstall") {
		t.Fatalf("brew uninstall ran: %v", runner.calls)
	}
	if _, err := os.Lstat(appPath); err != nil {
		t.Fatalf("app bundle moved: %v", err)
	}
}

func TestUninstallAppLeavesOutZapThatReachesTheUsersFiles(t *testing.T) {
	cases := []struct {
		name  string
		zap   string
		files []string
		setup func(t *testing.T, home string)
		want  string
	}{
		{
			name:  "Ableton's Music library",
			zap:   `{"trash":["~/Library/Caches/Ableton","~/Music/Ableton"]}`,
			files: []string{"Music/Ableton/User Library/Presets/lead.adv"},
			want:  "Music/Ableton (in your Music folder)",
		},
		{
			name:  "a glob in Documents",
			zap:   `{"trash":"~/Documents/Max [0-9]"}`,
			files: []string{"Documents/Max 9/Library/patch.maxpat"},
			want:  "Documents/Max 9 (in your Documents folder)",
		},
		{
			name:  "a delete in iCloud Drive",
			zap:   `{"delete":["~/Library/Mobile Documents/iCloud~com~example~foo"]}`,
			files: []string{"Library/Mobile Documents/iCloud~com~example~foo/Documents/board.foo"},
			want:  "iCloud~com~example~foo (in iCloud Drive)",
		},
		{
			name:  "a folder holding cloud storage",
			zap:   `{"trash":"~/Library/CloudStorage"}`,
			files: []string{"Library/CloudStorage/Dropbox/notes.txt"},
			want:  "Library/CloudStorage (in a cloud storage folder)",
		},
		{
			name:  "a glob that expands to a user folder",
			zap:   `{"trash":"~/Mov*"}`,
			files: []string{"Movies/clip.mov"},
			want:  "Movies (in your Movies folder)",
		},
		{
			name:  "braces",
			zap:   `{"trash":"~/Documents/{Foo,Bar}"}`,
			files: []string{"Documents/Bar/notes.txt"},
			want:  "Documents/Bar (in your Documents folder)",
		},
		{
			name:  "braces under a parent folder linked into Documents",
			zap:   `{"trash":"~/Library/Application Support/FooData/{projects,notes}"}`,
			files: []string{"Documents/FooData/notes/today.md"},
			setup: linkSupportFolderIntoDocuments,
			want:  "FooData/notes (in your Documents folder)",
		},
		{
			name:  "Homebrew's home placeholder",
			zap:   `{"trash":"/$HOME/Documents/Foo"}`,
			files: []string{"Documents/Foo/library.db"},
			want:  "Documents/Foo (in your Documents folder)",
		},
		{
			name:  "an escaped space",
			zap:   `{"trash":"~/Documents/Foo\\ Bar"}`,
			files: []string{"Documents/Foo Bar/notes.txt"},
			want:  "Documents/Foo Bar (in your Documents folder)",
		},
		{
			name:  "an app's folder in ~/.config",
			zap:   `{"trash":["~/.config/foo","~/Library/Caches/com.example.foo"]}`,
			files: []string{".config/foo/config.toml"},
			want:  ".config/foo (in your ~/.config folder)",
		},
		{
			name:  "a wildcard in another case",
			zap:   `{"trash":"~/Documents/zoom*"}`,
			files: []string{"Documents/Zoom/2026-11-12 Standup/video1.mp4"},
			want:  "Documents/Zoom (in your Documents folder)",
		},
		{
			name: "a POSIX class",
			zap:  `{"trash":"~/Documents/[[:alpha:]]oom"}`,
			want: "Documents/[[:alpha:]]oom (in your Documents folder)",
		},
		{
			name:  "a trailing slash after a wildcard",
			zap:   `{"trash":"~/Documents/Zoo*/"}`,
			files: []string{"Documents/Zoom/2026-11-12 Standup/video1.mp4"},
			want:  "Documents/Zoom (in your Documents folder)",
		},
		{
			name:  "a trailing slash on a linked folder",
			zap:   `{"delete":"~/Library/Application Support/Foo*/"}`,
			files: []string{"Documents/FooData/projects/plan.txt"},
			setup: linkSupportFolderIntoDocuments,
			want:  "Application Support/FooData (in your Documents folder)",
		},
		{
			name:  "an escaped space and a trailing slash",
			zap:   `{"trash":"~/Documents/Foo\\ Bar/"}`,
			files: []string{"Documents/Foo Bar/notes.txt"},
			want:  "Documents/Foo Bar (in your Documents folder)",
		},
		{
			name:  "a negated class that compares case exactly",
			zap:   `{"trash":"~/Documents/[!z]oom"}`,
			files: []string{"Documents/Zoom/rec.mp4"},
			want:  "Documents/Zoom (in your Documents folder)",
		},
		{
			name:  "** inside a name under a linked folder",
			zap:   `{"trash":"~/Library/Application Support/Foo**/projects"}`,
			files: []string{"Documents/FooData/projects/plan.txt"},
			setup: linkSupportFolderIntoDocuments,
			want:  "FooData/projects (in your Documents folder)",
		},
		{
			name:  "a negated set",
			zap:   `{"trash":"~/Music/[!.]*"}`,
			files: []string{"Music/Ableton/Sets/live.als"},
			want:  "Music/Ableton (in your Music folder)",
		},
		{
			name:  "a recursive glob in Documents",
			zap:   `{"trash":"~/Documents/**/Zoom"}`,
			files: []string{"Documents/Meetings/Zoom/rec.mp4"},
			want:  "Documents/**/Zoom (in your Documents folder)",
		},
		{
			name:  "a recursive glob after a wildcard that matches a linked folder",
			zap:   `{"trash":"~/Library/Application Support/Foo*/**/cache"}`,
			files: []string{"Documents/FooData/projects/cache/state.bin"},
			setup: linkSupportFolderIntoDocuments,
			want:  "Foo*/**/cache (in your Documents folder)",
		},
		{
			name:  "a letter range in another case",
			zap:   `{"trash":"~/Documents/[a-z]oom"}`,
			files: []string{"Documents/Zoom/rec.mp4"},
			want:  "Documents/Zoom (in your Documents folder)",
		},
		{
			name:  "a literal non-ASCII name after a wildcard",
			zap:   `{"trash":"~/Documents/Zoo*/über"}`,
			files: []string{"Documents/Zoom/Über/notes.txt"},
			// The lookup finds Über only where the volume folds case, as the
			// default APFS one does; Linux CI's file system does not, and
			// there Homebrew's lookup would not find it either.
			setup: func(t *testing.T, home string) {
				if _, err := os.Lstat(filepath.Join(home, "Documents", "Zoom", "über")); err != nil {
					t.Skipf("this file system compares names by case: %v", err)
				}
			},
			want: "Zoom/über (in your Documents folder)",
		},
		{
			name:  "a recursive glob under a parent folder linked into Documents",
			zap:   `{"trash":"~/Library/Application Support/FooData/**/cache"}`,
			setup: linkSupportFolderIntoDocuments,
			want:  "FooData/**/cache (in your Documents folder)",
		},
		{
			name:  "the Steam library",
			zap:   `{"trash":"~/Library/Application Support/Steam"}`,
			files: []string{"Library/Application Support/Steam/steamapps/common/Game/data.pak"},
			want:  "Application Support/Steam (the Steam library with installed games and saves)",
		},
		{
			name:  "a parent folder linked into Documents",
			zap:   `{"trash":"~/Library/Application Support/FooData/projects"}`,
			files: []string{"Documents/FooData/projects/plan.txt"},
			setup: linkSupportFolderIntoDocuments,
			want:  "FooData/projects (in your Documents folder)",
		},
		{
			name: "a path Bloom cannot check",
			zap:  `{"trash":"~/Documents/Locked/Zoom"}`,
			setup: func(t *testing.T, home string) {
				locked := filepath.Join(home, "Documents", "Locked")
				if err := os.MkdirAll(locked, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(locked, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
				if _, err := os.Lstat(filepath.Join(locked, "Zoom")); err == nil || os.IsNotExist(err) {
					t.Skipf("a mode 0 folder still answers lstat here: %v", err)
				}
			},
			want: "Documents/Locked/Zoom (in your Documents folder)",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			for _, dryRun := range []bool{true, false} {
				appPath, _ := caskUninstallTestApp(t)
				home := os.Getenv("HOME")
				writeZapTestFiles(t, home, tt.files)
				if tt.setup != nil {
					tt.setup(t, home)
				}
				runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", tt.zap)}}

				res := UninstallApp(context.Background(), runner, zapTestApp(appPath), dryRun)
				requireZapLeftOut(t, res, runner, dryRun, tt.want)
				for _, rel := range tt.files {
					if _, err := os.Lstat(filepath.Join(home, rel)); err != nil {
						t.Fatalf("dryRun=%v: %s changed: %v", dryRun, rel, err)
					}
				}
			}
		})
	}
}

// Targets that do not exist, rmdir entries, and Library data leave --zap to
// run as before.
func TestUninstallAppRunsZapThatLeavesTheUsersFilesAlone(t *testing.T) {
	appPath, cache := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Music/song.mp3"})
	if err := os.MkdirAll(filepath.Join(home, "Documents", "Foo", "Empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	zap := `{"trash":["~/Desktop/Zoom","~/Documents/Max [0-9]","~/Library/Caches/com.example.foo"],"rmdir":["~/Documents/Foo","~/Music"]}`
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", zap)}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	if !runnerCallContains(runner.calls, "brew uninstall --cask --force --zap foo") {
		t.Fatalf("zap did not run: %v", runner.calls)
	}
	if _, err := os.Lstat(cache); !os.IsNotExist(err) {
		t.Fatalf("leftover cache was not moved: %v", err)
	}
}

// brew uninstall --zap runs the stanza recorded at install time, which can
// name more than the current definition does.
func TestUninstallAppReadsTheZapStanzaInTheInstallReceipt(t *testing.T) {
	room := useTestCaskroom(t)
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Documents/Zoom/2026-11-12 Standup/video1.mp4"})
	writeCaskReceipt(t, room, "foo", `{"homebrew_version":"7.0.14","uninstall_artifacts":[{"app":["foo.app"]},{"zap":[{"trash":["~/Documents/Zoom","~/Library/Caches/us.zoom.xos"]}]}]}`)
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", "")}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), true)
	requireZapLeftOut(t, res, runner, true, "Documents/Zoom (in your Documents folder)")
}

// The receipt alone decides when brew info cannot describe the cask, as
// Homebrew itself then runs the recorded stanza.
func TestUninstallAppZapsByTheReceiptWhenBrewInfoFails(t *testing.T) {
	room := useTestCaskroom(t)
	appPath, _ := caskUninstallTestApp(t)
	writeCaskReceipt(t, room, "foo", `{"uninstall_artifacts":[{"app":["foo.app"]},{"zap":[{"trash":"~/Library/Caches/com.example.foo"}]}]}`)
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{{Err: errNotFound, Stderr: "Error: No available cask with the name \"foo\".\n"}}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	if !runnerCallContains(runner.calls, "brew uninstall --cask --force --zap foo") {
		t.Fatalf("zap did not run: %v", runner.calls)
	}
}

func TestUninstallAppRefusesZapItCannotRead(t *testing.T) {
	cases := []struct {
		name    string
		receipt string
		info    CommandOutput
	}{
		{name: "no receipt and brew info fails", info: CommandOutput{Err: errNotFound, Stderr: "Error: No available cask with the name \"foo\".\n"}},
		{name: "a receipt Bloom cannot parse", receipt: `{"uninstall_artifacts":`, info: caskInfoJSON("foo", "")},
		{name: "a zap path of an unknown shape", info: caskInfoJSON("foo", `{"trash":{"path":"~/Music/Ableton"}}`)},
		{name: "brew info for another cask", info: caskInfoJSON("bar", "")},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			room := useTestCaskroom(t)
			appPath, _ := caskUninstallTestApp(t)
			if tt.receipt != "" {
				writeCaskReceipt(t, room, "foo", tt.receipt)
			}
			runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{tt.info}}

			res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
			if res.Err == nil || !strings.Contains(res.Err.Error(), "could not read the uninstall and zap steps of Homebrew cask foo") {
				t.Fatalf("uninstall error = %v, want an unreadable-stanza refusal", res.Err)
			}
			requireZapNotRun(t, runner, appPath)
		})
	}
}

// A zap stanza that starts reaching the user's files between the plan and
// the action, as after a concurrent brew upgrade, is caught before it runs.
func TestUninstallAppRechecksZapReachBeforeRunningIt(t *testing.T) {
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Music/Ableton/Sets/live.als"})
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{
		caskInfoJSON("foo", ""),
		caskInfoJSON("foo", `{"trash":"~/Music/Ableton"}`),
	}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	requireZapLeftOut(t, res, runner, false, "Music/Ableton (in your Music folder)")
	if runner.infoCalls < 2 {
		t.Fatalf("zap stanza read %d times, want a recheck at the action boundary", runner.infoCalls)
	}
}

// Quitting the app may write a file the zap names, such as a document saved
// from its save prompt; the zap is judged again after the quit.
func TestUninstallAppRechecksZapReachAfterQuittingTheApp(t *testing.T) {
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	runner := &zapStanzaRunner{
		token:   "foo",
		appPath: appPath,
		infos:   []CommandOutput{caskInfoJSON("foo", `{"trash":["~/Documents/Zoom","~/Library/Caches/us.zoom.xos"]}`)},
		onQuitCheck: func() {
			writeZapTestFiles(t, home, []string{"Documents/Zoom/2026-11-17 Review/video1.mp4"})
		},
	}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	if !runnerCallContains(runner.calls, "/usr/bin/pgrep") {
		t.Fatalf("the app was never checked for quitting: %v", runner.calls)
	}
	requireZapLeftOut(t, res, runner, false, "Documents/Zoom (in your Documents folder)")
}

func writeInstalledCaskfile(t *testing.T, room, token, name, body string) {
	t.Helper()
	dir := filepath.Join(room, token, ".metadata", "1.0", "20240101000000.000", "Casks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Older installs keep the artifacts in the installed caskfile rather than in
// the receipt, and Homebrew zaps by that definition.
func TestUninstallAppReadsTheZapStanzaInAnInstalledCaskfile(t *testing.T) {
	cases := []struct {
		name, file, body, want string
	}{
		{
			name: "public JSON",
			file: "foo.json",
			body: `{"token":"foo","artifacts":[{"app":["foo.app"]},{"zap":[{"trash":["~/Documents/Foo"]}]}]}`,
			want: "Documents/Foo (in your Documents folder)",
		},
		{
			name: "internal JSON",
			file: "foo.internal.json",
			body: `{"token":"foo","raw_artifacts":[[":app",["foo.app"]],[":zap",{":trash":["~/Documents/Foo"]}]]}`,
			want: "Documents/Foo (in your Documents folder)",
		},
		{
			name: "internal JSON with args and a block",
			file: "foo.internal.json",
			body: `{"token":"foo","raw_artifacts":[[":zap",[],{":rmdir":"~/Music",":delete":"/$HOME/Documents/Foo"},":empty_block"]]}`,
			want: "Documents/Foo (in your Documents folder)",
		},
		{
			name: "internal JSON Bloom cannot parse",
			file: "foo.internal.json",
			body: `{"token":"foo","raw_artifacts":[[":zap",{":trash":{"path":"~/Documents/Foo"}}]]}`,
			want: "could not read the uninstall and zap steps of Homebrew cask foo",
		},
		{
			name: "an artifact list of the wrong type",
			file: "foo.internal.json",
			body: `{"token":"foo","raw_artifacts":{"zap":{"trash":"~/Documents/Foo"}}}`,
			want: "could not read the uninstall and zap steps of Homebrew cask foo",
		},
		{
			name: "internal JSON with a symbol for a path",
			file: "foo.internal.json",
			body: `{"token":"foo","raw_artifacts":[[":zap",{":trash":":documents"}]]}`,
			want: "could not read the uninstall and zap steps of Homebrew cask foo",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			room := useTestCaskroom(t)
			appPath, _ := caskUninstallTestApp(t)
			home := os.Getenv("HOME")
			writeZapTestFiles(t, home, []string{"Documents/Foo/library.db"})
			writeCaskReceipt(t, room, "foo", `{"homebrew_version":"4.0.0"}`)
			writeInstalledCaskfile(t, room, "foo", tt.file, tt.body)
			runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", "")}}

			res := UninstallApp(context.Background(), runner, zapTestApp(appPath), true)
			if !strings.HasPrefix(tt.want, "could not read") {
				requireZapLeftOut(t, res, runner, true, tt.want)
				return
			}
			if res.Err == nil || !strings.Contains(res.Err.Error(), tt.want) {
				t.Fatalf("uninstall error = %v, want %q", res.Err, tt.want)
			}
			requireZapNotRun(t, runner, appPath)
		})
	}
}

// Bloom cannot read a Ruby caskfile, so it needs the artifacts the receipt
// recorded from it at install time.
func TestUninstallAppNeedsRecordedArtifactsForARubyCaskfile(t *testing.T) {
	t.Run("not recorded", func(t *testing.T) {
		room := useTestCaskroom(t)
		appPath, _ := caskUninstallTestApp(t)
		writeCaskReceipt(t, room, "foo", `{"homebrew_version":"4.0.0"}`)
		writeInstalledCaskfile(t, room, "foo", "foo.rb", `cask "foo" do
  zap trash: "#{Dir.home}/Documents/Foo"
end
`)
		runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", "")}}

		res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
		if res.Err == nil || !strings.Contains(res.Err.Error(), "could not read the uninstall and zap steps of Homebrew cask foo") || !strings.Contains(res.Err.Error(), "is Ruby") {
			t.Fatalf("uninstall error = %v, want an unreadable-stanza refusal for the Ruby caskfile", res.Err)
		}
		requireZapNotRun(t, runner, appPath)
	})
	t.Run("recorded", func(t *testing.T) {
		room := useTestCaskroom(t)
		appPath, _ := caskUninstallTestApp(t)
		writeCaskReceipt(t, room, "foo", `{"uninstall_artifacts":[{"app":["foo.app"]},{"zap":[{"trash":"~/Library/Caches/com.example.foo"}]}]}`)
		writeInstalledCaskfile(t, room, "foo", "foo.rb", "cask \"foo\" do\nend\n")
		runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", "")}}

		res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
		if res.Err != nil {
			t.Fatalf("uninstall error = %v", res.Err)
		}
		if !runnerCallContains(runner.calls, "brew uninstall --cask --force --zap foo") {
			t.Fatalf("zap did not run: %v", runner.calls)
		}
	})
}

// Homebrew runs the uninstall stanza on every brew uninstall, with or
// without --zap, so its trash and delete entries count too, and the refusal
// does not suggest leaving --zap out.
func TestUninstallAppRefusesUninstallStepsThatReachTheUsersFiles(t *testing.T) {
	cases := []struct {
		name    string
		info    CommandOutput
		receipt string
	}{
		{
			name: "current definition",
			info: caskInfoCaskJSON("foo", `"artifacts":[{"uninstall":[{"quit":"com.example.foo","trash":"~/Documents/My Games/Foo Online"}]},{"app":["foo.app"]}]`),
		},
		{
			name:    "install receipt",
			info:    caskInfoJSON("foo", ""),
			receipt: `{"uninstall_artifacts":[{"uninstall":[{"delete":["~/Documents/My Games/Foo Online"]}]},{"app":["foo.app"]}]}`,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			room := useTestCaskroom(t)
			appPath, _ := caskUninstallTestApp(t)
			home := os.Getenv("HOME")
			writeZapTestFiles(t, home, []string{"Documents/My Games/Foo Online/characters.dat"})
			if tt.receipt != "" {
				writeCaskReceipt(t, room, "foo", tt.receipt)
			}
			runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{tt.info}}

			res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
			if res.Err == nil || !strings.Contains(res.Err.Error(), "its uninstall steps, which run with or without --zap") ||
				!strings.Contains(res.Err.Error(), "My Games/Foo Online (in your Documents folder)") {
				t.Fatalf("uninstall error = %v, want an uninstall-steps refusal", res.Err)
			}
			requireZapNotRun(t, runner, appPath)
		})
	}
}

// Steam's uninstall stanza deletes only its client bundle inside the library
// folder; the library protection applies to the zap stanza.
func TestUninstallAppLetsUninstallStepsRemoveTheSteamClient(t *testing.T) {
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Library/Application Support/Steam/Steam.AppBundle/Steam/Contents/MacOS/steam_osx"})
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{
		caskInfoCaskJSON("foo", `"artifacts":[{"uninstall":[{"delete":"~/Library/Application Support/Steam/Steam.AppBundle"}]},{"app":["foo.app"]}]`),
	}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	if !runnerCallContains(runner.calls, "brew uninstall --cask --force --zap foo") {
		t.Fatalf("zap did not run: %v", runner.calls)
	}
}

// Stanzas can sit in per-platform variations, and in an older installed
// caskfile's per-language overrides.
func TestUninstallAppReadsStanzasInVariationsAndOverrides(t *testing.T) {
	t.Run("variation in brew info", func(t *testing.T) {
		appPath, _ := caskUninstallTestApp(t)
		home := os.Getenv("HOME")
		writeZapTestFiles(t, home, []string{"Music/Ableton/Sets/live.als"})
		runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{
			caskInfoCaskJSON("foo", `"artifacts":[{"app":["foo.app"]}],"variations":{"arm64_sequoia":{"artifacts":[{"app":["foo.app"]},{"zap":[{"trash":"~/Music/Ableton"}]}]}}`),
		}}

		res := UninstallApp(context.Background(), runner, zapTestApp(appPath), true)
		requireZapLeftOut(t, res, runner, true, "Music/Ableton (in your Music folder)")
	})
	t.Run("language override in an internal caskfile", func(t *testing.T) {
		room := useTestCaskroom(t)
		appPath, _ := caskUninstallTestApp(t)
		home := os.Getenv("HOME")
		writeZapTestFiles(t, home, []string{"Documents/Foo/library.db"})
		writeCaskReceipt(t, room, "foo", `{"homebrew_version":"4.0.0"}`)
		writeInstalledCaskfile(t, room, "foo", "foo.internal.json", `{"token":"foo","raw_artifacts":[[":app",["foo.app"]]],"language_variations":[{":languages":["de"],":overrides":{"raw_artifacts":[[":zap",{":trash":"~/Documents/Foo"}]]}}]}`)
		runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", "")}}

		res := UninstallApp(context.Background(), runner, zapTestApp(appPath), true)
		requireZapLeftOut(t, res, runner, true, "Documents/Foo (in your Documents folder)")
	})
}

// Docker Desktop, Multipass and others mark a postflight step with
// "uninstall": true; that flag is not an uninstall stanza and must not make
// the cask unreadable.
func TestUninstallAppIgnoresStanzaNamesOutsideArtifactLists(t *testing.T) {
	appPath, _ := caskUninstallTestApp(t)
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{
		caskInfoCaskJSON("foo", `"artifacts":[{"app":["foo.app"]},{"postflight_steps":[{"steps":[{"command":"/bin/true","uninstall":true}]}]}]`),
	}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	if res.Err != nil {
		t.Fatalf("uninstall error = %v", res.Err)
	}
	if !runnerCallContains(runner.calls, "brew uninstall --cask --force --zap foo") {
		t.Fatalf("zap did not run: %v", runner.calls)
	}
}

// A positive class folds case at least as widely as Ruby's glob, which folds
// ranges on a case-insensitive volume, so [z]oom counts Zoom; a negated
// class stays exact, which only matches more.
func TestUninstallAppFoldsCaseInPositiveBracketClasses(t *testing.T) {
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Documents/Zoom/rec.mp4"})
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", `{"trash":"~/Documents/[z]oom"}`)}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	requireZapLeftOut(t, res, runner, false, "Documents/Zoom (in your Documents folder)")
}

// Only the Steam client bundle is exempt from the library protection for an
// uninstall stanza; removing the library itself is still refused.
func TestUninstallAppRefusesUninstallStepsThatRemoveTheSteamLibrary(t *testing.T) {
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Library/Application Support/Steam/steamapps/common/Game/data.pak"})
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{
		caskInfoCaskJSON("foo", `"artifacts":[{"uninstall":[{"delete":"~/Library/Application Support/Steam"}]},{"app":["foo.app"]}]`),
	}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "its uninstall steps") || !strings.Contains(res.Err.Error(), "the Steam library") {
		t.Fatalf("uninstall error = %v, want the library refused", res.Err)
	}
	requireZapNotRun(t, runner, appPath)
}

// The Steam client exemption covers the Steam library alone: a client
// bundle linked into Documents still leads an uninstall delete there.
func TestUninstallAppJudgesALinkedSteamClientByWhereItLeads(t *testing.T) {
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Documents/FooData/data/save.dat"})
	steam := filepath.Join(home, "Library", "Application Support", "Steam")
	if err := os.MkdirAll(steam, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "Documents", "FooData"), filepath.Join(steam, "Steam.AppBundle")); err != nil {
		t.Fatal(err)
	}
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{
		caskInfoCaskJSON("foo", `"artifacts":[{"uninstall":[{"delete":"~/Library/Application Support/Steam/Steam.AppBundle/data"}]},{"app":["foo.app"]}]`),
	}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "Steam.AppBundle/data (in your Documents folder)") {
		t.Fatalf("uninstall error = %v, want the linked client bundle refused", res.Err)
	}
	requireZapNotRun(t, runner, appPath)
}

// Ruby's ** does not follow links, but the components after it can match a
// link below the head and go on through it.
func TestUninstallAppFollowsLinksBelowARecursiveGlob(t *testing.T) {
	for _, zap := range []string{
		`{"trash":"~/Library/Application Support/Bar/**/linked/projects"}`,
		`{"trash":"~/Library/Application Support/Bar/**/link*/"}`,
	} {
		appPath, _ := caskUninstallTestApp(t)
		home := os.Getenv("HOME")
		writeZapTestFiles(t, home, []string{"Documents/FooData/projects/plan.txt"})
		nested := filepath.Join(home, "Library", "Application Support", "Bar", "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(home, "Documents", "FooData"), filepath.Join(nested, "linked")); err != nil {
			t.Fatal(err)
		}
		runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", zap)}}

		res := UninstallApp(context.Background(), runner, zapTestApp(appPath), true)
		requireZapLeftOut(t, res, runner, true, "(in your Documents folder)")
	}
}

// A ** head that is itself a link is walked where it leads, as Ruby follows
// the link the head names.
func TestUninstallAppWalksALinkedRecursiveGlobHead(t *testing.T) {
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Documents/FooData/projects/plan.txt"})
	elsewhere := filepath.Join(home, "Elsewhere", "nested")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "Documents", "FooData"), filepath.Join(elsewhere, "linked")); err != nil {
		t.Fatal(err)
	}
	support := filepath.Join(home, "Library", "Application Support")
	if err := os.MkdirAll(support, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "Elsewhere"), filepath.Join(support, "Bar")); err != nil {
		t.Fatal(err)
	}
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", `{"trash":"~/Library/Application Support/Bar/**/linked/projects"}`)}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), true)
	requireZapLeftOut(t, res, runner, true, "(in your Documents folder)")
}

// A tree below a ** head that is too large to walk counts as reaching.
func TestUninstallAppLeavesOutZapForARecursiveGlobItCannotWalk(t *testing.T) {
	previous := recursiveZapWalkLimit
	recursiveZapWalkLimit = 3
	t.Cleanup(func() { recursiveZapWalkLimit = previous })
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Library/Application Support/Bar/a/1", "Library/Application Support/Bar/b/2", "Library/Application Support/Bar/c/3"})
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", `{"trash":"~/Library/Application Support/Bar/**/cache"}`)}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), true)
	requireZapLeftOut(t, res, runner, true, "below a folder too large for Bloom to check")
}

// Each Caskroom's receipt counts, since the brew on PATH picks the prefix.
func TestUninstallAppReadsTheReceiptInEveryCaskroom(t *testing.T) {
	base := t.TempDir()
	first, second := filepath.Join(base, "opt", "Caskroom"), filepath.Join(base, "usr", "Caskroom")
	previous := caskroomDirs
	caskroomDirs = []string{first, second}
	t.Cleanup(func() { caskroomDirs = previous })
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Music/Ableton/Sets/live.als"})
	writeCaskReceipt(t, first, "foo", `{"uninstall_artifacts":[{"zap":[{"trash":"~/Library/Caches/Ableton"}]}]}`)
	writeCaskReceipt(t, second, "foo", `{"uninstall_artifacts":[{"zap":[{"trash":"~/Music/Ableton"}]}]}`)
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", "")}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), true)
	requireZapLeftOut(t, res, runner, true, "Music/Ableton (in your Music folder)")
}

// The summary names the command Bloom runs and what leaving out --zap kept,
// without repeating data the kept lines already list.
func TestPrintUninstallSummaryExplainsALeftOutZap(t *testing.T) {
	var out, errOut strings.Builder
	app := &App{Out: &out, Err: &errOut}
	res := UninstallResult{
		App:        AppEntry{Path: "/Applications/Foo.app", Name: "Foo"},
		BrewCask:   "foo",
		ZapSkipped: true,
		ZapKept: []UninstallKeptPath{
			{Path: "/Users/test/Music/Foo", Reason: "in your Music folder"},
			{Path: "/Users/test/Library/Caches/Buzz", Reason: "Buzz's Whisper models and plugins"},
		},
		KeptData: []UninstallKeptPath{{Path: "/Users/test/Library/Caches/Buzz", Reason: "Buzz's Whisper models and plugins"}},
	}
	app.printUninstallSummary(BatchSummary{Results: []UninstallResult{res}}, true, false)
	text := out.String()
	for _, want := range []string{
		"   would run: brew uninstall --cask --force foo\n",
		"   left out --zap, which would also remove /Users/test/Music/Foo (in your Music folder)\n",
		"   kept /Users/test/Library/Caches/Buzz, Buzz's Whisper models and plugins\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "--zap foo") || strings.Count(text, "Library/Caches/Buzz") != 1 {
		t.Fatalf("summary repeats kept data or names --zap:\n%s", text)
	}
}

// Browser profiles are kept, but the manifests apps install into them are
// the apps' own: a step that removes only those runs as written.
func TestUninstallAppLetsCasksRemoveTheirBrowserManifests(t *testing.T) {
	manifests := []string{
		"Library/Application Support/Google/Chrome/NativeMessagingHosts/com.example.foo.json",
		"Library/Application Support/Microsoft Edge/External Extensions/abcdefghijklmnop.json",
		"Library/Application Support/BraveSoftware/Brave-Browser/NativeMessagingHosts/com.example.foo.json",
	}
	cases := []struct {
		name string
		info CommandOutput
	}{
		{"zap", caskInfoJSON("foo", `{"trash":["~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.example.foo.json","~/Library/Application Support/BraveSoftware/Brave-Browser/NativeMessagingHosts/com.example.foo.json"]}`)},
		{"uninstall stanza", caskInfoCaskJSON("foo", `"artifacts":[{"uninstall":[{"delete":["~/Library/Application Support/Google/Chrome/NativeMessagingHosts/com.example.foo.json","~/Library/Application Support/Microsoft Edge/External Extensions/abcdefghijklmnop.json"]}]},{"app":["foo.app"]}]`)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			appPath, _ := caskUninstallTestApp(t)
			home := os.Getenv("HOME")
			writeZapTestFiles(t, home, append(append([]string{}, manifests...), "Library/Application Support/Google/Chrome/Default/Bookmarks"))
			runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{tt.info}}

			res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
			if res.Err != nil || res.ZapSkipped {
				t.Fatalf("uninstall error = %v, zap left out = %v (%#v), want the manifests' steps to run", res.Err, res.ZapSkipped, res.ZapKept)
			}
			if !runnerCallContains(runner.calls, "brew uninstall --cask --force --zap foo") {
				t.Fatalf("zap did not run: %v", runner.calls)
			}
		})
	}
}

// A browser's own zap of its whole profile folder leaves out --zap, so the
// profile stays.
func TestUninstallAppKeepsABrowserProfileFromItsZap(t *testing.T) {
	appPath, _ := caskUninstallTestApp(t)
	home := os.Getenv("HOME")
	writeZapTestFiles(t, home, []string{"Library/Application Support/Google/Chrome/Default/Login Data"})
	runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{caskInfoJSON("foo", `{"trash":["~/Library/Application Support/Google/Chrome","~/Library/Caches/Google/Chrome"]}`)}}

	res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
	requireZapLeftOut(t, res, runner, false, "Google/Chrome (browser profiles with passwords, bookmarks, and history)")
}

// The manifest exemption is judged per expanded alternative after its ..
// segments resolve: Homebrew checks for .. only before it expands braces,
// so {x,../Default} under NativeMessagingHosts reaches the profile itself.
func TestUninstallAppJudgesTheManifestExemptionAfterExpansion(t *testing.T) {
	entry := `"~/Library/Application Support/Google/Chrome/NativeMessagingHosts/{x,../Default}"`
	cases := []struct {
		name string
		info CommandOutput
	}{
		{"zap", caskInfoJSON("foo", `{"delete":`+entry+`}`)},
		{"uninstall stanza", caskInfoCaskJSON("foo", `"artifacts":[{"uninstall":[{"delete":`+entry+`}]},{"app":["foo.app"]}]`)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			appPath, _ := caskUninstallTestApp(t)
			home := os.Getenv("HOME")
			writeZapTestFiles(t, home, []string{
				"Library/Application Support/Google/Chrome/NativeMessagingHosts/x",
				"Library/Application Support/Google/Chrome/Default/Login Data",
			})
			runner := &zapStanzaRunner{token: "foo", appPath: appPath, infos: []CommandOutput{tt.info}}

			res := UninstallApp(context.Background(), runner, zapTestApp(appPath), false)
			if tt.name == "zap" {
				requireZapLeftOut(t, res, runner, false, "Chrome/Default (browser profiles with passwords, bookmarks, and history)")
				return
			}
			if res.Err == nil || !strings.Contains(res.Err.Error(), "its uninstall steps") || !strings.Contains(res.Err.Error(), "browser profiles") {
				t.Fatalf("uninstall error = %v, want the uninstall stanza refused", res.Err)
			}
			requireZapNotRun(t, runner, appPath)
		})
	}
}
