package bloom

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Clean is modeled after Mole's safety-first cleanup design:
// candidates come from fixed, known cache/log locations; every path is
// validated before it is touched; protected system/user-data locations and the
// user whitelist are enforced per candidate; and Bloom moves items to Trash by
// default instead of permanently deleting them.

const cleanFinderMetadataSentinel = "FINDER_METADATA"

type CleanConfig struct {
	Whitelist []string
}

type CleanItem struct {
	Label    string
	Pattern  string
	Category string
}

type CleanOptions struct {
	DryRun bool
	Config Config
	Runner Runner
}

type CleanTarget struct {
	Path   string
	Label  string
	SizeKB int64
}

type CleanSkip struct {
	Path   string
	Reason string
}

type CleanResult struct {
	DryRun    bool
	Targets   []CleanTarget
	Skipped   []CleanSkip
	Failed    []CleanSkip
	TotalKB   int64
	Whitelist []string
}

type cleanRule struct {
	Label   string
	Pattern string
}

func DefaultCleanWhitelist() []string {
	return []string{
		"~/Library/Caches/ms-playwright*",
		"~/.cache/huggingface*",
		"~/.m2/repository/*",
		"~/.gradle/caches/*",
		"~/.gradle/daemon/*",
		"~/.ollama/models/*",
		"~/Library/Caches/com.nssurge.surge-mac/*",
		"~/Library/Application Support/com.nssurge.surge-mac/*",
		"~/Library/Caches/org.R-project.R/R/renv/*",
		"~/Library/Caches/pypoetry/virtualenvs*",
		"~/Library/Caches/JetBrains*",
		"~/Library/Caches/com.jetbrains.toolbox*",
		"~/Library/Caches/tealdeer/tldr-pages",
		"~/Library/Application Support/JetBrains*",
		"~/Library/Caches/com.apple.finder",
		"~/Library/Mobile Documents*",
		"~/Library/Caches/com.apple.FontRegistry*",
		"~/Library/Caches/com.apple.spotlight*",
		"~/Library/Caches/com.apple.Spotlight*",
		"~/Library/Caches/CloudKit*",
		"~/Library/Caches/PassKit*",
		"~/.Trash",
		cleanFinderMetadataSentinel,
	}
}

func CleanWhitelistItems() []CleanItem {
	return []CleanItem{
		{Label: "Apple Mail cache", Pattern: "~/Library/Caches/com.apple.mail/*", Category: "system_cache"},
		{Label: "Gradle build cache (Android Studio, Gradle projects)", Pattern: "~/.gradle/caches/build-cache-*/*", Category: "ide_cache"},
		{Label: "Gradle daemon processes cache", Pattern: "~/.gradle/daemon/*", Category: "ide_cache"},
		{Label: "Gradle worker cache", Pattern: "~/.gradle/workers/*", Category: "ide_cache"},
		{Label: "Xcode DerivedData (build outputs, indexes)", Pattern: "~/Library/Developer/Xcode/DerivedData/*", Category: "ide_cache"},
		{Label: "Xcode internal cache files", Pattern: "~/Library/Caches/com.apple.dt.Xcode/*", Category: "ide_cache"},
		{Label: "Xcode iOS device support symbols", Pattern: "~/Library/Developer/Xcode/iOS DeviceSupport/*/Symbols/System/Library/Caches/*", Category: "ide_cache"},
		{Label: "Maven local repository (Java dependencies)", Pattern: "~/.m2/repository/*", Category: "ide_cache"},
		{Label: "JetBrains IDEs data (IntelliJ, PyCharm, WebStorm, GoLand)", Pattern: "~/Library/Application Support/JetBrains/*", Category: "ide_cache"},
		{Label: "JetBrains IDEs cache", Pattern: "~/Library/Caches/JetBrains/*", Category: "ide_cache"},
		{Label: "Android Studio cache and indexes", Pattern: "~/Library/Caches/Google/AndroidStudio*/*", Category: "ide_cache"},
		{Label: "Android build cache", Pattern: "~/.android/build-cache/*", Category: "ide_cache"},
		{Label: "VS Code runtime cache", Pattern: "~/Library/Application Support/Code/Cache/*", Category: "ide_cache"},
		{Label: "VS Code extension and update cache", Pattern: "~/Library/Application Support/Code/CachedData/*", Category: "ide_cache"},
		{Label: "VS Code system cache (Cursor, VSCodium)", Pattern: "~/Library/Caches/com.microsoft.VSCode/*", Category: "ide_cache"},
		{Label: "Cursor editor cache", Pattern: "~/Library/Caches/com.todesktop.230313mzl4w4u92/*", Category: "ide_cache"},
		{Label: "Bazel build cache", Pattern: "~/.cache/bazel/*", Category: "compiler_cache"},
		{Label: "Go build cache", Pattern: "~/Library/Caches/go-build/*", Category: "compiler_cache"},
		{Label: "Go module cache", Pattern: "~/go/pkg/mod/*", Category: "compiler_cache"},
		{Label: "Rust Cargo registry cache", Pattern: "~/.cargo/registry/cache/*", Category: "compiler_cache"},
		{Label: "Rust documentation cache", Pattern: "~/.rustup/toolchains/*/share/doc/*", Category: "compiler_cache"},
		{Label: "Rustup toolchain downloads", Pattern: "~/.rustup/downloads/*", Category: "compiler_cache"},
		{Label: "ccache compiler cache", Pattern: "~/.ccache/*", Category: "compiler_cache"},
		{Label: "sccache distributed compiler cache", Pattern: "~/.cache/sccache/*", Category: "compiler_cache"},
		{Label: "SBT Scala build cache", Pattern: "~/.sbt/*", Category: "compiler_cache"},
		{Label: "Ivy dependency cache", Pattern: "~/.ivy2/cache/*", Category: "compiler_cache"},
		{Label: "Turbo monorepo build cache", Pattern: "~/.turbo/*", Category: "compiler_cache"},
		{Label: "Next.js build cache", Pattern: "~/.next/*", Category: "compiler_cache"},
		{Label: "Vite build cache", Pattern: "~/.vite/*", Category: "compiler_cache"},
		{Label: "Webpack build cache", Pattern: "~/.cache/webpack/*", Category: "compiler_cache"},
		{Label: "Parcel bundler cache", Pattern: "~/.parcel-cache/*", Category: "compiler_cache"},
		{Label: "Node-gyp cache", Pattern: "~/.cache/node-gyp/*", Category: "compiler_cache"},
		{Label: "pre-commit hooks cache", Pattern: "~/.cache/pre-commit/*", Category: "compiler_cache"},
		{Label: "Ruff Python linter cache", Pattern: "~/.cache/ruff/*", Category: "compiler_cache"},
		{Label: "MyPy type checker cache", Pattern: "~/.cache/mypy/*", Category: "compiler_cache"},
		{Label: "Pytest test cache", Pattern: "~/.pytest_cache/*", Category: "compiler_cache"},
		{Label: "pyenv download cache", Pattern: "~/.pyenv/cache/*", Category: "compiler_cache"},
		{Label: "Flutter SDK cache", Pattern: "~/.cache/flutter/*", Category: "compiler_cache"},
		{Label: "Swift Package Manager cache", Pattern: "~/.cache/swift-package-manager/*", Category: "compiler_cache"},
		{Label: "Zig compiler cache", Pattern: "~/.cache/zig/*", Category: "compiler_cache"},
		{Label: "Deno cache", Pattern: "~/Library/Caches/deno/*", Category: "compiler_cache"},
		{Label: "Jupyter runtime files", Pattern: "~/.jupyter/runtime/*", Category: "compiler_cache"},
		{Label: "CocoaPods cache (iOS dependencies)", Pattern: "~/Library/Caches/CocoaPods/*", Category: "package_manager"},
		{Label: "npm package cache", Pattern: "~/.npm/_cacache/*", Category: "package_manager"},
		{Label: "pip Python package cache", Pattern: "~/.cache/pip/*", Category: "package_manager"},
		{Label: "uv Python package cache", Pattern: "~/.cache/uv/*", Category: "package_manager"},
		{Label: "R renv global cache (virtual environments)", Pattern: "~/Library/Caches/org.R-project.R/R/renv/*", Category: "package_manager"},
		{Label: "tealdeer tldr pages cache", Pattern: "~/Library/Caches/tealdeer/tldr-pages", Category: "package_manager"},
		{Label: "Homebrew downloaded packages", Pattern: "~/Library/Caches/Homebrew/*", Category: "package_manager"},
		{Label: "Yarn package manager cache", Pattern: "~/.cache/yarn/*", Category: "package_manager"},
		{Label: "pnpm package store", Pattern: "~/Library/pnpm/store/*", Category: "package_manager"},
		{Label: "Composer PHP dependencies cache (legacy)", Pattern: "~/.composer/cache/*", Category: "package_manager"},
		{Label: "Composer PHP dependencies cache", Pattern: "~/Library/Caches/composer/*", Category: "package_manager"},
		{Label: "RubyGems cache", Pattern: "~/.gem/cache/*", Category: "package_manager"},
		{Label: "RubyGems specs cache", Pattern: "~/.gem/specs/*", Category: "package_manager"},
		{Label: "RubyGems per-version cache", Pattern: "~/.gem/ruby/*/cache/*.gem", Category: "package_manager"},
		{Label: "Bundler cache", Pattern: "~/.bundle/cache/*", Category: "package_manager"},
		{Label: "rbenv download cache", Pattern: "~/.rbenv/cache/*", Category: "package_manager"},
		{Label: "Hex package cache", Pattern: "~/.hex/cache/*", Category: "package_manager"},
		{Label: "Cabal package cache", Pattern: "~/.cabal/packages/*", Category: "package_manager"},
		{Label: "OPAM download cache", Pattern: "~/.opam/download-cache/*", Category: "package_manager"},
		{Label: "Conda package metadata/tarball cache", Pattern: "~/.conda/pkgs", Category: "package_manager"},
		{Label: "Anaconda package metadata/tarball cache", Pattern: "~/anaconda3/pkgs", Category: "package_manager"},
		{Label: "Kubernetes client cache", Pattern: "~/.kube/cache/*", Category: "package_manager"},
		{Label: "AWS CLI cache", Pattern: "~/.aws/cli/cache/*", Category: "package_manager"},
		{Label: "Google Cloud CLI logs", Pattern: "~/.config/gcloud/logs/*", Category: "package_manager"},
		{Label: "Azure CLI logs", Pattern: "~/.azure/logs/*", Category: "package_manager"},
		{Label: "Terraform plugin/module cache", Pattern: "~/.cache/terraform/*", Category: "package_manager"},
		{Label: "Prisma cache", Pattern: "~/.cache/prisma/*", Category: "package_manager"},
		{Label: "PyTorch model cache", Pattern: "~/.cache/torch/*", Category: "ai_ml_cache"},
		{Label: "TensorFlow model and dataset cache", Pattern: "~/.cache/tensorflow/*", Category: "ai_ml_cache"},
		{Label: "HuggingFace models and datasets", Pattern: "~/.cache/huggingface/*", Category: "ai_ml_cache"},
		{Label: "Playwright browser binaries", Pattern: "~/Library/Caches/ms-playwright*", Category: "ai_ml_cache"},
		{Label: "Selenium WebDriver binaries", Pattern: "~/.cache/selenium/*", Category: "ai_ml_cache"},
		{Label: "Ollama local AI models", Pattern: "~/.ollama/models/*", Category: "ai_ml_cache"},
		{Label: "Weights & Biases ML experiments cache", Pattern: "~/.cache/wandb/*", Category: "ai_ml_cache"},
		{Label: "Safari web browser cache", Pattern: "~/Library/Caches/com.apple.Safari/*", Category: "browser_cache"},
		{Label: "Chrome browser cache", Pattern: "~/Library/Caches/Google/Chrome/*", Category: "browser_cache"},
		{Label: "Firefox browser cache", Pattern: "~/Library/Caches/Firefox/*", Category: "browser_cache"},
		{Label: "Brave browser cache", Pattern: "~/Library/Caches/BraveSoftware/Brave-Browser/*", Category: "browser_cache"},
		{Label: "Ghostty terminal cache", Pattern: "~/Library/Caches/com.mitchellh.ghostty/*", Category: "app_cache"},
		{Label: "Zed editor cache", Pattern: "~/Library/Caches/Zed/*", Category: "app_cache"},
		{Label: "Zed editor logs", Pattern: "~/Library/Logs/Zed/*", Category: "app_cache"},
		{Label: "Surge proxy cache", Pattern: "~/Library/Caches/com.nssurge.surge-mac/*", Category: "network_tools"},
		{Label: "Surge configuration and data", Pattern: "~/Library/Application Support/com.nssurge.surge-mac/*", Category: "network_tools"},
		{Label: "Docker BuildX cache", Pattern: "~/.docker/buildx/cache/*", Category: "container_cache"},
		{Label: "Podman container cache", Pattern: "~/.local/share/containers/cache/*", Category: "container_cache"},
		{Label: "Podman container temp files", Pattern: "~/.local/share/containers/storage/tmp/*", Category: "container_cache"},
		{Label: "Lima downloaded images", Pattern: "~/Library/Caches/lima/download/by-url-sha256/*", Category: "container_cache"},
		{Label: "Vagrant temp files", Pattern: "~/.vagrant.d/tmp/*", Category: "container_cache"},
		{Label: "Font cache", Pattern: "~/Library/Caches/com.apple.FontRegistry/*", Category: "system_cache"},
		{Label: "Spotlight metadata cache", Pattern: "~/Library/Caches/com.apple.spotlight/*", Category: "system_cache"},
		{Label: "CloudKit cache", Pattern: "~/Library/Caches/CloudKit/*", Category: "system_cache"},
		{Label: "GeoServices cache", Pattern: "~/Library/Caches/GeoServices/*", Category: "system_cache"},
		{Label: "Trash", Pattern: "~/.Trash", Category: "system_cache"},
		{Label: "iOS/iPadOS device firmware (.ipsw) from iTunes/Finder", Pattern: "~/Library/iTunes/*Software Updates/*.ipsw", Category: "system_cache"},
		{Label: "Apple Configurator 2 device firmware (.ipsw)", Pattern: "~/Library/Group Containers/*.group.com.apple.configurator/**/*.ipsw", Category: "system_cache"},
		{Label: "Finder metadata, .DS_Store", Pattern: cleanFinderMetadataSentinel, Category: "system_cache"},
	}
}

func SetCleanWhitelist(cfg *Config, selected []string) error {
	predefined := cleanPredefinedPatterns()
	custom := []string{}
	for _, pattern := range cfg.Clean.Whitelist {
		if pattern == "" {
			continue
		}
		if !cleanPatternInSet(pattern, predefined) {
			custom = append(custom, pattern)
		}
	}
	combined := append([]string{}, selected...)
	combined = append(combined, custom...)
	cfg.Clean.Whitelist = normalizeCleanWhitelist(combined)
	return nil
}

func normalizeCleanWhitelist(patterns []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" || !cleanWhitelistPatternAllowed(pattern) {
			continue
		}
		portable := portableCleanPattern(pattern)
		key := expandedCleanPattern(portable)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, portable)
	}
	return out
}

func cleanPredefinedPatterns() map[string]bool {
	patterns := map[string]bool{}
	for _, item := range CleanWhitelistItems() {
		patterns[expandedCleanPattern(item.Pattern)] = true
	}
	for _, pattern := range DefaultCleanWhitelist() {
		patterns[expandedCleanPattern(pattern)] = true
	}
	return patterns
}

func cleanPatternInSet(pattern string, set map[string]bool) bool {
	return set[expandedCleanPattern(pattern)]
}

func cleanWhitelistPatternAllowed(pattern string) bool {
	if pattern == cleanFinderMetadataSentinel {
		return true
	}
	if hasControlChar(pattern) || strings.Contains(pattern, "//") || hasDotDotComponent(pattern) {
		return false
	}
	expanded := expandedCleanPattern(pattern)
	if !filepath.IsAbs(expanded) {
		return false
	}
	return !isBlockedSystemCleanPath(expanded)
}

func expandedCleanPattern(pattern string) string {
	if pattern == cleanFinderMetadataSentinel {
		return pattern
	}
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(pattern, "~/") && home != "" {
		pattern = filepath.Join(home, pattern[2:])
	} else if pattern == "~" && home != "" {
		pattern = home
	}
	if home != "" {
		pattern = strings.ReplaceAll(pattern, "$HOME", home)
		pattern = strings.ReplaceAll(pattern, "${HOME}", home)
	}
	return filepath.Clean(pattern)
}

func portableCleanPattern(pattern string) string {
	if pattern == cleanFinderMetadataSentinel {
		return pattern
	}
	home, _ := os.UserHomeDir()
	expanded := expandedCleanPattern(pattern)
	if home != "" {
		home = filepath.Clean(home)
		if expanded == home {
			return "~"
		}
		if strings.HasPrefix(expanded, home+string(os.PathSeparator)) {
			return "~" + expanded[len(home):]
		}
	}
	return expanded
}

func RunClean(ctx context.Context, opts CleanOptions) CleanResult {
	whitelist := normalizeCleanWhitelist(opts.Config.Clean.Whitelist)
	res := CleanResult{DryRun: opts.DryRun, Whitelist: whitelist}
	runner := opts.Runner
	if runner == nil {
		runner = OSRunner{}
	}

	candidates := discoverCleanCandidates(whitelist)
	for _, target := range candidates {
		if ctx.Err() != nil {
			res.Failed = append(res.Failed, CleanSkip{Path: target.Path, Reason: ctx.Err().Error()})
			break
		}
		if isTrashCleanPath(target.Path) {
			res.Skipped = append(res.Skipped, CleanSkip{Path: target.Path, Reason: "trash"})
			continue
		}
		if shouldProtectCleanPath(target.Path) {
			res.Skipped = append(res.Skipped, CleanSkip{Path: target.Path, Reason: "protected"})
			continue
		}
		if isCleanPathWhitelisted(target.Path, whitelist) {
			res.Skipped = append(res.Skipped, CleanSkip{Path: target.Path, Reason: "whitelist"})
			continue
		}
		if err := validateCleanPath(target.Path); err != nil {
			res.Skipped = append(res.Skipped, CleanSkip{Path: target.Path, Reason: "invalid: " + err.Error()})
			logCleanOperation("trash", "0", "rejected", target.Path)
			continue
		}
		if shouldSkipOpenIncompleteDownload(ctx, runner, target.Path) {
			res.Skipped = append(res.Skipped, CleanSkip{Path: target.Path, Reason: "open incomplete download"})
			continue
		}

		sizeKB := pathSizeKB(target.Path)
		target.SizeKB = sizeKB
		if opts.DryRun {
			res.Targets = append(res.Targets, target)
			res.TotalKB += sizeKB
			logCleanOperation("trash", cleanLogSize(sizeKB), "dry-run", target.Path)
			continue
		}

		if err := moveCleanPathToTrash(ctx, runner, target.Path); err != nil {
			res.Failed = append(res.Failed, CleanSkip{Path: target.Path, Reason: err.Error()})
			logCleanOperation("trash", cleanLogSize(sizeKB), "error", target.Path)
			continue
		}
		res.Targets = append(res.Targets, target)
		res.TotalKB += sizeKB
		logCleanOperation("trash", cleanLogSize(sizeKB), "ok", target.Path)
	}

	return res
}

func discoverCleanCandidates(whitelist []string) []CleanTarget {
	var candidates []CleanTarget
	for _, rule := range defaultCleanRules() {
		if rule.Pattern == cleanFinderMetadataSentinel {
			if !isCleanPathWhitelisted(cleanFinderMetadataSentinel, whitelist) {
				candidates = append(candidates, findDSStoreTargets()...)
			}
			continue
		}
		paths := expandCleanRulePattern(rule.Pattern)
		for _, path := range paths {
			candidates = append(candidates, CleanTarget{Path: path, Label: rule.Label})
		}
	}
	return normalizeCleanTargets(candidates)
}

func defaultCleanRules() []cleanRule {
	rules := []cleanRule{
		{Label: "User app cache", Pattern: "~/Library/Caches/*"},
		{Label: "User app logs", Pattern: "~/Library/Logs/*"},
		{Label: "Saved application states", Pattern: "~/Library/Saved Application State/*"},
		{Label: "Diagnostic reports", Pattern: "~/Library/Logs/DiagnosticReports/*"},
		{Label: "Diagnostic reports", Pattern: "~/Library/DiagnosticReports/*"},
		{Label: "QuickLook thumbnails", Pattern: "~/Library/Caches/com.apple.QuickLook.thumbnailcache"},
		{Label: "QuickLook cache", Pattern: "~/Library/Caches/Quick Look/*"},
		{Label: "WebKit network cache", Pattern: "~/Library/Caches/com.apple.WebKit.Networking/*"},
		{Label: "Recent items", Pattern: "~/Library/Application Support/com.apple.sharedfilelist/com.apple.LSSharedFileList.RecentApplications.sfl2"},
		{Label: "Recent documents", Pattern: "~/Library/Application Support/com.apple.sharedfilelist/com.apple.LSSharedFileList.RecentDocuments.sfl2"},
		{Label: "Recent servers", Pattern: "~/Library/Application Support/com.apple.sharedfilelist/com.apple.LSSharedFileList.RecentServers.sfl2"},
		{Label: "Recent hosts", Pattern: "~/Library/Application Support/com.apple.sharedfilelist/com.apple.LSSharedFileList.RecentHosts.sfl2"},
		{Label: "Recent items", Pattern: "~/Library/Application Support/com.apple.sharedfilelist/com.apple.LSSharedFileList.RecentApplications.sfl"},
		{Label: "Recent documents", Pattern: "~/Library/Application Support/com.apple.sharedfilelist/com.apple.LSSharedFileList.RecentDocuments.sfl"},
		{Label: "Recent servers", Pattern: "~/Library/Application Support/com.apple.sharedfilelist/com.apple.LSSharedFileList.RecentServers.sfl"},
		{Label: "Recent hosts", Pattern: "~/Library/Application Support/com.apple.sharedfilelist/com.apple.LSSharedFileList.RecentHosts.sfl"},
		{Label: "Safari incomplete downloads", Pattern: "~/Downloads/*.download"},
		{Label: "Chrome incomplete downloads", Pattern: "~/Downloads/*.crdownload"},
		{Label: "Partial incomplete downloads", Pattern: "~/Downloads/*.part"},
		{Label: "Chrome code cache", Pattern: "~/Library/Application Support/Google/Chrome/*/Code Cache/*"},
		{Label: "Chrome GPU cache", Pattern: "~/Library/Application Support/Google/Chrome/*/GPUCache/*"},
		{Label: "Chrome crash reports", Pattern: "~/Library/Application Support/Google/Chrome/Crashpad/completed/*"},
		{Label: "Brave code cache", Pattern: "~/Library/Application Support/BraveSoftware/Brave-Browser/*/Code Cache/*"},
		{Label: "Brave GPU cache", Pattern: "~/Library/Application Support/BraveSoftware/Brave-Browser/*/GPUCache/*"},
		{Label: "Firefox profile cache", Pattern: "~/Library/Application Support/Firefox/Profiles/*/cache2/*"},
		{Label: "VS Code GPU cache", Pattern: "~/Library/Application Support/Code/GPUCache/*"},
		{Label: "VS Code extension cache", Pattern: "~/Library/Application Support/Code/CachedExtensionVSIXs/*"},
		{Label: "Cursor cached data", Pattern: "~/Library/Application Support/Cursor/CachedData/*"},
		{Label: "Cursor GPU cache", Pattern: "~/Library/Application Support/Cursor/GPUCache/*"},
		{Label: "Homebrew cache", Pattern: "~/Library/Caches/Homebrew/*"},
		{Label: "Yarn v1 cache", Pattern: "~/Library/Caches/Yarn/*"},
		{Label: "Bun cache", Pattern: "~/.bun/install/cache/*"},
		{Label: "Corepack cache", Pattern: "~/Library/Caches/node/corepack/*"},
		{Label: "Cargo git cache", Pattern: "~/.cargo/git/*"},
		{Label: "Poetry cache", Pattern: "~/.cache/poetry/*"},
		{Label: "Node-gyp cache", Pattern: "~/.node-gyp/*"},
		{Label: "TypeScript cache", Pattern: "~/.cache/typescript/*"},
		{Label: "Electron cache", Pattern: "~/.cache/electron/*"},
		{Label: "ESLint cache", Pattern: "~/.cache/eslint/*"},
		{Label: "Prettier cache", Pattern: "~/.cache/prettier/*"},
		{Label: "Zsh completion cache", Pattern: "~/.zcompdump*"},
		{Label: "Git config lock", Pattern: "~/.gitconfig.lock"},
		{Label: "Oh My Zsh cache", Pattern: "~/.oh-my-zsh/cache/*"},
	}
	seen := map[string]bool{}
	for _, item := range CleanWhitelistItems() {
		if item.Pattern == cleanFinderMetadataSentinel || isTrashWhitelistPattern(item.Pattern) {
			continue
		}
		if !seen[item.Pattern] {
			rules = append(rules, cleanRule{Label: item.Label, Pattern: item.Pattern})
			seen[item.Pattern] = true
		}
	}
	return rules
}

func expandCleanRulePattern(pattern string) []string {
	expanded := expandedCleanPattern(pattern)
	if !hasGlobMeta(expanded) {
		if _, err := os.Lstat(expanded); err == nil {
			return []string{expanded}
		}
		return nil
	}
	if strings.Contains(expanded, "**") {
		return recursiveGlob(expanded)
	}
	matches, err := filepath.Glob(expanded)
	if err != nil {
		return nil
	}
	return existingCleanPaths(matches)
}

func recursiveGlob(pattern string) []string {
	root := staticGlobRoot(pattern)
	if root == "" {
		return nil
	}
	var matches []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(matches) >= 2000 {
			return filepath.SkipAll
		}
		if shellPatternMatch(pattern, path) {
			matches = append(matches, path)
		}
		if d.IsDir() {
			if isTrashCleanPath(path) {
				return filepath.SkipDir
			}
			if path != root && shouldProtectCleanPath(path) {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return existingCleanPaths(matches)
}

func staticGlobRoot(pattern string) string {
	idx := strings.IndexAny(pattern, "*?[")
	if idx < 0 {
		return pattern
	}
	root := filepath.Dir(pattern[:idx])
	if root == "." || root == string(os.PathSeparator) {
		return ""
	}
	return filepath.Clean(root)
}

func existingCleanPaths(paths []string) []string {
	out := paths[:0]
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			out = append(out, filepath.Clean(path))
		}
	}
	return out
}

func normalizeCleanTargets(targets []CleanTarget) []CleanTarget {
	sort.SliceStable(targets, func(i, j int) bool {
		if len(targets[i].Path) == len(targets[j].Path) {
			return targets[i].Path < targets[j].Path
		}
		return len(targets[i].Path) < len(targets[j].Path)
	})

	seenIdentity := map[string]bool{}
	seenPath := map[string]bool{}
	out := make([]CleanTarget, 0, len(targets))
	for _, target := range targets {
		path := filepath.Clean(target.Path)
		if path == "" || seenPath[path] {
			continue
		}
		seenPath[path] = true
		identity := cleanPathIdentity(path)
		if identity != "" && seenIdentity[identity] {
			continue
		}
		isChild := false
		for _, kept := range out {
			if path == kept.Path || strings.HasPrefix(path, kept.Path+string(os.PathSeparator)) {
				isChild = true
				break
			}
		}
		if isChild {
			continue
		}
		if identity != "" {
			seenIdentity[identity] = true
		}
		target.Path = path
		out = append(out, target)
	}
	return out
}

func cleanPathIdentity(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		return ""
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", uint64(st.Dev), uint64(st.Ino))
	}
	return "path:" + path
}

func findDSStoreTargets() []CleanTarget {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	excluded := []string{
		filepath.Join(home, "Library", "Application Support", "MobileSync"),
		filepath.Join(home, "Library", "Developer"),
		filepath.Join(home, ".Trash"),
		filepath.Join(home, "node_modules"),
		filepath.Join(home, ".git"),
		filepath.Join(home, "Library", "Caches"),
	}
	var targets []CleanTarget
	rootDepth := pathDepth(home)
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(targets) >= 500 {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path != home && pathDepth(path)-rootDepth >= 5 {
				return filepath.SkipDir
			}
			if path != home && shouldProtectCleanPath(path) {
				return filepath.SkipDir
			}
			for _, excludedPath := range excluded {
				if path == excludedPath || strings.HasPrefix(path, excludedPath+string(os.PathSeparator)) {
					return filepath.SkipDir
				}
			}
			if isTrashCleanPath(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Base(path) == ".DS_Store" {
			targets = append(targets, CleanTarget{Path: path, Label: "Home directory, .DS_Store"})
		}
		return nil
	})
	return targets
}

func pathDepth(path string) int {
	path = filepath.Clean(path)
	if path == string(os.PathSeparator) {
		return 0
	}
	return len(strings.Split(strings.Trim(path, string(os.PathSeparator)), string(os.PathSeparator)))
}

func validateCleanPath(path string) error {
	if path == "" {
		return errors.New("empty path")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}
	if hasDotDotComponent(path) {
		return fmt.Errorf("path traversal not allowed: %s", path)
	}
	if hasControlChar(path) {
		return fmt.Errorf("control characters not allowed: %s", path)
	}
	cleaned := filepath.Clean(path)
	if err := validateCleanSymlinkTarget(cleaned); err != nil {
		return err
	}
	variants := resolvedCleanPathVariants(cleaned)
	for _, candidate := range variants {
		if isTrashCleanPath(candidate) {
			return fmt.Errorf("refusing to clean Trash: %s", candidate)
		}
		if shouldProtectCleanPath(candidate) {
			return fmt.Errorf("protected path: %s", candidate)
		}
		if isAllowedSystemCleanException(candidate) {
			continue
		}
		if isBlockedSystemCleanPath(candidate) {
			return fmt.Errorf("critical system path: %s", candidate)
		}
	}
	if len(variants) > 1 && !isAllowedCleanCanonicalization(variants[0], variants[1]) {
		return fmt.Errorf("refusing redirected clean path: %s -> %s", variants[0], variants[1])
	}
	return nil
}

func validateTrashMovePath(path string) error {
	if path == "" {
		return errors.New("empty path")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}
	if hasDotDotComponent(path) {
		return fmt.Errorf("path traversal not allowed: %s", path)
	}
	if hasControlChar(path) {
		return fmt.Errorf("control characters not allowed: %s", path)
	}
	cleaned := filepath.Clean(path)
	if err := validateCleanSymlinkTarget(cleaned); err != nil {
		return err
	}
	variants := resolvedCleanPathVariants(cleaned)
	for _, candidate := range variants {
		if isTrashCleanPath(candidate) {
			return fmt.Errorf("refusing to move Trash to Trash: %s", candidate)
		}
		if isBlockedSystemCleanPath(candidate) {
			return fmt.Errorf("critical system path: %s", candidate)
		}
	}
	if len(variants) > 1 && !isAllowedCleanCanonicalization(variants[0], variants[1]) {
		return fmt.Errorf("refusing redirected trash path: %s -> %s", variants[0], variants[1])
	}
	return nil
}

func resolvedCleanPathVariants(path string) []string {
	cleaned := filepath.Clean(path)
	variants := []string{cleaned}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil || resolved == "" {
		return variants
	}
	resolved = filepath.Clean(resolved)
	if resolved != cleaned {
		variants = append(variants, resolved)
	}
	return variants
}

func isAllowedCleanCanonicalization(raw, resolved string) bool {
	raw = filepath.Clean(raw)
	resolved = filepath.Clean(resolved)
	if raw == resolved {
		return true
	}
	if sameCleanPathRelativeToHome(raw, resolved) {
		return true
	}
	return sameCleanPathRelativeToSystemCanonicalRoot(raw, resolved)
}

func sameCleanPathRelativeToHome(raw, resolved string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	rawHome := filepath.Clean(home)
	resolvedHome := rawHome
	if evaluated, err := filepath.EvalSymlinks(rawHome); err == nil && evaluated != "" {
		resolvedHome = filepath.Clean(evaluated)
	}
	rawRel, rawOK := cleanRelUnder(raw, rawHome)
	resolvedRel, resolvedOK := cleanRelUnder(resolved, resolvedHome)
	return rawOK && resolvedOK && rawRel == resolvedRel
}

func cleanRelUnder(path, root string) (string, bool) {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if root == "" || root == string(os.PathSeparator) {
		return "", false
	}
	if path == root {
		return ".", true
	}
	prefix := root + string(os.PathSeparator)
	if strings.HasPrefix(path, prefix) {
		return path[len(prefix):], true
	}
	return "", false
}

func sameCleanPathRelativeToSystemCanonicalRoot(raw, resolved string) bool {
	pairs := [][2]string{
		{"/tmp", "/private/tmp"},
		{"/var/tmp", "/private/var/tmp"},
		{"/var/log", "/private/var/log"},
		{"/var/folders", "/private/var/folders"},
		{"/var/db/diagnostics", "/private/var/db/diagnostics"},
		{"/var/db/DiagnosticPipeline", "/private/var/db/DiagnosticPipeline"},
		{"/var/db/powerlog", "/private/var/db/powerlog"},
		{"/var/db/reportmemoryexception", "/private/var/db/reportmemoryexception"},
		{"/var/db/receipts", "/private/var/db/receipts"},
	}
	for _, pair := range pairs {
		if sameRelUnderRootPair(raw, resolved, pair[0], pair[1]) || sameRelUnderRootPair(raw, resolved, pair[1], pair[0]) {
			return true
		}
	}
	return false
}

func sameRelUnderRootPair(raw, resolved, rawRoot, resolvedRoot string) bool {
	rawRel, rawOK := cleanRelUnder(raw, rawRoot)
	resolvedRel, resolvedOK := cleanRelUnder(resolved, resolvedRoot)
	return rawOK && resolvedOK && rawRel == resolvedRel
}

func validateCleanSymlinkTarget(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("cannot read symlink: %s", path)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	target = filepath.Clean(target)
	if resolved, err := filepath.EvalSymlinks(target); err == nil && resolved != "" {
		target = filepath.Clean(resolved)
	}
	if isBlockedSymlinkTarget(target) || shouldProtectCleanPath(target) || isTrashCleanPath(target) {
		return fmt.Errorf("symlink points to protected path: %s -> %s", path, target)
	}
	return nil
}

func isBlockedSymlinkTarget(path string) bool {
	p := filepath.Clean(path)
	blocked := []string{"/", "/System", "/bin", "/sbin", "/usr", "/etc", "/private/etc", "/Library/Extensions"}
	for _, prefix := range blocked {
		if p == prefix || strings.HasPrefix(p, prefix+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func isAllowedSystemCleanException(path string) bool {
	p := filepath.Clean(path)
	exceptions := []string{
		"/System/Library/Caches/com.apple.coresymbolicationd/data",
		"/var/tmp",
		"/var/log",
		"/var/folders",
		"/var/db/diagnostics",
		"/var/db/DiagnosticPipeline",
		"/var/db/powerlog",
		"/var/db/reportmemoryexception",
		"/private/tmp",
		"/private/var/tmp",
		"/private/var/log",
		"/private/var/folders",
		"/private/var/db/diagnostics",
		"/private/var/db/DiagnosticPipeline",
		"/private/var/db/powerlog",
		"/private/var/db/reportmemoryexception",
	}
	for _, prefix := range exceptions {
		if p == prefix || strings.HasPrefix(p, prefix+string(os.PathSeparator)) {
			return true
		}
	}
	if shellPatternMatch("/var/db/receipts/*.bom", p) || shellPatternMatch("/var/db/receipts/*.plist", p) ||
		shellPatternMatch("/private/var/db/receipts/*.bom", p) || shellPatternMatch("/private/var/db/receipts/*.plist", p) {
		return true
	}
	return false
}

func isBlockedSystemCleanPath(path string) bool {
	p := filepath.Clean(path)
	if isAllowedSystemCleanException(p) {
		return false
	}
	blocked := []string{
		"/", "/bin", "/sbin", "/usr", "/usr/bin", "/usr/sbin", "/usr/lib",
		"/System", "/Library/Extensions", "/private", "/etc", "/private/etc",
		"/var", "/var/db", "/private/var", "/private/var/db",
	}
	for _, prefix := range blocked {
		if p == prefix || strings.HasPrefix(p, prefix+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func shouldProtectCleanPath(path string) bool {
	if path == "" {
		return false
	}
	p := filepath.Clean(path)
	if isTrashCleanPath(p) {
		return true
	}
	lower := strings.ToLower(p)
	keywords := []string{
		"systemsettings", "system settings", "systempreferences", "system preferences",
		"controlcenter", "control center", "com.apple.settings", "com.apple.notes",
		"com.apple.mail", "apple mail", "mobile documents", "cloudkit", "clouddocs", "icloud",
		"apple notes", "com.apple.messages", "imessage", "photoslibrary",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	if cleanPathContainsProtectedComponent(p) {
		return true
	}
	if bundleID, containerCache := cleanContainerBundleID(p); bundleID != "" && !containerCache && shouldProtectCleanData(bundleID) {
		return true
	}
	for _, pattern := range protectedCleanPathPatterns {
		if shellPatternMatch(pattern, p) {
			return true
		}
	}
	for _, pattern := range protectedCleanBundlePatterns {
		if shellPatternMatch(pattern, p) {
			return true
		}
	}
	return shouldProtectCleanData(filepath.Base(p))
}

func cleanPathContainsProtectedComponent(path string) bool {
	for _, component := range strings.Split(filepath.Clean(path), string(os.PathSeparator)) {
		component = strings.TrimSpace(component)
		if component == "" {
			continue
		}
		if shouldProtectCleanData(component) {
			return true
		}
	}
	return false
}

func cleanContainerBundleID(path string) (string, bool) {
	for _, marker := range []string{"/Library/Containers/", "/Library/Group Containers/"} {
		idx := strings.Index(path, marker)
		if idx < 0 {
			continue
		}
		rest := path[idx+len(marker):]
		bundleID := rest
		if slash := strings.IndexRune(rest, os.PathSeparator); slash >= 0 {
			bundleID = rest[:slash]
		}
		cache := strings.Contains(path, "/Data/Library/Caches/") || strings.Contains(path, "/Data/tmp/")
		return bundleID, cache
	}
	return "", false
}

func shouldProtectCleanData(token string) bool {
	if token == "" {
		return false
	}
	patterns := []string{
		"com.apple.*", "loginwindow", "dock", "systempreferences", "finder", "safari", "org.cups.*",
		"backgroundtaskmanagement*", "keychain*", "security*", "bluetooth*", "wifi*", "network*", "tcc",
		"notification*", "accessibility*", "universalaccess*", "HIToolbox*",
		"*inputmethod*", "*InputMethod*", "*IME", "textinput*", "TextInput*",
		"keyboard*", "Keyboard*", "inputsource*", "InputSource*", "keylayout*", "KeyLayout*",
		"GlobalPreferences", ".GlobalPreferences", "org.pqrs.Karabiner*",
		"com.1password.*", "com.agilebits.*", "com.lastpass.*", "com.dashlane.*", "com.bitwarden.*",
		"com.dropbox.*", "com.getdropbox.*", "*dropbox*", "com.google.GoogleDrive", "*GoogleDrive*", "com.microsoft.OneDrive*", "*OneDrive*",
		"com.jetbrains.*", "JetBrains*", "com.microsoft.*", "com.visualstudio.*",
		"com.sublimetext.*", "com.sublimehq.*", "Cursor", "Claude", "com.anthropic.claude*", "ChatGPT", "com.openai.codex", "Codex", "codex-runtimes", "Ollama", "com.ollama.ollama", "com.lmstudio.lmstudio", "LM Studio", "com.exafunction.windsurf",
		"com.clash.app", "com.nssurge.*", "com.v2ray.*", "com.clash.*", "ClashX*", "Surge*", "Shadowrocket*", "Quantumult*", "mihomo*", "*openvpn*", "*OpenVPN*",
		"clash-*", "Clash-*", "*-clash", "*-Clash", "clash.*", "Clash.*", "clash_*", "*clash-verge*", "*Clash-Verge*", "clashverge*", "ClashVerge*",
		"*nordvpn*", "*expressvpn*", "*protonvpn*", "*surfshark*", "*windscribe*", "*mullvad*", "*ShadowsocksX-NG*", "*v2box*", "*nekoray*", "*sing-box*", "*hiddify*", "*loon*", "*Loon*", "*zerotier*", "com.zerotier.*", "*cloudflare*warp*", "org.amnezia.*",
		"com.docker.*", "com.getpostman.*", "com.insomnia.*",
		"*spotify*", "com.spotify.*", "*backblaze*", "com.displaylink.*",
		"com.tencent.*", "com.sogou.*", "com.baidu.*", "com.googlecode.*", "im.rime.*",
	}
	for _, pattern := range patterns {
		if shellPatternMatch(pattern, token) {
			return true
		}
	}
	return false
}

var protectedCleanPathPatterns = []string{
	"*com.apple.systempreferences.cache*",
	"*com.apple.Settings.cache*",
	"*com.apple.controlcenter.cache*",
	"*com.apple.finder.cache*",
	"*com.apple.dock.cache*",
	"*/Library/Containers/com.apple.Settings*",
	"*/Library/Containers/com.apple.SystemSettings*",
	"*/Library/Containers/com.apple.controlcenter*",
	"*/Library/Group Containers/com.apple.systempreferences*",
	"*/Library/Group Containers/com.apple.Settings*",
	"*/Library/Group Containers/group.com.apple.notes*",
	"*/com.apple.sharedfilelist/*com.apple.Settings*",
	"*/com.apple.sharedfilelist/*com.apple.SystemSettings*",
	"*/com.apple.sharedfilelist/*systempreferences*",
	"*com.apple.Settings*",
	"*com.apple.SystemSettings*",
	"*com.apple.controlcenter*",
	"*com.apple.finder*",
	"*com.apple.dock*",
	"*/Library/Preferences/com.apple.dock.plist",
	"*/Library/Preferences/com.apple.finder.plist",
	"*/Library/Logs/bloom",
	"*/Library/Logs/bloom/*",
	"*/Library/Logs/mole",
	"*/Library/Logs/mole/*",
	"*/ByHost/com.apple.bluetooth.*",
	"*/ByHost/com.apple.wifi.*",
	"*/Library/Preferences/com.apple.networkextension*.plist",
	"*/Library/Application Support/CloudDocs*",
	"*/Library/Application Support/com.apple.CloudDocs*",
	"*/Library/Application Support/CloudKit*",
	"*/Library/Application Support/MobileSync",
	"*/Library/Application Support/MobileSync/*",
	"*/Library/Application Support/Spotify",
	"*/Library/Application Support/Spotify/*",
	"*/Library/Mobile Documents*",
	"*/Mobile Documents*",
	"*/Library/Accounts",
	"*/Library/Accounts/*",
	"*/Library/Keychains",
	"*/Library/Keychains/*",
	"*/Library/Cookies",
	"*/Library/Cookies/*",
	"*/Library/Safari",
	"*/Library/Safari/*",
	"*/Library/Mail",
	"*/Library/Mail/*",
	"*/Library/Mail Downloads",
	"*/Library/Mail Downloads/*",
	"*/Library/Containers/com.apple.mail*",
	"*/Library/Containers/com.apple.Mail*",
	"*/Library/Group Containers/group.com.apple.mail*",
	"*/Library/Group Containers/*.group.com.apple.mail*",
	"*/Library/Containers/com.apple.Notes*",
	"*/Library/Containers/com.apple.notes*",
	"*/Library/Notes",
	"*/Library/Notes/*",
	"*/Library/LaunchAgents/*.plist",
	"*/Library/LaunchAgents",
	"*/Library/LaunchAgents/*",
	"*/Library/LaunchDaemons",
	"*/Library/LaunchDaemons/*",
	"*/Library/Calendars",
	"*/Library/Calendars/*",
	"*/Library/Contacts",
	"*/Library/Contacts/*",
	"*/Library/Application Support/AddressBook",
	"*/Library/Application Support/AddressBook/*",
	"*/Library/Messages",
	"*/Library/Messages/*",
	"*/Library/Reminders",
	"*/Library/Reminders/*",
	"*/Library/Containers/com.apple.iChat*",
	"*/Library/Containers/com.apple.MobileSMS*",
	"*/Library/Group Containers/group.com.apple.messages*",
	"*/Library/Group Containers/group.com.apple.reminders*",
	"*/Library/Photos",
	"*/Library/Photos/*",
	"*/Pictures/*.photoslibrary",
	"*/Pictures/*.photoslibrary/*",
	"*/Library/Application Support/Google/Chrome",
	"*/Library/Application Support/Google/Chrome/*",
	"*/Library/Application Support/BraveSoftware/Brave-Browser",
	"*/Library/Application Support/BraveSoftware/Brave-Browser/*",
	"*/Library/Application Support/Firefox/Profiles",
	"*/Library/Application Support/Firefox/Profiles/*",
	"*/Library/Application Support/Google/Chrome/*/History*",
	"*/Library/Application Support/Google/Chrome/*/Cookies*",
	"*/Library/Application Support/BraveSoftware/Brave-Browser/*/History*",
	"*/Library/Application Support/BraveSoftware/Brave-Browser/*/Cookies*",
	"*/Library/Application Support/Firefox/Profiles/*/places.sqlite*",
	"*/Library/Application Support/Firefox/Profiles/*/cookies.sqlite*",
	"/Library/Audio/Plug-Ins/Components",
	"/Library/Audio/Plug-Ins/Components/*",
	"/Library/Audio/Plug-Ins/VST",
	"/Library/Audio/Plug-Ins/VST/*",
	"/Library/Audio/Plug-Ins/VST3",
	"/Library/Audio/Plug-Ins/VST3/*",
	"/Library/Application Support/iZotope",
	"/Library/Application Support/iZotope/*",
	"*/Library/Application Support/iZotope",
	"*/Library/Application Support/iZotope/*",
	"/Library/Application Support/LaserSoft Imaging",
	"/Library/Application Support/LaserSoft Imaging/*",
	"*/Library/Preferences/com.native-instruments*",
	"*/Library/Preferences/com.avid.mediacomposer*.plist",
	"*/Library/Preferences/com.fabfilter.*.[0-9].plist",
	"*/Library/Preferences/com.fabfilter.*.[0-9][0-9].plist",
	"*/Library/Preferences/com.paceap.*.plist",
	"/private/var/folders/*/C/com.native-instruments*",
	"/private/var/folders/*/C/com.avid.mediacomposer*",
	"/private/var/folders/*/C/com.paceap.eden.iLokLicenseManager*",
	"*/Library/Caches/ms-playwright",
	"*/Library/Caches/ms-playwright/*",
	"*/Library/Caches/app.cotypist.Cotypist",
	"*/Library/Caches/app.cotypist.Cotypist/*",
	"*/Library/Caches/com.displaylink.DisplayLinkUserAgent",
	"*/Library/Caches/com.displaylink.DisplayLinkUserAgent/*",
	"*/Library/Caches/com.lasersoft-imaging.SilverFast9",
	"*/Library/Caches/com.lasersoft-imaging.SilverFast9/*",
	"*/Library/Caches/com.lasersoft-imaging.SilverFast-9-Installer",
	"*/Library/Caches/com.lasersoft-imaging.SilverFast-9-Installer/*",
	"*/Library/Caches/Adobe *",
	"*/Library/Caches/* Adobe*",
	"*/Library/Caches/com.apple.containermanagerd",
	"*/Library/Caches/com.apple.containermanagerd/*",
	"*/Library/Caches/com.apple.homed",
	"*/Library/Caches/com.apple.homed/*",
	"*/Library/Caches/com.apple.ap.adprivacyd",
	"*/Library/Caches/com.apple.ap.adprivacyd/*",
	"*/Library/Caches/FamilyCircle",
	"*/Library/Caches/FamilyCircle/*",
	"*/Library/Caches/com.apple.HomeKit",
	"*/Library/Caches/com.apple.HomeKit/*",
	"*/Library/Caches/PassKit",
	"*/Library/Caches/PassKit/*",
	"*/Library/Caches/com.apple.WorkflowKit.BackgroundShortcutRunner.ShortcutsSandboxCache",
	"*/Library/Caches/com.apple.WorkflowKit.BackgroundShortcutRunner.ShortcutsSandboxCache/*",
	"*/Library/Caches/com.apple.siriactionsd.ShortcutsSandboxCache",
	"*/Library/Caches/com.apple.siriactionsd.ShortcutsSandboxCache/*",
	"*com.apple.coreaudio*",
	"*com.apple.audio.*",
	"*coreaudiod*",
}

var protectedCleanBundlePatterns = []string{
	"*Keychain*", "*keychain*", "*1Password*", "*Bitwarden*", "*LastPass*", "*Dashlane*",
	"*Tailscale*", "*WireGuard*", "*Shadowsocks*", "*ShadowsocksX-NG*", "*V2Ray*", "*NetworkExtension*",
	"mihomo*", "*openvpn*", "*OpenVPN*", "*nordvpn*", "*expressvpn*", "*protonvpn*", "*surfshark*", "*windscribe*", "*mullvad*",
	"*v2box*", "*nekoray*", "*sing-box*", "*hiddify*", "*loon*", "*Loon*", "*zerotier*", "com.zerotier.*", "*cloudflare*warp*", "org.amnezia.*",
	"*Mobile Documents*", "*CloudKit*", "*CloudDocs*", "*iCloud*",
	"*dropbox*", "*GoogleDrive*", "*OneDrive*", "*backblaze*",
	"*Aerial.saver*", "*Fliqlo*",
	"*group.com.apple.notes*", "*com.apple.Notes*", "*com.apple.notes*",
	"*com.apple.mail*", "*Apple Mail*", "*com.apple.LaunchServices*",
	"*/Library/LaunchAgents/*.plist", "*/Library/LaunchDaemons/*.plist",
}

func isCleanPathWhitelisted(path string, whitelist []string) bool {
	if path == "" || len(whitelist) == 0 {
		return false
	}
	if path == cleanFinderMetadataSentinel {
		for _, pattern := range whitelist {
			if strings.TrimSpace(pattern) == cleanFinderMetadataSentinel {
				return true
			}
		}
		return false
	}
	target := normalizeCleanPathForMatch(path)
	for _, pattern := range whitelist {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if pattern == cleanFinderMetadataSentinel {
			if filepath.Base(target) == ".DS_Store" {
				return true
			}
			continue
		}
		check := normalizeCleanPathForMatch(expandedCleanPattern(pattern))
		hasGlob := hasGlobMeta(check)
		if target == check || shellPatternMatch(check, target) {
			return true
		}
		if strings.HasPrefix(check, target+string(os.PathSeparator)) {
			return true
		}
		if !hasGlob && strings.HasPrefix(target, check+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func normalizeCleanPathForMatch(path string) string {
	path = filepath.Clean(path)
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	return strings.TrimRight(path, string(os.PathSeparator))
}

func isTrashWhitelistPattern(pattern string) bool {
	return isTrashCleanPath(expandedCleanPattern(pattern))
}

func isTrashCleanPath(path string) bool {
	if path == "" || path == cleanFinderMetadataSentinel {
		return false
	}
	p := filepath.Clean(path)
	home, _ := os.UserHomeDir()
	if home != "" {
		trash := filepath.Join(home, ".Trash")
		if p == trash || strings.HasPrefix(p, trash+string(os.PathSeparator)) {
			return true
		}
	}
	for _, part := range strings.Split(p, string(os.PathSeparator)) {
		if part == ".Trash" || part == ".Trashes" {
			return true
		}
	}
	return false
}

func shouldSkipOpenIncompleteDownload(ctx context.Context, runner Runner, path string) bool {
	if !isIncompleteDownloadCleanPath(path) {
		return false
	}
	if _, err := runner.LookPath("lsof"); err != nil {
		return true
	}
	out := runner.Run(ctx, "lsof", path)
	if out.Err == nil {
		return true
	}
	if isLsofNoOpenFiles(out) {
		return false
	}
	return true
}

func isIncompleteDownloadCleanPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(base, ".download") || strings.HasSuffix(base, ".crdownload") || strings.HasSuffix(base, ".part")
}

func isLsofNoOpenFiles(out CommandOutput) bool {
	var exitErr *exec.ExitError
	if !errors.As(out.Err, &exitErr) || exitErr.ExitCode() != 1 {
		return false
	}
	combined := strings.ToLower(out.Combined())
	if strings.Contains(combined, "permission") || strings.Contains(combined, "operation not permitted") {
		return false
	}
	return strings.TrimSpace(out.Stdout) == ""
}

func moveCleanPathToTrash(ctx context.Context, runner Runner, path string) error {
	if err := validateCleanPath(path); err != nil {
		return err
	}
	return movePathToTrash(ctx, runner, path)
}

func movePathToTrash(ctx context.Context, runner Runner, path string) error {
	if err := validateTrashMovePath(path); err != nil {
		return err
	}
	if testTrash := os.Getenv("BLOOM_TEST_TRASH_DIR"); testTrash != "" {
		return movePathIntoTrashDir(path, testTrash)
	}
	if _, err := runner.LookPath("trash"); err == nil {
		if out := runner.Run(ctx, "trash", path); out.Err == nil {
			return nil
		}
	}
	if out := runner.Run(ctx, "/usr/bin/osascript",
		"-e", "on run argv",
		"-e", "set p to POSIX file (item 1 of argv)",
		"-e", "tell application \"Finder\" to delete p",
		"-e", "end run",
		path,
	); out.Err == nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return errors.New("Trash unavailable")
	}
	if err := movePathIntoTrashDir(path, filepath.Join(home, ".Trash")); err != nil {
		return fmt.Errorf("Trash unavailable: %w", err)
	}
	return nil
}

func movePathIntoTrashDir(path, trashDir string) error {
	if info, err := os.Lstat(trashDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Trash is a symlink: %s", trashDir)
	}
	if err := os.MkdirAll(trashDir, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(trashDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Trash is not a normal directory: %s", trashDir)
	}
	_ = os.Chmod(trashDir, 0o700)
	dest, err := uniqueTrashDestination(trashDir, filepath.Base(path))
	if err != nil {
		return err
	}
	return os.Rename(path, dest)
}

func uniqueTrashDestination(trashDir, base string) (string, error) {
	base = strings.ReplaceAll(base, ":", "__")
	if base == "" || base == "." || base == ".." {
		base = "bloom-trash-item"
	}
	dest := filepath.Join(trashDir, base)
	if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
		return dest, nil
	}
	ts := time.Now().Unix()
	for i := 1; i <= 100; i++ {
		dest = filepath.Join(trashDir, fmt.Sprintf("%s.%d.%d", base, ts, i))
		if _, err := os.Lstat(dest); errors.Is(err, os.ErrNotExist) {
			return dest, nil
		}
	}
	return "", fmt.Errorf("could not choose unique Trash destination for %s", base)
}

func logCleanOperation(mode, sizeKB, status, target string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	logFile := filepath.Join(home, "Library", "Logs", "bloom", "clean.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s\t%s\t%s\t%s\t%s\n", time.Now().Format("2006-01-02T15:04:05-0700"), mode, sizeKB, status, target)
}

func cleanLogSize(sizeKB int64) string {
	if sizeKB < 0 {
		return "unknown"
	}
	return strconv.FormatInt(sizeKB, 10)
}

func hasDotDotComponent(path string) bool {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
	for _, part := range parts {
		if part == ".." {
			return true
		}
	}
	return false
}

func hasControlChar(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func hasGlobMeta(value string) bool {
	return strings.ContainsAny(value, "*?[")
}

func shellPatternMatch(pattern, value string) bool {
	re, err := shellPatternRegexp(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func shellPatternRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		case '[':
			end := i + 1
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end < len(pattern) {
				b.WriteString(pattern[i : end+1])
				i = end
			} else {
				b.WriteString(regexp.QuoteMeta(string(ch)))
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}
