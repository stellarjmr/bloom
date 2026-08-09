package bloom

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cleanProbeRunner struct {
	processTables []string
	psCalls       int
	onPS          func(call int)
	lsofAvailable bool
	lsofOutput    CommandOutput
}

func (r *cleanProbeRunner) LookPath(file string) (string, error) {
	if file == "ps" || file == "lsof" && r.lsofAvailable {
		return "/usr/bin/" + file, nil
	}
	return "", os.ErrNotExist
}

func (r *cleanProbeRunner) Run(ctx context.Context, name string, args ...string) CommandOutput {
	switch name {
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
	default:
		return OSRunner{}.Run(ctx, name, args...)
	}
}

func TestValidateCleanPathSafetyBoundaries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, path := range []string{"", "relative/path", "/tmp/../etc", "/", "/System", "/usr/bin", "/etc"} {
		if err := validateCleanPath(path); err == nil {
			t.Fatalf("validateCleanPath(%q) succeeded, want rejection", path)
		}
	}

	valid := filepath.Join(home, "storage", "default", "https+++example.com", "name..files", "data")
	if err := validateCleanPath(valid); err != nil {
		t.Fatalf("Firefox-style path rejected: %v", err)
	}

	if err := validateCleanPath(filepath.Join(home, ".Trash", "victim")); err == nil {
		t.Fatal("Trash path was not rejected")
	}

	link := filepath.Join(home, "system-link")
	if err := os.Symlink("/System", link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if err := validateCleanPath(link); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Fatalf("symlink to /System error = %v, want protected rejection", err)
	}
}

func TestCleanWhitelistMatchesGlobParentChildAndSentinel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	patterns := []string{"~/Library/Caches/Keep*", "~/Library/Caches/Parent", cleanFinderMetadataSentinel}

	if !isCleanPathWhitelisted(filepath.Join(home, "Library", "Caches", "KeepApp", "data"), patterns) {
		t.Fatal("glob whitelist did not match child path")
	}
	if !isCleanPathWhitelisted(filepath.Join(home, "Library", "Caches", "Parent", "child"), patterns) {
		t.Fatal("directory whitelist did not match child path")
	}
	if !isCleanPathWhitelisted(filepath.Join(home, "Documents", ".DS_Store"), patterns) {
		t.Fatal("Finder metadata sentinel did not protect .DS_Store")
	}
}

func TestCleanHardProtectsHighValueData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	protected := []string{
		filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "paper.pdf"),
		filepath.Join(home, "Library", "Application Support", "CloudDocs", "session.db"),
		filepath.Join(home, "Library", "Caches", "CloudKit", "sync-state.db"),
		filepath.Join(home, "Library", "Caches", "PassKit", "passes.db"),
		filepath.Join(home, "Library", "Group Containers", "group.com.apple.notes", "NoteStore.sqlite"),
		filepath.Join(home, "Library", "Containers", "com.apple.Notes", "Data", "Library", "Caches", "note-cache"),
		filepath.Join(home, "Library", "Notes", "NotesV7.storedata"),
		filepath.Join(home, "Library", "Mail", "V10", "MailData", "Envelope Index"),
		filepath.Join(home, "Library", "Mail Downloads", "attachment.pdf"),
		filepath.Join(home, "Library", "Containers", "com.apple.mail", "Data", "Library", "Caches", "cache.db"),
		filepath.Join(home, "Library", "Caches", "com.apple.mail", "cache.db"),
		filepath.Join(home, "Library", "Keychains", "login.keychain-db"),
		filepath.Join(home, "Library", "Accounts", "Accounts4.sqlite"),
		filepath.Join(home, "Library", "Cookies", "Cookies.binarycookies"),
		filepath.Join(home, "Library", "Safari", "History.db"),
		filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "Default", "History"),
		filepath.Join(home, "Library", "Application Support", "Firefox", "Profiles", "main.default", "cookies.sqlite"),
		filepath.Join(home, "Library", "LaunchAgents", "com.example.agent.plist"),
		filepath.Join(home, "Library", "LaunchAgents", "com.example.agent.data"),
		filepath.Join(home, "Library", "LaunchDaemons", "com.example.daemon.data"),
		filepath.Join(home, "Library", "Messages", "chat.db"),
		filepath.Join(home, "Library", "Reminders", "Container_v1"),
		filepath.Join(home, "Library", "Application Support", "AddressBook", "AddressBook-v22.abcddb"),
		filepath.Join(home, "Library", "Application Support", "MobileSync", "Backup", "device", "Manifest.db"),
		filepath.Join(home, "Library", "Application Support", "Spotify", "PersistentCache", "offline.bnk"),
		filepath.Join(home, "Library", "Application Support", "Raycast", "ai-chat-state.db"),
		filepath.Join(home, "Library", "Caches", "com.raycast.macos", "urlcache", "state.db"),
		filepath.Join(home, "Library", "Caches", "com.raycast.shared", "fsCachedData", "state.db"),
		filepath.Join(home, "Library", "Containers", "com.dropbox.DropboxMacUpdate", "Data", "Documents", "state.db"),
		filepath.Join(home, "Library", "Containers", "com.microsoft.OneDrive-mac", "Data", "Documents", "state.db"),
		filepath.Join(home, "Library", "Containers", "com.lmstudio.lmstudio", "Data", "models", "model.gguf"),
		filepath.Join(home, ".config", "gcloud", "logs", "gcloud.log"),
		filepath.Join(home, ".config", "tool", "settings.json"),
		filepath.Join(home, "Library", "Logs", "bloom", "uninstall.log"),
		filepath.Join(home, "Pictures", "Photos Library.photoslibrary", "database", "Photos.sqlite"),
	}

	for _, path := range protected {
		if !shouldProtectCleanPath(path) {
			t.Fatalf("shouldProtectCleanPath(%q) = false, want true", path)
		}
		if err := validateCleanPath(path); err == nil {
			t.Fatalf("validateCleanPath(%q) succeeded, want protected rejection", path)
		}
	}
}

func TestRunCleanDoesNotTargetRaycastCacheState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	raycastState := filepath.Join(home, "Library", "Caches", "com.raycast.macos", "urlcache", "state.db")
	dropFile := filepath.Join(home, "Library", "Caches", "DropApp", "data.tmp")
	for _, path := range []string{raycastState, dropFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg})
	if !cleanResultContains(res, filepath.Dir(dropFile)) {
		t.Fatalf("dry-run targets missing DropApp: %#v", res.Targets)
	}
	if cleanResultCovers(res, raycastState) || cleanResultContains(res, filepath.Dir(filepath.Dir(raycastState))) {
		t.Fatalf("Raycast cache state appeared in clean targets: targets=%#v skipped=%#v", res.Targets, res.Skipped)
	}
}

func TestValidateCleanPathRejectsParentSymlinkToProtectedDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	library := filepath.Join(home, "Library")
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("Mobile Documents", func(t *testing.T) {
		mobileDocs := filepath.Join(library, "Mobile Documents")
		if err := os.MkdirAll(mobileDocs, 0o755); err != nil {
			t.Fatal(err)
		}
		cacheLink := filepath.Join(library, "Caches")
		if err := os.Symlink(mobileDocs, cacheLink); err != nil {
			t.Skipf("cannot create parent symlink: %v", err)
		}
		candidate := filepath.Join(cacheLink, "cache.tmp")
		if err := os.WriteFile(candidate, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := validateCleanPath(candidate); err == nil || !strings.Contains(err.Error(), "protected") {
			t.Fatalf("validateCleanPath(%q) = %v, want protected rejection", candidate, err)
		}
		if err := os.Remove(candidate); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(cacheLink); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ordinary user data", func(t *testing.T) {
		documents := filepath.Join(home, "Documents")
		if err := os.MkdirAll(documents, 0o755); err != nil {
			t.Fatal(err)
		}
		cacheLink := filepath.Join(library, "Caches")
		if err := os.Symlink(documents, cacheLink); err != nil {
			t.Skipf("cannot create parent symlink: %v", err)
		}
		candidate := filepath.Join(cacheLink, "report.pdf")
		if err := os.WriteFile(candidate, []byte("important"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := validateCleanPath(candidate); err == nil || !strings.Contains(err.Error(), "redirected") {
			t.Fatalf("validateCleanPath(%q) = %v, want redirected rejection", candidate, err)
		}
		if err := os.Remove(candidate); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(cacheLink); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Trash", func(t *testing.T) {
		trash := filepath.Join(home, ".Trash")
		if err := os.MkdirAll(trash, 0o755); err != nil {
			t.Fatal(err)
		}
		cacheLink := filepath.Join(library, "Caches")
		if err := os.Symlink(trash, cacheLink); err != nil {
			t.Skipf("cannot create parent symlink: %v", err)
		}
		candidate := filepath.Join(cacheLink, "cache.tmp")
		if err := os.WriteFile(candidate, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := validateCleanPath(candidate); err == nil || !strings.Contains(err.Error(), "Trash") {
			t.Fatalf("validateCleanPath(%q) = %v, want Trash rejection", candidate, err)
		}
		if err := os.Remove(candidate); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(cacheLink); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCleanSkipsOpenOrUncertainIncompleteDownloads(t *testing.T) {
	if shouldSkipOpenIncompleteDownload(context.Background(), pathRunner{}, "/tmp/complete.tmp") {
		t.Fatal("non-download path was treated as an incomplete download")
	}
	if !shouldSkipOpenIncompleteDownload(context.Background(), pathRunner{}, "/tmp/movie.part") {
		t.Fatal("missing lsof should conservatively skip incomplete downloads")
	}
	if !shouldSkipOpenIncompleteDownload(context.Background(), pathRunner{"lsof": true}, "/tmp/movie.crdownload") {
		t.Fatal("open incomplete download was not skipped")
	}
}

func TestRunCleanDryRunHonorsWhitelistAndNeverCleansTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	keepFile := filepath.Join(home, "Library", "Caches", "KeepApp", "data.tmp")
	dropFile := filepath.Join(home, "Library", "Caches", "DropApp", "data.tmp")
	trashFile := filepath.Join(home, ".Trash", "old.tmp")
	for _, path := range []string{keepFile, dropFile, trashFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = []string{"~/Library/Caches/KeepApp*"}
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg})

	if !cleanResultContains(res, filepath.Dir(dropFile)) {
		t.Fatalf("dry-run targets missing DropApp: %#v", res.Targets)
	}
	if cleanResultContains(res, filepath.Dir(keepFile)) {
		t.Fatalf("whitelisted KeepApp appeared in targets: %#v", res.Targets)
	}
	if cleanResultContains(res, filepath.Dir(trashFile)) || cleanResultContains(res, trashFile) {
		t.Fatalf("Trash appeared in targets: %#v", res.Targets)
	}
	for _, path := range []string{keepFile, dropFile, trashFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry-run touched %s: %v", path, err)
		}
	}
}

func TestSetCleanWhitelistCannotRemoveHardSafetyEntries(t *testing.T) {
	cfg := DefaultConfig()
	if err := SetCleanWhitelist(&cfg, []string{"~/.cache/custom-keep/*"}); err != nil {
		t.Fatal(err)
	}
	if !containsString(cfg.Clean.Whitelist, cleanFinderMetadataSentinel) {
		t.Fatalf("hard safety entry was removed: %#v", cfg.Clean.Whitelist)
	}
}

func TestRunCleanNeverTargetsHighValueCachesEvenWithoutWhitelist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	paths := []string{
		filepath.Join(home, "Library", "Caches", "DropApp", "data.tmp"),
		filepath.Join(home, "Library", "Caches", "com.apple.mail", "cache.db"),
		filepath.Join(home, "Library", "Caches", "CloudKit", "sync-state.db"),
		filepath.Join(home, "Library", "Caches", "PassKit", "passes.db"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg})
	if !cleanResultContains(res, filepath.Join(home, "Library", "Caches", "DropApp")) {
		t.Fatalf("dry-run targets missing DropApp: %#v", res.Targets)
	}
	for _, path := range []string{
		filepath.Join(home, "Library", "Caches", "com.apple.mail"),
		filepath.Join(home, "Library", "Caches", "CloudKit"),
		filepath.Join(home, "Library", "Caches", "PassKit"),
	} {
		if cleanResultContains(res, path) {
			t.Fatalf("high-value cache target %q appeared in clean targets: %#v", path, res.Targets)
		}
	}
}

func TestRunCleanNeverTargetsZshCompletionCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	zcompdump := filepath.Join(home, ".zcompdump")
	zcompdumpVersioned := filepath.Join(home, ".zcompdump-host-5.9")
	dropFile := filepath.Join(home, "Library", "Caches", "DropApp", "data.tmp")
	for _, path := range []string{zcompdump, zcompdumpVersioned, dropFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg})
	if !cleanResultContains(res, filepath.Dir(dropFile)) {
		t.Fatalf("dry-run targets missing DropApp: %#v", res.Targets)
	}
	for _, path := range []string{zcompdump, zcompdumpVersioned} {
		if cleanResultContains(res, path) {
			t.Fatalf("Zsh completion cache %q appeared in targets: %#v", path, res.Targets)
		}
	}
}

func TestRunCleanTargetsMoleCompatibleDeveloperCaches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	files := []string{
		filepath.Join(home, ".cache", "webpack", "cache.bin"),
		filepath.Join(home, ".cache", "node-gyp", "headers.tar.gz"),
		filepath.Join(home, ".pyenv", "cache", "Python-3.12.0.tar.xz"),
		filepath.Join(home, ".jupyter", "runtime", "kernel.json"),
		filepath.Join(home, ".gem", "specs", "rubygems.org%443", "latest_specs.4.8"),
		filepath.Join(home, ".gem", "ruby", "3.3.0", "cache", "rake.gem"),
		filepath.Join(home, ".bundle", "cache", "compact_index", "rubygems.org.versions"),
		filepath.Join(home, ".rbenv", "cache", "ruby-3.3.0.tar.gz"),
		filepath.Join(home, ".kube", "cache", "discovery", "api.json"),
		filepath.Join(home, ".aws", "cli", "cache", "session.json"),
		filepath.Join(home, ".azure", "logs", "az.log"),
		filepath.Join(home, ".cache", "terraform", "plugin.zip"),
		filepath.Join(home, ".cache", "prisma", "engine.gz"),
		filepath.Join(home, "Library", "Caches", "lima", "download", "by-url-sha256", "image.tar"),
		filepath.Join(home, ".vagrant.d", "tmp", "box.tmp"),
		filepath.Join(home, ".local", "share", "containers", "storage", "tmp", "layer.tmp"),
		filepath.Join(home, "Library", "Caches", "Zed", "cache.bin"),
		filepath.Join(home, "Library", "Logs", "Zed", "zed.log"),
		filepath.Join(home, "Library", "Caches", "com.mitchellh.ghostty", "cache.bin"),
		filepath.Join(home, "Library", "Caches", "GeoServices", "map.cache"),
	}
	for _, path := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	runner := &cleanProbeRunner{processTables: []string{"/sbin/launchd\n"}}
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg, Runner: runner})
	for _, path := range files {
		if !cleanResultCovers(res, path) {
			t.Fatalf("dry-run targets missing Mole-compatible cache %q: targets=%#v skipped=%#v", path, res.Targets, res.Skipped)
		}
	}
}

func TestCleanCacheOwnerProcessMatchingRequiresCorroboration(t *testing.T) {
	table := strings.Join([]string{
		"/Applications/Claude.app/Contents/Frameworks/Squirrel.framework/Resources/ShipIt com.anthropic.claudefordesktop.ShipIt",
		"/System/Library/PrivateFrameworks/DataAccess.framework/Support/dataaccessd",
		"/usr/libexec/syncdefaultsd",
	}, "\n")

	if cleanProcessTableMatchesOwner(table, "com.microsoft.VSCode.ShipIt") {
		t.Fatal("a different vendor's ShipIt process claimed the VS Code cache")
	}
	if cleanProcessTableMatchesOwner(table, "com.plausiblelabs.crashreporter.data") {
		t.Fatal("an embedded data substring claimed an unrelated cache")
	}
	if !cleanProcessTableMatchesOwner(table, "com.anthropic.claudefordesktop.ShipIt") {
		t.Fatal("the corroborated Claude ShipIt process was not detected")
	}
	if !cleanProcessTableMatchesOwner(
		"/Applications/Autodesk Fusion.app/Contents/MacOS/AcCoreConsole",
		"com.autodesk.AcCoreConsole",
	) {
		t.Fatal("the corroborated Autodesk helper was not detected")
	}
}

func TestCleanNamedCacheOwnerDoesNotUseGenericCamelCaseWords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "Library", "Caches", "GeoServices", "map.cache")
	programs := cleanNamedCacheOwnerPrograms(path)
	if cleanProcessTableMentionsAny("/usr/libexec/example-services-helper", programs) {
		t.Fatalf("generic services process matched GeoServices via %#v", programs)
	}
}

func TestCleanProcessGuardsCoverActiveDeveloperTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tests := []struct {
		path    string
		command string
		family  string
	}{
		{
			path:    filepath.Join(home, ".cargo", "registry", "cache", "crate.crate"),
			command: "/usr/local/bin/cargo build",
			family:  "Rust",
		},
		{
			path:    filepath.Join(home, ".gradle", "caches", "modules-2"),
			command: "org.gradle.launcher.daemon.bootstrap.GradleDaemon",
			family:  "Gradle",
		},
		{
			path:    filepath.Join(home, "Library", "pnpm", "store", "v10"),
			command: "/usr/local/bin/node /opt/pnpm/dist/pnpm.cjs install",
			family:  "pnpm/Corepack",
		},
		{
			path:    filepath.Join(home, "Library", "Caches", "node", "corepack", "v1"),
			command: "/usr/local/bin/node /opt/corepack/dist/corepack.cjs pnpm",
			family:  "pnpm/Corepack",
		},
		{
			path:    filepath.Join(home, "Library", "Developer", "Xcode", "DerivedData", "Project"),
			command: "/usr/bin/xcodebuild -scheme Project",
			family:  "Xcode",
		},
	}

	for _, test := range tests {
		family, programs := cleanProcessGuardForPath(test.path)
		if family != test.family {
			t.Errorf("guard family for %q = %q, want %q", test.path, family, test.family)
			continue
		}
		if !cleanProcessTableMentionsAny(test.command, programs) {
			t.Errorf("guard %q did not match command %q via %#v", family, test.command, programs)
		}
	}

	_, pnpmPrograms := cleanProcessGuardForPath(filepath.Join(home, "Library", "pnpm", "store", "v10"))
	if cleanProcessTableMentionsAny("cat /tmp/pnpm-lock.yaml", pnpmPrograms) {
		t.Fatal("a pnpm lockfile mention was mistaken for a running pnpm command")
	}
}

func TestResolvedCleanToolHomeRejectsUnsafeEnvironmentValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fallback := filepath.Join(home, ".cargo")
	valid := filepath.Join(home, ".local", "share", "mise", "cargo")
	if got := resolvedCleanToolHome(valid, fallback); got != valid {
		t.Fatalf("valid absolute tool home = %q, want %q", got, valid)
	}

	for _, value := range []string{
		"relative/cargo",
		home + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "outside",
		filepath.Join(home, "cargo[old]"),
		filepath.Join(home, "cargo\nold"),
		string(os.PathSeparator),
		"/usr/local/cargo",
		filepath.Join(home, ".Trash", "cargo"),
	} {
		if got := resolvedCleanToolHome(value, fallback); got != fallback {
			t.Errorf("unsafe tool home %q resolved to %q, want fallback %q", value, got, fallback)
		}
	}
}

func TestRunCleanHonorsRelocatedRustHomesAndPreservesInstalledState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cargoHome := filepath.Join(home, ".local", "share", "mise", "cargo")
	rustupHome := filepath.Join(home, ".local", "share", "mise", "rustup")
	t.Setenv("CARGO_HOME", cargoHome)
	t.Setenv("RUSTUP_HOME", rustupHome)

	cleanable := []string{
		filepath.Join(cargoHome, "registry", "cache", "index-hash", "crate.crate"),
		filepath.Join(cargoHome, "registry", "src", "index-hash", "crate", "src.rs"),
		filepath.Join(cargoHome, "git", "checkouts", "repo", "HEAD"),
		filepath.Join(rustupHome, "downloads", "archive.tar.xz"),
		filepath.Join(rustupHome, "toolchains", "stable", "share", "doc", "book", "index.html"),
	}
	preserved := []string{
		filepath.Join(cargoHome, "registry", "index", "index-hash", "config.json"),
		filepath.Join(cargoHome, "bin", "cargo"),
		filepath.Join(rustupHome, "toolchains", "stable", "bin", "rustc"),
		filepath.Join(home, ".cargo", "registry", "src", "default", "keep.rs"),
	}
	for _, path := range append(append([]string{}, cleanable...), preserved...) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	runner := &cleanProbeRunner{processTables: []string{"/sbin/launchd\n"}}
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg, Runner: runner})
	for _, path := range cleanable {
		if !cleanResultCovers(res, path) {
			t.Errorf("relocated Rust cache missing from targets: %q (targets=%#v, skipped=%#v, failed=%#v)", path, res.Targets, res.Skipped, res.Failed)
		}
	}
	for _, path := range preserved {
		if cleanResultCovers(res, path) {
			t.Errorf("installed or inactive Rust state appeared in targets: %q (targets=%#v)", path, res.Targets)
		}
	}
}

func TestRunCleanSkipsCargoCachesWhileBuildIsActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cargoHome := filepath.Join(home, "mise", "cargo")
	rustupHome := filepath.Join(home, "mise", "rustup")
	t.Setenv("CARGO_HOME", cargoHome)
	t.Setenv("RUSTUP_HOME", rustupHome)
	cargoFiles := []string{
		filepath.Join(cargoHome, "registry", "cache", "index", "crate.crate"),
		filepath.Join(cargoHome, "registry", "src", "index", "crate", "lib.rs"),
		filepath.Join(cargoHome, "git", "checkouts", "repo", "HEAD"),
	}
	rustupDownload := filepath.Join(rustupHome, "downloads", "toolchain.tar.xz")
	for _, path := range append(append([]string{}, cargoFiles...), rustupDownload) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	runner := &cleanProbeRunner{processTables: []string{"/usr/local/bin/cargo build\n"}}
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg, Runner: runner})
	for _, path := range cargoFiles {
		if cleanResultCovers(res, path) {
			t.Errorf("active Cargo cache appeared in targets: %q", path)
		}
	}
	if !cleanResultCovers(res, rustupDownload) {
		t.Fatalf("independent Rustup download cache was hidden: targets=%#v skipped=%#v", res.Targets, res.Skipped)
	}
}

func TestRunCleanRejectsCargoCacheRootThatEscapesToolHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	cargoHome := filepath.Join(home, "custom-cargo")
	t.Setenv("CARGO_HOME", cargoHome)
	outside := filepath.Join(home, "outside-registry-sources")
	outsideFile := filepath.Join(outside, "crate", "private.rs")
	if err := os.MkdirAll(filepath.Dir(outsideFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cargoHome, "registry"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cargoHome, "registry", "src")); err != nil {
		t.Skipf("cannot create symlink fixture: %v", err)
	}

	logicalTarget := filepath.Join(cargoHome, "registry", "src", "crate")
	if reason := cleanToolCacheContainmentReason(logicalTarget); reason != "Rust cache path leaves tool home" {
		t.Fatalf("containment reason = %q, want tool-home escape", reason)
	}
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{
		Config: cfg,
		Runner: &cleanProbeRunner{processTables: []string{"/sbin/launchd\n"}},
	})
	if cleanResultContains(res, logicalTarget) {
		t.Fatalf("escaped Cargo source root appeared in moved targets: %#v", res.Targets)
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("file outside CARGO_HOME was touched: %v", err)
	}
}

func TestRunCleanAllowsContainedCacheBelowSymlinkedCargoHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	physicalHome := filepath.Join(home, "mise", "installs", "rust", "stable", "cargo")
	logicalHome := filepath.Join(home, "cargo-current")
	t.Setenv("CARGO_HOME", logicalHome)
	targetFile := filepath.Join(physicalHome, "registry", "src", "index", "crate", "lib.rs")
	if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetFile, []byte("cache"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physicalHome, logicalHome); err != nil {
		t.Skipf("cannot create symlink fixture: %v", err)
	}

	logicalTarget := filepath.Join(logicalHome, "registry", "src", "index")
	if reason := cleanToolCacheContainmentReason(logicalTarget); reason != "" {
		t.Fatalf("contained symlinked tool home was rejected: %s", reason)
	}
	if err := validateCleanPath(logicalTarget); err != nil {
		t.Fatalf("contained symlinked tool home failed validation: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{
		DryRun: true,
		Config: cfg,
		Runner: &cleanProbeRunner{processTables: []string{"/sbin/launchd\n"}},
	})
	if !cleanResultContains(res, logicalTarget) {
		t.Fatalf("contained relocated Cargo cache missing: targets=%#v skipped=%#v failed=%#v", res.Targets, res.Skipped, res.Failed)
	}
}

func TestCleanWhitelistInventoryIncludesCargoSourceAndGitCaches(t *testing.T) {
	items := CleanWhitelistItems()
	for _, want := range []string{"~/.cargo/registry/src/*", "~/.cargo/git/*"} {
		found := false
		for _, item := range items {
			if item.Pattern == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("clean whitelist inventory missing %q", want)
		}
	}
}

func TestRunCleanSkipsLiveReverseDNSCacheOwner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cache := filepath.Join(home, "Library", "Caches", "com.autodesk.AcCoreConsole")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "cache.bin"), []byte("cache"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	runner := &cleanProbeRunner{processTables: []string{
		"/Applications/Autodesk Fusion.app/Contents/MacOS/AcCoreConsole\n",
	}}
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg, Runner: runner})
	if cleanResultContains(res, cache) {
		t.Fatalf("live cache appeared in targets: %#v", res.Targets)
	}
	if !cleanResultSkippedFor(res, cache, "cache owner is running") {
		t.Fatalf("live cache skip missing: %#v", res.Skipped)
	}
}

func TestRunCleanSkipsLiveSQLiteCacheInDryRunAndRealRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	cache := filepath.Join(home, "Library", "Caches", "SQLiteApp")
	db := filepath.Join(cache, "Cache.db")
	for _, path := range []string{db, db + "-wal", db + "-shm"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	runner := &cleanProbeRunner{processTables: []string{"/sbin/launchd\n"}}
	for _, dryRun := range []bool{true, false} {
		res := RunClean(context.Background(), CleanOptions{DryRun: dryRun, Config: cfg, Runner: runner})
		if cleanResultContains(res, cache) {
			t.Fatalf("live SQLite cache appeared in targets (dry-run=%v): %#v", dryRun, res.Targets)
		}
		if !cleanResultSkippedFor(res, cache, "live SQLite cache") {
			t.Fatalf("live SQLite skip missing (dry-run=%v): %#v", dryRun, res.Skipped)
		}
		if _, err := os.Stat(db); err != nil {
			t.Fatalf("live SQLite database was touched (dry-run=%v): %v", dryRun, err)
		}
	}
}

func TestRunCleanSkipsSQLiteCacheWithOpenHandle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cache := filepath.Join(home, "Library", "Caches", "DatabaseApp")
	db := filepath.Join(cache, "Cache.db")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("cache"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	runner := &cleanProbeRunner{
		processTables: []string{"/sbin/launchd\n"},
		lsofAvailable: true,
		lsofOutput:    CommandOutput{Stdout: "p123\nn" + db + "\n"},
	}
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg, Runner: runner})
	if cleanResultContains(res, cache) {
		t.Fatalf("open SQLite cache appeared in targets: %#v", res.Targets)
	}
	if !cleanResultSkippedFor(res, cache, "live SQLite cache") {
		t.Fatalf("open SQLite skip missing: %#v", res.Skipped)
	}
}

func TestRunCleanRechecksActivityAtTrashBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	cache := filepath.Join(home, "Library", "Caches", "com.vendor.Widget")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "cache.bin"), []byte("cache"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	runner := &cleanProbeRunner{processTables: []string{
		"/sbin/launchd\n",
		"/sbin/launchd\n",
		"/Applications/Widget.app/Contents/MacOS/Widget com.vendor.Widget\n",
	}}
	res := RunClean(context.Background(), CleanOptions{Config: cfg, Runner: runner})
	if runner.psCalls < 3 {
		t.Fatalf("process table calls = %d, want an action-boundary refresh", runner.psCalls)
	}
	if !cleanResultSkippedFor(res, cache, "cache owner is running") {
		t.Fatalf("boundary skip missing: %#v", res.Skipped)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache was moved after its owner started: %v", err)
	}
}

func TestRunCleanRefusesTargetReplacedAtTrashBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BLOOM_TEST_TRASH_DIR", filepath.Join(home, "trash-stub"))
	cache := filepath.Join(home, "Library", "Caches", "ReplaceableCache")
	backup := filepath.Join(home, "original-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "old.bin"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := &cleanProbeRunner{processTables: []string{"/sbin/launchd\n"}}
	runner.onPS = func(call int) {
		if call != 3 {
			return
		}
		if err := os.Rename(cache, backup); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(cache, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cache, "new.bin"), []byte("new"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{Config: cfg, Runner: runner})
	if !cleanResultSkippedFor(res, cache, "path changed during clean") {
		t.Fatalf("replacement skip missing: %#v", res.Skipped)
	}
	for _, path := range []string{filepath.Join(cache, "new.bin"), filepath.Join(backup, "old.bin")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("replacement safety fixture was touched: %s: %v", path, err)
		}
	}
}

func TestRunCleanNeverTargetsDotConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configFile := filepath.Join(home, ".config", "gcloud", "logs", "gcloud.log")
	dropFile := filepath.Join(home, "Library", "Caches", "DropApp", "data.tmp")
	for _, path := range []string{configFile, dropFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg})
	if !cleanResultContains(res, filepath.Dir(dropFile)) {
		t.Fatalf("dry-run targets missing DropApp: %#v", res.Targets)
	}
	if cleanResultCovers(res, configFile) || cleanResultContains(res, filepath.Dir(configFile)) {
		t.Fatalf("~/.config path appeared in clean targets: targets=%#v skipped=%#v", res.Targets, res.Skipped)
	}
}

func TestRunCleanMovesToTrashWithoutPermanentDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	testTrash := filepath.Join(home, "trash-stub")
	t.Setenv("BLOOM_TEST_TRASH_DIR", testTrash)
	dropFile := filepath.Join(home, "Library", "Caches", "DropApp", "data.tmp")
	if err := os.MkdirAll(filepath.Dir(dropFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dropFile, []byte("cache"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	res := RunClean(context.Background(), CleanOptions{Config: cfg})
	if len(res.Failed) > 0 {
		t.Fatalf("clean failed: %#v", res.Failed)
	}
	if _, err := os.Stat(filepath.Dir(dropFile)); !os.IsNotExist(err) {
		t.Fatalf("original cache dir still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(filepath.Join(testTrash, "DropApp", "data.tmp")); err != nil {
		t.Fatalf("cache was not moved into test Trash: %v", err)
	}
}

func TestSetCleanWhitelistPreservesCustomPatterns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = []string{"~/custom-cache/*", "~/Library/Caches/ms-playwright*"}

	if err := SetCleanWhitelist(&cfg, []string{"~/.cache/huggingface*"}); err != nil {
		t.Fatal(err)
	}
	if !containsString(cfg.Clean.Whitelist, "~/.cache/huggingface*") {
		t.Fatalf("selected predefined pattern missing: %#v", cfg.Clean.Whitelist)
	}
	if !containsString(cfg.Clean.Whitelist, "~/custom-cache/*") {
		t.Fatalf("custom pattern was not preserved: %#v", cfg.Clean.Whitelist)
	}
	if containsString(cfg.Clean.Whitelist, "~/Library/Caches/ms-playwright*") {
		t.Fatalf("unselected predefined pattern was preserved: %#v", cfg.Clean.Whitelist)
	}
}

func cleanResultContains(res CleanResult, path string) bool {
	path = filepath.Clean(path)
	for _, target := range res.Targets {
		if filepath.Clean(target.Path) == path {
			return true
		}
	}
	return false
}

func cleanResultCovers(res CleanResult, path string) bool {
	path = filepath.Clean(path)
	for _, target := range res.Targets {
		targetPath := filepath.Clean(target.Path)
		if targetPath == path || strings.HasPrefix(path, targetPath+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func cleanResultSkippedFor(res CleanResult, path, reason string) bool {
	path = filepath.Clean(path)
	for _, skipped := range res.Skipped {
		if filepath.Clean(skipped.Path) == path && skipped.Reason == reason {
			return true
		}
	}
	return false
}
