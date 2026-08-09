package bloom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const codexStagingMaxAge = 30 * 24 * time.Hour

type cleanSpecialKind uint8

const (
	cleanSpecialCodexSparkle cleanSpecialKind = iota + 1
	cleanSpecialCodexMarketplace
)

const (
	codexEligibilityAge        = "age"
	codexEligibilitySuperseded = "superseded"
)

type cleanSpecialTarget struct {
	kind           cleanSpecialKind
	root           string
	eligibility    string
	installedBuild int64
}

func discoverCodexStagingTargets(ctx context.Context, runner Runner, now time.Time) []CleanTarget {
	if ctx.Err() != nil {
		return nil
	}
	targets := discoverCodexSparkleTargets(ctx, runner, now)
	targets = append(targets, discoverCodexMarketplaceTargets(now)...)
	return normalizeCleanTargets(targets)
}

func discoverCodexSparkleTargets(ctx context.Context, runner Runner, now time.Time) []CleanTarget {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	root := filepath.Join(home, "Library", "Caches", "com.openai.codex", "org.sparkle-project.Sparkle", "Installation")
	if !cleanCodexStagingPathSafe(root, root) {
		return nil
	}
	installedBuild, installedKnown := cleanCodexInstalledBuild(ctx, runner)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	targets := make([]CleanTarget, 0, len(entries))
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if !cleanCodexStagingPathSafe(path, root) || !cleanDirectoryHasEntries(path) {
			continue
		}
		mode, eligible := cleanCodexSparkleEligibility(ctx, runner, path, installedBuild, installedKnown, now)
		if !eligible {
			continue
		}
		targets = append(targets, CleanTarget{
			Path:  path,
			Label: "Codex Desktop stale update staging",
			special: &cleanSpecialTarget{
				kind:           cleanSpecialCodexSparkle,
				root:           root,
				eligibility:    mode,
				installedBuild: installedBuild,
			},
		})
	}
	return targets
}

func discoverCodexMarketplaceTargets(now time.Time) []CleanTarget {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	type marketplaceRoot struct {
		path     string
		prefixes []string
	}
	roots := []marketplaceRoot{
		{
			path:     filepath.Join(home, ".codex", ".tmp", "bundled-marketplaces"),
			prefixes: []string{"openai-bundled.staging-"},
		},
		{
			path:     filepath.Join(home, ".codex", ".tmp", "marketplaces", ".staging"),
			prefixes: []string{"marketplace-upgrade-", "marketplace-add-"},
		},
	}
	var targets []CleanTarget
	for _, root := range roots {
		if !cleanCodexStagingPathSafe(root.path, root.path) {
			continue
		}
		entries, err := os.ReadDir(root.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !hasAnyPrefix(entry.Name(), root.prefixes) {
				continue
			}
			path := filepath.Join(root.path, entry.Name())
			if !cleanCodexStagingPathSafe(path, root.path) || !cleanDirectoryHasEntries(path) || !cleanCodexOlderThan(path, now) {
				continue
			}
			targets = append(targets, CleanTarget{
				Path:  path,
				Label: "Codex marketplace staging",
				special: &cleanSpecialTarget{
					kind:        cleanSpecialCodexMarketplace,
					root:        root.path,
					eligibility: codexEligibilityAge,
				},
			})
		}
	}
	return targets
}

func cleanCodexSparkleEligibility(ctx context.Context, runner Runner, entry string, installedBuild int64, installedKnown bool, now time.Time) (string, bool) {
	if installedKnown {
		if stagedBuild, ok := cleanCodexStagedBuild(ctx, runner, entry); ok {
			if stagedBuild <= installedBuild {
				return codexEligibilitySuperseded, true
			}
			// A newer staged build is a pending update at every age.
			return "", false
		}
	}
	if cleanCodexOlderThan(entry, now) {
		return codexEligibilityAge, true
	}
	return "", false
}

func cleanCodexInstalledBuild(ctx context.Context, runner Runner) (int64, bool) {
	home, _ := os.UserHomeDir()
	return cleanCodexInstalledBuildFromCandidates(ctx, runner, []string{
		"/Applications/Codex.app",
		filepath.Join(home, "Applications", "Codex.app"),
	})
}

func cleanCodexInstalledBuildFromCandidates(ctx context.Context, runner Runner, fixed []string) (int64, bool) {
	if runner == nil {
		runner = OSRunner{}
	}
	seen := map[string]bool{}
	var resolved int64
	known := false
	addCandidate := func(path string) bool {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || seen[path] {
			return true
		}
		seen[path] = true
		build, ok := cleanCodexAppBuild(ctx, runner, path)
		if !ok {
			return true
		}
		if known && resolved != build {
			return false
		}
		resolved = build
		known = true
		return true
	}
	for _, path := range fixed {
		if !addCandidate(path) {
			return 0, false
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	out := runner.Run(probeCtx, "/usr/bin/mdfind", "kMDItemCFBundleIdentifier == 'com.openai.codex'")
	probeErr := probeCtx.Err()
	cancel()
	if out.Err != nil || probeErr != nil {
		return 0, false
	}
	home, _ := os.UserHomeDir()
	cacheRoot := filepath.Join(home, "Library", "Caches")
	for _, line := range strings.Split(out.Stdout, "\n") {
		path := filepath.Clean(strings.TrimSpace(line))
		if path == "." || !strings.EqualFold(filepath.Ext(path), ".app") {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() || cleanPathAtOrBelow(path, cacheRoot) {
			continue
		}
		if !addCandidate(path) {
			return 0, false
		}
	}
	if !known {
		return 0, false
	}
	return resolved, true
}

func cleanCodexAppBuild(ctx context.Context, runner Runner, appPath string) (int64, bool) {
	info, err := os.Stat(appPath)
	if err != nil || !info.IsDir() {
		return 0, false
	}
	plist := filepath.Join(appPath, "Contents", "Info.plist")
	bundleID, ok := cleanCodexPlistValue(ctx, runner, plist, "CFBundleIdentifier")
	if !ok || bundleID != "com.openai.codex" {
		return 0, false
	}
	version, ok := cleanCodexPlistValue(ctx, runner, plist, "CFBundleVersion")
	if !ok || len(version) == 0 || len(version) > 10 {
		return 0, false
	}
	for _, r := range version {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	build, err := strconv.ParseInt(version, 10, 64)
	return build, err == nil
}

func cleanCodexPlistValue(ctx context.Context, runner Runner, plist, key string) (string, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	out := runner.Run(probeCtx, "/usr/libexec/PlistBuddy", "-c", "Print :"+key, plist)
	probeErr := probeCtx.Err()
	cancel()
	if out.Err != nil || probeErr != nil {
		return "", false
	}
	value := strings.TrimSpace(out.Stdout)
	return value, value != ""
}

func cleanCodexStagedBuild(ctx context.Context, runner Runner, entry string) (int64, bool) {
	entries, err := os.ReadDir(entry)
	if err != nil {
		return 0, false
	}
	var resolved int64
	known := false
	for _, child := range entries {
		if !strings.HasSuffix(child.Name(), ".app") {
			continue
		}
		if !child.IsDir() || child.Type()&os.ModeSymlink != 0 {
			return 0, false
		}
		build, ok := cleanCodexAppBuild(ctx, runner, filepath.Join(entry, child.Name()))
		if !ok || known && build != resolved {
			return 0, false
		}
		resolved = build
		known = true
	}
	return resolved, known
}

func cleanTargetActivityReason(ctx context.Context, activity *cleanActivityProbe, target CleanTarget) string {
	if target.special == nil {
		return activity.skipReason(ctx, target.Path)
	}
	if !activity.loaded {
		activity.refresh(ctx)
	}
	return cleanCodexStagingActivityReason(ctx, activity, target)
}

func cleanCodexStagingActivityReason(ctx context.Context, activity *cleanActivityProbe, target CleanTarget) string {
	if reason := cleanCodexStagingEligibilityReason(ctx, activity.runner, target, time.Now()); reason != "" {
		return reason
	}
	if !activity.processKnown {
		return "Codex process state unknown"
	}
	if cleanCodexRuntimeActive(activity.processTable, target.special.kind) {
		return "Codex is running"
	}
	if target.special.kind == cleanSpecialCodexSparkle && cleanCodexSparkleUpdaterActive(activity.processTable) {
		return "Sparkle updater is running"
	}
	if reason := cleanCodexOpenFileReason(ctx, activity.runner, target.special.root); reason != "" {
		return reason
	}
	return cleanCodexStagingEligibilityReason(ctx, activity.runner, target, time.Now())
}

func cleanCodexStagingEligibilityReason(ctx context.Context, runner Runner, target CleanTarget, now time.Time) string {
	meta := target.special
	if meta == nil || !cleanCodexSpecialPathMatches(target.Path, meta) || !cleanCodexStagingPathSafe(target.Path, meta.root) || !cleanDirectoryHasEntries(target.Path) {
		return "Codex staging entry changed"
	}
	switch meta.kind {
	case cleanSpecialCodexMarketplace:
		if !cleanCodexOlderThan(target.Path, now) {
			return "Codex staging entry is no longer stale"
		}
	case cleanSpecialCodexSparkle:
		switch meta.eligibility {
		case codexEligibilityAge:
			if !cleanCodexOlderThan(target.Path, now) {
				return "Codex staging entry is no longer stale"
			}
		case codexEligibilitySuperseded:
			installedNow, ok := cleanCodexInstalledBuild(ctx, runner)
			if !ok || installedNow != meta.installedBuild {
				return "Codex installed build changed"
			}
			stagedNow, ok := cleanCodexStagedBuild(ctx, runner, target.Path)
			if !ok || stagedNow > installedNow {
				return "Codex staged build changed"
			}
		default:
			return "Codex staging eligibility unknown"
		}
	default:
		return "Codex staging target unknown"
	}
	return ""
}

func cleanCodexRuntimeActive(table string, kind cleanSpecialKind) bool {
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		base := filepath.Base(fields[0])
		lower := strings.ToLower(line)
		desktop := base == "Codex" || base == "ChatGPT" || strings.Contains(lower, "/codex.app/") || strings.Contains(lower, "/chatgpt.app/")
		if desktop {
			return true
		}
		if kind == cleanSpecialCodexMarketplace && cleanCommandMentionsProgram(lower, "codex") {
			return true
		}
	}
	return false
}

func cleanCodexSparkleUpdaterActive(table string) bool {
	for _, line := range strings.Split(strings.ToLower(table), "\n") {
		if strings.Contains(line, "org.sparkle-project.sparkle") || strings.Contains(line, "sparkle.framework/") {
			return true
		}
	}
	return false
}

func cleanCodexOpenFileReason(ctx context.Context, runner Runner, root string) string {
	if _, err := runner.LookPath("lsof"); err != nil {
		return "Codex staging open-file state unknown"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	out := runner.Run(probeCtx, "lsof", "-Fn", "+D", root)
	probeErr := probeCtx.Err()
	cancel()
	if probeErr != nil {
		return "Codex staging open-file check timed out"
	}
	if out.Err == nil {
		if strings.Contains(out.Stdout, "\nn/") || strings.HasPrefix(out.Stdout, "n/") {
			return "Codex staging files are open"
		}
		return ""
	}
	if isLsofNoOpenFiles(out) && strings.TrimSpace(out.Stderr) == "" {
		return ""
	}
	return "Codex staging open-file state unknown"
}

func validateCleanTargetPath(target CleanTarget) error {
	if target.special == nil {
		return validateCleanPath(target.Path)
	}
	path := target.Path
	if path == "" || !filepath.IsAbs(path) || hasDotDotComponent(path) || hasControlChar(path) {
		return errors.New("invalid Codex staging path")
	}
	if !cleanCodexSpecialPathMatches(path, target.special) || !cleanCodexStagingPathSafe(path, target.special.root) {
		return errors.New("unsafe Codex staging path")
	}
	if isTrashCleanPath(path) {
		return errors.New("refusing to clean Trash")
	}
	for _, variant := range resolvedCleanPathVariants(path) {
		if isBlockedSystemCleanPath(variant) {
			return fmt.Errorf("critical system path: %s", variant)
		}
	}
	return nil
}

func moveCleanTargetToTrash(ctx context.Context, runner Runner, target CleanTarget) error {
	if target.special == nil {
		return moveCleanPathToTrash(ctx, runner, target.Path, target.identity)
	}
	activity := &cleanActivityProbe{runner: runner}
	activity.refresh(ctx)
	if reason := cleanCodexStagingActivityReason(ctx, activity, target); reason != "" {
		return errors.New(reason)
	}
	if err := validateCleanTargetPath(target); err != nil {
		return err
	}
	return movePathToTrashWithIdentity(ctx, runner, target.Path, target.identity)
}

func cleanCodexSpecialPathMatches(path string, meta *cleanSpecialTarget) bool {
	if meta == nil {
		return false
	}
	rel, ok := cleanRelUnder(path, meta.root)
	if !ok || rel == "." || strings.Contains(rel, string(os.PathSeparator)) {
		return false
	}
	switch meta.kind {
	case cleanSpecialCodexSparkle:
		return true
	case cleanSpecialCodexMarketplace:
		base := filepath.Base(path)
		if strings.HasSuffix(meta.root, filepath.Join(".tmp", "bundled-marketplaces")) {
			return strings.HasPrefix(base, "openai-bundled.staging-")
		}
		return strings.HasPrefix(base, "marketplace-upgrade-") || strings.HasPrefix(base, "marketplace-add-")
	default:
		return false
	}
}

func cleanCodexStagingPathSafe(candidate, root string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || hasControlChar(candidate) || hasDotDotComponent(candidate) {
		return false
	}
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	rootRel, rootUnderHome := cleanRelUnder(root, home)
	candidateRel, candidateUnderHome := cleanRelUnder(candidate, home)
	if !rootUnderHome || rootRel == "." || !candidateUnderHome {
		return false
	}
	if candidate != root {
		rel, ok := cleanRelUnder(candidate, root)
		if !ok || rel == "." || strings.Contains(rel, string(os.PathSeparator)) {
			return false
		}
	}
	for _, rel := range []string{rootRel, candidateRel} {
		probe := filepath.Clean(home)
		for _, component := range strings.Split(rel, string(os.PathSeparator)) {
			if component == "" || component == "." || component == ".." {
				return false
			}
			probe = filepath.Join(probe, component)
			info, err := os.Lstat(probe)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return false
			}
		}
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return false
	}
	candidateInfo, err := os.Stat(candidate)
	if err != nil || !candidateInfo.IsDir() {
		return false
	}
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	physicalCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false
	}
	physicalRoot = filepath.Clean(physicalRoot)
	physicalCandidate = filepath.Clean(physicalCandidate)
	if candidate == root {
		return physicalCandidate == physicalRoot
	}
	return filepath.Dir(physicalCandidate) == physicalRoot
}

func cleanCodexOlderThan(path string, now time.Time) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir() && info.ModTime().Before(now.Add(-codexStagingMaxAge))
}

func cleanDirectoryHasEntries(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
