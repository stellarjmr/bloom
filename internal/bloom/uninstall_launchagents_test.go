package bloom

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRelatedPathsRequiresLaunchAgentOwnership(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := filepath.Join(home, "Applications", "Foo.app")
	writeTestInfoPlist(t, appPath, "com.example.foo", "Foo")
	mainBinary := filepath.Join(appPath, "Contents", "MacOS", "Foo")
	writeTestExecutable(t, mainBinary)
	helper := filepath.Join(appPath, "Contents", "Library", "LoginItems", "Foo Helper.app", "Contents", "MacOS", "Foo Helper")
	writeTestExecutable(t, helper)
	outside := t.TempDir()
	outsideTool := filepath.Join(outside, "tool")
	writeTestExecutable(t, outsideTool)
	// A folder inside the bundle that resolves outside of it.
	if err := os.Symlink(outside, filepath.Join(appPath, "Contents", "Helpers")); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(home, "Library", "LaunchAgents")
	agent := func(name string) string { return filepath.Join(dir, name) }
	owned := []string{
		agent("com.example.foo.plist"),
		agent("com.example.foo.helper.plist"),
		agent("io.vendor.sync.plist"),
	}
	writeTestLaunchAgent(t, owned[0], "/usr/local/libexec/foo-updater", true, "")
	writeTestLaunchAgent(t, owned[1], helper, false, "")
	writeTestLaunchAgent(t, owned[2], mainBinary, true, "")

	notOwned := []string{
		agent("com.riot.checkinstalls.plist"),
		agent("com.example.foo.nightly.plist"),
		agent("io.vendor.escape.plist"),
		agent("io.vendor.symlinked-folder.plist"),
		agent("io.vendor.missing.plist"),
		agent("io.vendor.prefix.plist"),
		agent("com.apple.foo.plist"),
		agent("io.vendor.link.plist"),
		agent("io.vendor.nested.plist"),
	}
	writeTestLaunchAgent(t, notOwned[0], outsideTool, false, appPath)
	writeTestLaunchAgent(t, notOwned[1], "/Applications/Foo Nightly.app/Contents/MacOS/Foo", false, "")
	writeTestLaunchAgent(t, notOwned[2], appPath+"/Contents/../../../tool", true, "")
	writeTestLaunchAgent(t, notOwned[3], filepath.Join(appPath, "Contents", "Helpers", "tool"), true, "")
	writeTestLaunchAgent(t, notOwned[4], filepath.Join(appPath, "Contents", "MacOS", "Missing"), true, "")
	writeTestLaunchAgent(t, notOwned[5], appPath+".backup/Contents/MacOS/Foo", true, "")
	writeTestLaunchAgent(t, notOwned[6], mainBinary, true, "")
	linkTarget := filepath.Join(outside, "linked.plist")
	writeTestLaunchAgent(t, linkTarget, mainBinary, true, "")
	if err := os.Symlink(linkTarget, notOwned[7]); err != nil {
		t.Fatal(err)
	}
	nested := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
  <key>Sockets</key><dict><key>Program</key><string>` + mainBinary + `</string></dict>
  <key>ProgramArguments</key><array><string>` + outsideTool + `</string></array>
</dict></plist>`
	if err := os.WriteFile(notOwned[8], []byte(nested), 0o644); err != nil {
		t.Fatal(err)
	}

	app := AppEntry{Path: appPath, Name: "Foo", BundleID: "com.example.foo"}
	paths := FindRelatedPaths(app)
	for _, path := range owned {
		if !containsString(paths, path) {
			t.Fatalf("owned LaunchAgent %q missing: %#v", path, paths)
		}
	}
	for _, path := range notOwned {
		if containsString(paths, path) {
			t.Fatalf("LaunchAgent %q claimed without ownership proof: %#v", path, paths)
		}
	}

	if _, err := os.Stat("/usr/bin/plutil"); err == nil {
		binaryAgent := agent("io.vendor.binary.plist")
		writeTestLaunchAgent(t, binaryAgent, mainBinary, false, "")
		if out, err := exec.Command("/usr/bin/plutil", "-convert", "binary1", binaryAgent).CombinedOutput(); err != nil {
			t.Fatalf("convert plist: %v: %s", err, out)
		}
		if !containsString(FindRelatedPaths(app), binaryAgent) {
			t.Fatal("binary LaunchAgent with a bundle program was not owned")
		}
	}
}

func caseInsensitiveDir(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(probe)
	_, err := os.Stat(filepath.Join(dir, "caseprobe"))
	return err == nil
}

func TestLaunchAgentOwnershipComparesBundleIdentityNotSpelling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := filepath.Join(home, "Applications", "Foo.APP")
	writeTestInfoPlist(t, appPath, "com.example.foo", "Foo")
	writeTestExecutable(t, filepath.Join(appPath, "Contents", "MacOS", "Foo"))
	dir := filepath.Join(home, "Library", "LaunchAgents")

	// A folder outside the bundle that links into it could be retargeted to
	// another app after planning without the plist changing, so it proves
	// nothing; neither does an executable that is itself a symlink.
	links := t.TempDir()
	if err := os.Symlink(appPath, filepath.Join(links, "FooLink")); err != nil {
		t.Fatal(err)
	}
	viaLink := filepath.Join(dir, "io.vendor.via-link.plist")
	writeTestLaunchAgent(t, viaLink, filepath.Join(links, "FooLink", "Contents", "MacOS", "Foo"), true, "")
	cli := filepath.Join(links, "foo-cli")
	if err := os.Symlink(filepath.Join(appPath, "Contents", "MacOS", "Foo"), cli); err != nil {
		t.Fatal(err)
	}
	viaCLI := filepath.Join(dir, "io.vendor.cli.plist")
	writeTestLaunchAgent(t, viaCLI, cli, true, "")
	// Unicode folding equates U+017F with "s"; the exact-name shortcut must
	// compare ASCII case only, leaving this agent to its program check.
	longS := filepath.Join(dir, "com.example.fooſ.plist")
	writeTestLaunchAgent(t, longS, "/usr/local/bin/other-tool", true, "")

	app := AppEntry{Path: appPath, Name: "Foo", BundleID: "com.example.foos"}
	paths := FindRelatedPaths(app)
	for _, path := range []string{viaLink, viaCLI, longS} {
		if containsString(paths, path) {
			t.Fatalf("LaunchAgent %q claimed without ownership proof: %#v", path, paths)
		}
	}

	if !caseInsensitiveDir(t, home) {
		t.Skip("case-equivalent bundle spelling needs a case-insensitive volume")
	}
	respelled := filepath.Join(dir, "io.vendor.respelled.plist")
	writeTestLaunchAgent(t, respelled, filepath.Join(home, "Applications", "foo.app", "Contents", "MacOS", "Foo"), false, "")
	if !containsString(FindRelatedPaths(app), respelled) {
		t.Fatal("case-equivalent spelling of the selected bundle was not owned")
	}
}

type launchAgentUnloadRunner struct {
	onUnload func(plist string)
	calls    []string
}

func (r *launchAgentUnloadRunner) LookPath(string) (string, error) {
	return "", errors.New("not found")
}

func (r *launchAgentUnloadRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	if name == "/bin/launchctl" && len(args) == 2 && args[0] == "unload" {
		r.onUnload(args[1])
	}
	return CommandOutput{Err: errors.New("not running")}
}

func TestUnloadLaunchAgentsJudgesEachAgentRightBeforeItsUnload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := filepath.Join(home, "Applications", "Foo.app")
	writeTestInfoPlist(t, appPath, "com.example.foo", "Foo")
	mainBinary := filepath.Join(appPath, "Contents", "MacOS", "Foo")
	writeTestExecutable(t, mainBinary)
	dir := filepath.Join(home, "Library", "LaunchAgents")
	first := filepath.Join(dir, "a.vendor.first.plist")
	second := filepath.Join(dir, "b.vendor.second.plist")
	writeTestLaunchAgent(t, first, mainBinary, true, "")
	writeTestLaunchAgent(t, second, mainBinary, true, "")

	r := &launchAgentUnloadRunner{onUnload: func(plist string) {
		if plist == first {
			// An updater rewrites the next agent while the first unloads.
			writeTestLaunchAgent(t, second, "/usr/local/bin/other-tool", true, "")
		}
	}}
	unloadMatchingLaunchAgents(context.Background(), r, AppEntry{Path: appPath, Name: "Foo", BundleID: "com.example.foo"})
	if !runnerCallContains(r.calls, "/bin/launchctl unload "+first) {
		t.Fatalf("owned agent was not unloaded: %#v", r.calls)
	}
	if runnerCallContains(r.calls, "/bin/launchctl unload "+second) {
		t.Fatalf("agent rewritten before its turn was unloaded on a stale verdict: %#v", r.calls)
	}
}

type launchAgentSwapRunner struct {
	swap    func()
	swapped bool
	calls   []string
}

func (r *launchAgentSwapRunner) LookPath(string) (string, error) {
	return "", errors.New("not found")
}

func (r *launchAgentSwapRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	// stopApp probes the process first, after the plan has been built and
	// before any LaunchAgent is unloaded or moved.
	if name == "/usr/bin/pgrep" && !r.swapped {
		r.swapped = true
		r.swap()
	}
	return CommandOutput{Err: errors.New("not running")}
}

func TestUninstallAppKeepsLaunchAgentChangedBeforeMove(t *testing.T) {
	for _, mode := range []string{"replaced", "rewritten"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
			appPath := filepath.Join(home, "Applications", "Foo.app")
			writeTestInfoPlist(t, appPath, "com.example.foo", "Foo")
			mainBinary := filepath.Join(appPath, "Contents", "MacOS", "Foo")
			writeTestExecutable(t, mainBinary)
			agentPath := filepath.Join(home, "Library", "LaunchAgents", "io.vendor.sync.plist")
			writeTestLaunchAgent(t, agentPath, mainBinary, true, "")

			r := &launchAgentSwapRunner{swap: func() {
				if mode == "replaced" {
					if err := os.Remove(agentPath); err != nil {
						t.Fatal(err)
					}
					writeTestLaunchAgent(t, agentPath, "/usr/local/bin/other-tool", true, "")
					return
				}
				data, err := os.ReadFile(agentPath)
				if err != nil {
					t.Fatal(err)
				}
				rewritten := strings.Replace(string(data), mainBinary, "/usr/local/bin/other-tool", 1)
				if err := os.WriteFile(agentPath, []byte(rewritten), 0o644); err != nil {
					t.Fatal(err)
				}
			}}

			res := UninstallApp(context.Background(), r, AppEntry{Path: appPath, Name: "Foo", BundleID: "com.example.foo"}, false)
			if !r.swapped {
				t.Fatalf("process probe never ran: %#v", r.calls)
			}
			if !res.AppRemoved {
				t.Fatalf("app bundle was not removed: %+v", res)
			}
			if _, err := os.Lstat(agentPath); err != nil {
				t.Fatalf("changed LaunchAgent was moved: %v", err)
			}
			if !containsString(res.Failed, agentPath) {
				t.Fatalf("changed LaunchAgent not reported as kept: %#v", res.Failed)
			}
			if runnerCallContains(r.calls, "/bin/launchctl unload "+agentPath) {
				t.Fatalf("changed LaunchAgent was unloaded: %#v", r.calls)
			}
		})
	}
}
