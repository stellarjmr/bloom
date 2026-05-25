package bloom

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	res := RunClean(context.Background(), CleanOptions{DryRun: true, Config: cfg})
	for _, path := range files {
		if !cleanResultCovers(res, path) {
			t.Fatalf("dry-run targets missing Mole-compatible cache %q: targets=%#v skipped=%#v", path, res.Targets, res.Skipped)
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
