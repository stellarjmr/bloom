package bloom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var autodeskFusionVersionDirName = regexp.MustCompile(`^[0-9a-f]{40}$`)

const autodeskFusionAliasJXA = `ObjC.import("Foundation"); function run(argv) { var url = $.NSURL.fileURLWithPath($(argv[0])); var resolved = $.NSURL.URLByResolvingAliasFileAtURLOptionsError(url, 0, null); if (!resolved) throw new Error("unresolved alias"); return ObjC.unwrap(resolved.path); }`

type autodeskFusionBundleMetadata struct {
	version string
}

func discoverAutodeskFusionTargets(ctx context.Context, runner Runner) []CleanTarget {
	if ctx.Err() != nil {
		return nil
	}
	scanCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	root := autodeskFusionProductionRoot()
	if !autodeskFusionProductionRootTrusted(root) {
		return nil
	}
	currentDir, currentVersion, ok := autodeskFusionCurrentVersion(scanCtx, runner, root)
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	versionDirs := 0
	var targets []CleanTarget
	for _, entry := range entries {
		if scanCtx.Err() != nil {
			return nil
		}
		if !autodeskFusionVersionDirName.MatchString(entry.Name()) {
			continue
		}
		versionDirs++
		if versionDirs > 128 {
			return nil
		}
		path := filepath.Join(root, entry.Name())
		if path == currentDir || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !cleanDirectChildPathSafe(path, root) {
			continue
		}
		metadata, ok := autodeskFusionVersionDirMetadata(scanCtx, runner, root, path)
		if !ok {
			continue
		}
		comparison, comparable := compareAutodeskFusionVersions(metadata.version, currentVersion)
		if !comparable || comparison >= 0 {
			continue
		}
		targets = append(targets, CleanTarget{
			Path:  path,
			Label: "Autodesk Fusion old version",
			special: &cleanSpecialTarget{
				kind:             cleanSpecialAutodeskFusion,
				root:             root,
				currentDir:       currentDir,
				currentVersion:   currentVersion,
				candidateVersion: metadata.version,
			},
		})
	}
	return normalizeCleanTargets(targets)
}

func autodeskFusionProductionRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "Autodesk", "webdeploy", "production")
}

func autodeskFusionProductionRootTrusted(root string) bool {
	if root == "" || filepath.Clean(root) != filepath.Clean(autodeskFusionProductionRoot()) {
		return false
	}
	return cleanDirectChildPathSafe(root, root)
}

func autodeskFusionCurrentVersion(ctx context.Context, runner Runner, root string) (dir, version string, ok bool) {
	dir, ok = autodeskFusionResolveCurrentDir(ctx, runner, root)
	if !ok {
		return "", "", false
	}
	metadata, ok := autodeskFusionVersionDirMetadata(ctx, runner, root, dir)
	if !ok {
		return "", "", false
	}
	dirAfter, ok := autodeskFusionResolveCurrentDir(ctx, runner, root)
	if !ok || dirAfter != dir {
		return "", "", false
	}
	return dir, metadata.version, true
}

func autodeskFusionResolveCurrentDir(ctx context.Context, runner Runner, root string) (string, bool) {
	if runner == nil {
		runner = OSRunner{}
	}
	aliasPath := filepath.Join(root, "Autodesk Fusion.app")
	info, err := os.Lstat(aliasPath)
	if err != nil {
		return "", false
	}

	var resolved string
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err = filepath.EvalSymlinks(aliasPath)
		if err != nil {
			return "", false
		}
	} else {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		out := runner.Run(probeCtx, "/usr/bin/osascript", "-l", "JavaScript", "-e", autodeskFusionAliasJXA, aliasPath)
		probeErr := probeCtx.Err()
		cancel()
		if out.Err != nil || probeErr != nil {
			return "", false
		}
		resolved = strings.TrimSpace(out.Stdout)
		if resolved == "" || hasControlChar(resolved) || hasDotDotComponent(resolved) {
			return "", false
		}
		resolved, err = filepath.EvalSymlinks(resolved)
		if err != nil {
			return "", false
		}
	}
	resolved = filepath.Clean(resolved)
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	physicalRoot = filepath.Clean(physicalRoot)

	var versionName string
	if filepath.Dir(resolved) == physicalRoot && autodeskFusionVersionDirName.MatchString(filepath.Base(resolved)) {
		versionName = filepath.Base(resolved)
	} else if isAutodeskFusionAppName(filepath.Base(resolved)) {
		physicalVersionDir := filepath.Dir(resolved)
		if filepath.Dir(physicalVersionDir) != physicalRoot || !autodeskFusionVersionDirName.MatchString(filepath.Base(physicalVersionDir)) {
			return "", false
		}
		versionName = filepath.Base(physicalVersionDir)
	} else {
		return "", false
	}

	dir := filepath.Join(root, versionName)
	if !cleanDirectChildPathSafe(dir, root) {
		return "", false
	}
	return dir, true
}

func autodeskFusionVersionDirMetadata(ctx context.Context, runner Runner, root, versionDir string) (autodeskFusionBundleMetadata, bool) {
	if runner == nil {
		runner = OSRunner{}
	}
	if !autodeskFusionVersionDirName.MatchString(filepath.Base(versionDir)) || !cleanDirectChildPathSafe(versionDir, root) {
		return autodeskFusionBundleMetadata{}, false
	}

	var appPath string
	for _, name := range []string{"Autodesk Fusion.app", "Autodesk Fusion 360.app"} {
		candidate := filepath.Join(versionDir, name)
		info, err := os.Lstat(candidate)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if appPath != "" {
			return autodeskFusionBundleMetadata{}, false
		}
		appPath = candidate
	}
	if appPath == "" {
		return autodeskFusionBundleMetadata{}, false
	}

	contents := filepath.Join(appPath, "Contents")
	macOSDir := filepath.Join(contents, "MacOS")
	for _, dir := range []string{contents, macOSDir} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return autodeskFusionBundleMetadata{}, false
		}
	}
	plist := filepath.Join(contents, "Info.plist")
	plistInfo, err := os.Lstat(plist)
	if err != nil || !plistInfo.Mode().IsRegular() || plistInfo.Mode()&os.ModeSymlink != 0 {
		return autodeskFusionBundleMetadata{}, false
	}
	bundleID, ok := cleanPlistValue(ctx, runner, plist, "CFBundleIdentifier")
	if !ok || bundleID != "com.autodesk.fusion360" {
		return autodeskFusionBundleMetadata{}, false
	}
	version, ok := cleanPlistValue(ctx, runner, plist, "CFBundleVersion")
	if !ok {
		return autodeskFusionBundleMetadata{}, false
	}
	if _, comparable := compareAutodeskFusionVersions(version, version); !comparable {
		return autodeskFusionBundleMetadata{}, false
	}
	executable, ok := cleanPlistValue(ctx, runner, plist, "CFBundleExecutable")
	if !ok || !isAutodeskFusionExecutableName(executable) {
		return autodeskFusionBundleMetadata{}, false
	}
	executableInfo, err := os.Lstat(filepath.Join(macOSDir, executable))
	if err != nil || !executableInfo.Mode().IsRegular() || executableInfo.Mode()&os.ModeSymlink != 0 || executableInfo.Mode().Perm()&0o111 == 0 {
		return autodeskFusionBundleMetadata{}, false
	}
	return autodeskFusionBundleMetadata{version: version}, true
}

func compareAutodeskFusionVersions(candidate, current string) (int, bool) {
	parse := func(value string) ([]uint64, bool) {
		if value == "" || len(value) > 128 {
			return nil, false
		}
		parts := strings.Split(value, ".")
		if len(parts) > 16 {
			return nil, false
		}
		parsed := make([]uint64, len(parts))
		for i, part := range parts {
			if part == "" || len(part) > 9 {
				return nil, false
			}
			for _, r := range part {
				if r < '0' || r > '9' {
					return nil, false
				}
			}
			value, err := strconv.ParseUint(part, 10, 32)
			if err != nil {
				return nil, false
			}
			parsed[i] = value
		}
		return parsed, true
	}
	candidateParts, ok := parse(candidate)
	if !ok {
		return 0, false
	}
	currentParts, ok := parse(current)
	if !ok {
		return 0, false
	}
	count := len(candidateParts)
	if len(currentParts) > count {
		count = len(currentParts)
	}
	for i := 0; i < count; i++ {
		var candidatePart, currentPart uint64
		if i < len(candidateParts) {
			candidatePart = candidateParts[i]
		}
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		if candidatePart < currentPart {
			return -1, true
		}
		if candidatePart > currentPart {
			return 1, true
		}
	}
	return 0, true
}

func cleanAutodeskFusionActivityReason(ctx context.Context, activity *cleanActivityProbe, target CleanTarget) string {
	if reason := cleanAutodeskFusionEligibilityReason(ctx, activity.runner, target); reason != "" {
		return reason
	}
	if !activity.processKnown {
		if activity.processStop != "" {
			return "Autodesk Fusion " + activity.processStop
		}
		return "Autodesk Fusion process state unknown"
	}
	if cleanAutodeskFusionRuntimeActive(activity.processTable) {
		return "Autodesk Fusion is running"
	}
	if reason := cleanAutodeskFusionOpenFileReason(ctx, activity.runner, target.Path); reason != "" {
		return reason
	}

	activity.refresh(ctx)
	if !activity.processKnown {
		if activity.processStop != "" {
			return "Autodesk Fusion " + activity.processStop
		}
		return "Autodesk Fusion process state unknown"
	}
	if cleanAutodeskFusionRuntimeActive(activity.processTable) {
		return "Autodesk Fusion is running"
	}
	if reason := cleanAutodeskFusionEligibilityReason(ctx, activity.runner, target); reason != "" {
		return reason
	}
	return cleanAutodeskFusionOpenFileReason(ctx, activity.runner, target.Path)
}

func cleanAutodeskFusionEligibilityReason(ctx context.Context, runner Runner, target CleanTarget) string {
	meta := target.special
	if meta == nil || meta.kind != cleanSpecialAutodeskFusion || !autodeskFusionTargetPathMatches(target.Path, meta) || !autodeskFusionProductionRootTrusted(meta.root) {
		return "Autodesk Fusion target changed"
	}
	currentDir, currentVersion, ok := autodeskFusionCurrentVersion(ctx, runner, meta.root)
	if !ok {
		return "Autodesk Fusion current version unknown"
	}
	if currentDir != meta.currentDir || currentVersion != meta.currentVersion {
		return "Autodesk Fusion current version changed"
	}
	metadata, ok := autodeskFusionVersionDirMetadata(ctx, runner, meta.root, target.Path)
	if !ok || metadata.version != meta.candidateVersion {
		return "Autodesk Fusion candidate changed"
	}
	comparison, comparable := compareAutodeskFusionVersions(metadata.version, currentVersion)
	if !comparable || comparison >= 0 || target.Path == currentDir {
		return "Autodesk Fusion retention changed"
	}
	return ""
}

func cleanAutodeskFusionRuntimeActive(table string) bool {
	for _, line := range strings.Split(table, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "com.autodesk.") || strings.Contains(lower, "/autodesk fusion.app/") ||
			strings.Contains(lower, "/autodesk fusion 360.app/") || strings.Contains(lower, "fusion client downloader") ||
			cleanCommandMentionsProgram(lower, "accoreconsole") || cleanCommandMentionsProgram(lower, "adpclientservice") ||
			cleanCommandMentionsProgram(lower, "fusion360") {
			return true
		}
		fields := strings.Fields(lower)
		if len(fields) > 0 && filepath.Base(fields[0]) == "streamer" {
			return true
		}
	}
	return false
}

func cleanAutodeskFusionOpenFileReason(ctx context.Context, runner Runner, target string) string {
	if _, err := runner.LookPath("lsof"); err != nil {
		return "Autodesk Fusion open-file state unknown"
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	out := runner.Run(probeCtx, "lsof", "-Fn", "+D", target)
	probeErr := probeCtx.Err()
	cancel()
	if probeErr != nil || errors.Is(out.Err, context.DeadlineExceeded) || errors.Is(out.Err, context.Canceled) {
		return "Autodesk Fusion open-file " + cleanProbeStopReason(ctx, "check")
	}
	if out.Err == nil {
		if strings.Contains(out.Stdout, "\nn/") || strings.HasPrefix(out.Stdout, "n/") {
			return "Autodesk Fusion files are open"
		}
		return ""
	}
	if isLsofNoOpenFiles(out) && strings.TrimSpace(out.Stderr) == "" {
		return ""
	}
	return "Autodesk Fusion open-file state unknown"
}

func validateAutodeskFusionTargetPath(target CleanTarget) error {
	meta := target.special
	path := target.Path
	if meta == nil || meta.kind != cleanSpecialAutodeskFusion || path == "" || !filepath.IsAbs(path) || hasDotDotComponent(path) || hasControlChar(path) {
		return errors.New("invalid Autodesk Fusion target")
	}
	if !autodeskFusionProductionRootTrusted(meta.root) || !autodeskFusionTargetPathMatches(path, meta) || !cleanDirectChildPathSafe(path, meta.root) {
		return errors.New("unsafe Autodesk Fusion target")
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

func autodeskFusionTargetPathMatches(path string, meta *cleanSpecialTarget) bool {
	if meta == nil || meta.kind != cleanSpecialAutodeskFusion || filepath.Clean(meta.root) != filepath.Clean(autodeskFusionProductionRoot()) {
		return false
	}
	rel, ok := cleanRelUnder(path, meta.root)
	return ok && rel != "." && !strings.Contains(rel, string(os.PathSeparator)) && autodeskFusionVersionDirName.MatchString(rel)
}

func isAutodeskFusionAppName(name string) bool {
	return name == "Autodesk Fusion.app" || name == "Autodesk Fusion 360.app"
}

func isAutodeskFusionExecutableName(name string) bool {
	return name == "Autodesk Fusion" || name == "Autodesk Fusion 360"
}
