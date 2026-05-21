package bloom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Uninstall scanning and removal for macOS .app bundles.
//
// Inspiration: tw93/Mole (MIT). The set of cleanup locations below reflects
// well-known, publicly documented macOS user/library paths that any app
// uninstaller must consider. The implementation here is original Bloom code
// in Go; no Mole source is copied.

// AppEntry describes a discovered macOS .app bundle.
type AppEntry struct {
	Path     string
	Name     string // basename without .app
	BundleID string
	SizeKB   int64
	// LastUsedEpoch is seconds since the Unix epoch for the bundle's
	// kMDItemLastUsedDate metadata, or 0 when unknown.
	LastUsedEpoch int64
}

// UninstallResult captures the outcome for a single app removal.
type UninstallResult struct {
	App          AppEntry
	RemovedKB    int64
	Files        []string
	Failed       []string
	Err          error
	BrewRemoved  bool
	StillRunning bool
}

// BatchSummary aggregates per-app results plus shared post-batch effects.
type BatchSummary struct {
	Results        []UninstallResult
	TotalRemovedKB int64
	BrewAutoremove bool
}

var defaultAppDirs = []string{
	"/Applications",
	"/Applications/Setapp",
	"~/Applications",
}

// ScanApplications walks the standard macOS application directories and
// returns every .app bundle that is not a system-protected component.
func ScanApplications(ctx context.Context) ([]AppEntry, error) {
	home, _ := os.UserHomeDir()
	seen := map[string]bool{}
	var apps []AppEntry

	for _, dir := range defaultAppDirs {
		root := dir
		if strings.HasPrefix(root, "~") && home != "" {
			root = filepath.Join(home, root[1:])
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		walkAppDir(ctx, root, seen, &apps)
	}

	sort.Slice(apps, func(i, j int) bool {
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
	return apps, nil
}

func walkAppDir(ctx context.Context, root string, seen map[string]bool, out *[]AppEntry) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return
		default:
		}
		name := entry.Name()
		full := filepath.Join(root, name)
		if !strings.HasSuffix(strings.ToLower(name), ".app") {
			// Recurse one level for grouping folders such as "Utilities".
			if entry.IsDir() && root == "/Applications" && !strings.HasPrefix(name, ".") {
				sub, err := os.ReadDir(full)
				if err != nil {
					continue
				}
				for _, child := range sub {
					if !strings.HasSuffix(strings.ToLower(child.Name()), ".app") {
						continue
					}
					addAppEntry(filepath.Join(full, child.Name()), seen, out)
				}
			}
			continue
		}
		addAppEntry(full, seen, out)
	}
}

func addAppEntry(path string, seen map[string]bool, out *[]AppEntry) {
	if seen[path] {
		return
	}
	if isProtectedAppPath(path) {
		return
	}
	seen[path] = true
	entry := AppEntry{
		Path: path,
		Name: strings.TrimSuffix(filepath.Base(path), ".app"),
	}
	entry.BundleID = readBundleID(path)
	entry.SizeKB = directorySizeKB(path)
	entry.LastUsedEpoch = readLastUsedEpoch(path)
	*out = append(*out, entry)
}

// readLastUsedEpoch returns the bundle's Spotlight kMDItemLastUsedDate as a
// Unix timestamp. Falls back to the bundle's mtime so freshly scanned items
// never display as "Unknown" forever.
func readLastUsedEpoch(appPath string) int64 {
	cmd := exec.Command("/usr/bin/mdls", "-name", "kMDItemLastUsedDate", "-raw", appPath)
	out, err := cmd.Output()
	if err == nil {
		raw := strings.TrimSpace(string(out))
		if raw != "" && raw != "(null)" {
			// mdls prints e.g. "2024-08-13 09:31:22 +0000".
			if t, err := time.Parse("2006-01-02 15:04:05 -0700", raw); err == nil {
				if epoch := t.Unix(); epoch > epochFloor {
					return epoch
				}
			}
		}
	}
	if info, err := os.Stat(appPath); err == nil {
		if epoch := info.ModTime().Unix(); epoch > epochFloor {
			return epoch
		}
	}
	return 0
}

// epochFloor rejects clearly bogus timestamps (e.g. 2001-01-01 default).
const epochFloor int64 = 978307200

// FormatLastUsed turns a Unix timestamp into a short human-readable phrase.
func FormatLastUsed(epoch int64) string {
	if epoch <= 0 {
		return "Unknown"
	}
	days := int((time.Now().Unix() - epoch) / 86400)
	if days < 0 {
		days = 0
	}
	switch {
	case days == 0:
		return "Today"
	case days == 1:
		return "Yesterday"
	case days < 7:
		return fmt.Sprintf("%d days ago", days)
	case days < 30:
		w := days / 7
		if w == 1 {
			return "1 week ago"
		}
		return fmt.Sprintf("%d weeks ago", w)
	case days < 365:
		m := days / 30
		if m == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", m)
	default:
		y := days / 365
		if y == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", y)
	}
}

// isProtectedAppPath skips Apple system bundles that should never be touched.
func isProtectedAppPath(path string) bool {
	if appPathStringIsProtected(path) {
		return true
	}
	if target := symlinkTargetPath(path); target != "" && appPathStringIsProtected(target) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != "" && resolved != path && appPathStringIsProtected(resolved) {
		return true
	}
	return false
}

func appPathStringIsProtected(path string) bool {
	lower := strings.ToLower(filepath.Clean(path))
	if lower == "/system" || strings.HasPrefix(lower, "/system/") {
		return true
	}
	base := filepath.Base(path)
	if strings.HasSuffix(strings.ToLower(base), ".app") {
		base = base[:len(base)-len(".app")]
	}
	switch strings.ToLower(base) {
	case "finder", "system preferences", "system settings", "safari",
		"app store", "messages", "facetime", "mail", "contacts",
		"calendar", "reminders", "notes", "maps", "photos", "music",
		"tv", "podcasts", "news", "stocks", "voice memos",
		"home", "find my", "freeform", "shortcuts", "automator",
		"image capture", "preview", "quicktime player", "textedit",
		"calculator", "chess", "dictionary", "stickies", "weather",
		"time machine", "migration assistant", "feedback assistant", "console", "activity monitor",
		"disk utility", "keychain access", "terminal":
		return true
	}
	return false
}

func symlinkTargetPath(path string) string {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	target, err := os.Readlink(path)
	if err != nil || target == "" {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target)
}

func readBundleID(appPath string) string {
	plist := filepath.Join(appPath, "Contents", "Info.plist")
	if _, err := os.Stat(plist); err != nil {
		return ""
	}
	cmd := exec.Command("/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", plist)
	out, err := cmd.Output()
	id := ""
	if err == nil {
		id = strings.TrimSpace(string(out))
	} else {
		id = readXMLPlistString(plist, "CFBundleIdentifier")
	}
	if id == "(null)" || id == "" {
		return ""
	}
	if !looksLikeBundleID(id) {
		return ""
	}
	return id
}

func readBundleExecutable(appPath string) string {
	plist := filepath.Join(appPath, "Contents", "Info.plist")
	if _, err := os.Stat(plist); err != nil {
		return ""
	}
	cmd := exec.Command("/usr/bin/plutil", "-extract", "CFBundleExecutable", "raw", plist)
	out, err := cmd.Output()
	name := ""
	if err == nil {
		name = strings.TrimSpace(string(out))
	} else {
		name = readXMLPlistString(plist, "CFBundleExecutable")
	}
	if name == "(null)" {
		return ""
	}
	return name
}

func readXMLPlistString(plist, key string) string {
	data, err := os.ReadFile(plist)
	if err != nil {
		return ""
	}
	text := string(data)
	marker := "<key>" + key + "</key>"
	idx := strings.Index(text, marker)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(marker):]
	start := strings.Index(rest, "<string>")
	if start < 0 {
		return ""
	}
	rest = rest[start+len("<string>"):]
	end := strings.Index(rest, "</string>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func looksLikeBundleID(id string) bool {
	parts := strings.Split(id, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for i, r := range part {
			if i == 0 && !isBundleIDAlphaNum(r) {
				return false
			}
			if !isBundleIDAlphaNum(r) && r != '-' {
				return false
			}
		}
	}
	return true
}

func isBundleIDAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

func bundleIDComponents(id string) []string {
	if !looksLikeBundleID(id) {
		return nil
	}
	return strings.Split(id, ".")
}

func bundleIDEqual(id, want string) bool {
	return strings.EqualFold(id, want)
}

func directorySizeKB(path string) int64 {
	cmd := exec.Command("/usr/bin/du", "-sk", path)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0
	}
	var size int64
	for _, c := range fields[0] {
		if c < '0' || c > '9' {
			break
		}
		size = size*10 + int64(c-'0')
	}
	return size
}

// FindRelatedPaths returns every cleanup candidate for the given app.
// Includes the bundle itself, then standard ~/Library subdirectories that
// match by bundle id or app basename. Paths are de-duplicated and ordered.
func FindRelatedPaths(app AppEntry) []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return []string{app.Path}
	}

	tokens := uniqueStrings([]string{app.BundleID, app.Name})
	paths := []string{app.Path}
	add := func(p string) {
		if p == "" {
			return
		}
		paths = append(paths, p)
	}

	libraryRoots := []string{
		filepath.Join(home, "Library", "Application Support"),
		filepath.Join(home, "Library", "Caches"),
		filepath.Join(home, "Library", "HTTPStorages"),
		filepath.Join(home, "Library", "Containers"),
		filepath.Join(home, "Library", "Group Containers"),
		filepath.Join(home, "Library", "WebKit"),
		filepath.Join(home, "Library", "Logs"),
		filepath.Join(home, "Library", "Saved Application State"),
		filepath.Join(home, "Library", "Application Scripts"),
	}

	for _, root := range libraryRoots {
		for _, tok := range tokens {
			if tok == "" {
				continue
			}
			matchInDir(root, tok, &paths)
		}
	}
	matchGroupContainers(app, &paths)
	matchDiagnosticReports(app, &paths)
	matchVSCodePaths(app, &paths)

	// Preferences: <bundleID>.plist + ByHost variants
	prefDir := filepath.Join(home, "Library", "Preferences")
	if app.BundleID != "" {
		add(filepath.Join(prefDir, app.BundleID+".plist"))
		matchInDir(filepath.Join(prefDir, "ByHost"), app.BundleID, &paths)
	}

	// LaunchAgents: <bundleID>*.plist
	launchUser := filepath.Join(home, "Library", "LaunchAgents")
	if app.BundleID != "" {
		matchInDir(launchUser, app.BundleID, &paths)
	}

	// Cookies + HTTPStorages binarycookies
	cookies := filepath.Join(home, "Library", "Cookies")
	if app.BundleID != "" {
		add(filepath.Join(cookies, app.BundleID+".binarycookies"))
	}

	// Keep only existing paths and dedupe.
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		out = append(out, p)
	}

	return out
}

func matchVSCodePaths(app AppEntry, out *[]string) {
	home, _ := os.UserHomeDir()
	if home == "" {
		return
	}

	stable := bundleIDEqual(app.BundleID, "com.microsoft.VSCode") || strings.EqualFold(app.Name, "Visual Studio Code")
	insiders := bundleIDEqual(app.BundleID, "com.microsoft.VSCodeInsiders") || strings.EqualFold(app.Name, "Visual Studio Code - Insiders")
	if !stable && !insiders {
		return
	}
	add := func(p string) {
		*out = append(*out, p)
	}
	if insiders {
		add(filepath.Join(home, ".vscode-insiders"))
		add(filepath.Join(home, "Library", "Application Support", "Code - Insiders"))
		add(filepath.Join(home, "Library", "Caches", "com.microsoft.VSCodeInsiders"))
		add(filepath.Join(home, "Library", "Caches", "com.microsoft.VSCodeInsiders.ShipIt"))
		return
	}
	add(filepath.Join(home, ".vscode"))
	add(filepath.Join(home, "Library", "Application Support", "Code"))
	add(filepath.Join(home, "Library", "Caches", "com.microsoft.VSCode"))
	add(filepath.Join(home, "Library", "Caches", "com.microsoft.VSCode.ShipIt"))
}

// matchInDir scans dir for entries containing token on filename boundaries
// (case-insensitive) and appends matching absolute paths.
func matchInDir(dir, token string, out *[]string) {
	if token == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		if nameContainsTokenOnBoundary(name, token) {
			*out = append(*out, filepath.Join(dir, name))
		}
	}
}

func nameContainsTokenOnBoundary(name, token string) bool {
	if token == "" {
		return false
	}
	name = strings.ToLower(name)
	token = strings.ToLower(token)
	for start := 0; start < len(name); {
		idx := strings.Index(name[start:], token)
		if idx < 0 {
			return false
		}
		idx += start
		end := idx + len(token)
		if tokenBoundaryBefore(name, idx) && tokenBoundaryAfter(name, end) {
			return true
		}
		_, size := utf8.DecodeRuneInString(name[idx:])
		if size <= 0 {
			return false
		}
		start = idx + size
	}
	return false
}

func tokenBoundaryBefore(s string, idx int) bool {
	if idx <= 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:idx])
	return isTokenBoundary(r)
}

func tokenBoundaryAfter(s string, idx int) bool {
	if idx >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[idx:])
	return isTokenBoundary(r)
}

func isTokenBoundary(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

func matchGroupContainers(app AppEntry, out *[]string) {
	if app.BundleID == "" {
		return
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return
	}
	dir := filepath.Join(home, "Library", "Group Containers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	domainPrefix := bundleDomainPrefix(app.BundleID)
	if domainPrefix == "" {
		return
	}
	teamID := readTeamID(app.Path)
	allowDomainFallback := app.Path == ""
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		if groupContainerMatches(name, teamID, domainPrefix, allowDomainFallback) {
			*out = append(*out, filepath.Join(dir, name))
		}
	}
}

func bundleDomainPrefix(bundleID string) string {
	parts := bundleIDComponents(bundleID)
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], ".")
}

func groupContainerMatches(name, teamID, domainPrefix string, allowDomainFallback bool) bool {
	if domainPrefix == "" {
		return false
	}
	if teamID != "" {
		if strings.HasPrefix(name, teamID+"."+domainPrefix) || strings.HasPrefix(name, teamID+".group."+domainPrefix) {
			return true
		}
		if rest := strings.TrimPrefix(name, teamID+"."); rest != name && strings.Contains(rest, "."+domainPrefix) {
			return true
		}
		return false
	}
	if !allowDomainFallback {
		return false
	}
	return strings.HasSuffix(name, "."+domainPrefix) || strings.HasSuffix(name, ".group."+domainPrefix)
}

func readTeamID(appPath string) string {
	if appPath == "" {
		return ""
	}
	cmd := exec.Command("/usr/bin/codesign", "-dv", appPath)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	for _, field := range strings.Fields(string(out)) {
		value, ok := strings.CutPrefix(field, "TeamIdentifier=")
		if !ok || !looksLikeTeamID(value) {
			continue
		}
		return value
	}
	return ""
}

func looksLikeTeamID(value string) bool {
	if len(value) < 5 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func matchDiagnosticReports(app AppEntry, out *[]string) {
	home, _ := os.UserHomeDir()
	if home == "" {
		return
	}
	dir := filepath.Join(home, "Library", "Logs", "DiagnosticReports")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefixes := diagnosticReportPrefixes(app)
	if len(prefixes) == 0 {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !diagnosticReportNameMatches(name, prefixes) {
			continue
		}
		*out = append(*out, filepath.Join(dir, name))
	}
}

func diagnosticReportPrefixes(app AppEntry) []string {
	values := []string{
		readBundleExecutable(app.Path),
		strings.ReplaceAll(app.Name, " ", ""),
	}
	prefixes := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) < 2 {
			continue
		}
		value = strings.ToLower(value)
		if seen[value] {
			continue
		}
		seen[value] = true
		prefixes = append(prefixes, value)
	}
	return prefixes
}

func diagnosticReportNameMatches(name string, prefixes []string) bool {
	lower := strings.ToLower(name)
	switch filepath.Ext(lower) {
	case ".ips", ".crash", ".spin", ".diag":
	default:
		return false
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix+".") || strings.HasPrefix(lower, prefix+"_") || strings.HasPrefix(lower, prefix+"-") {
			return true
		}
	}
	return false
}

// UninstallApp moves the bundle and all related files for one app to Trash.
// If dryRun is true, no files are touched and Files lists what would be
// moved.
//
// Order matters for Homebrew casks: brew must be invoked BEFORE the .app
// bundle is deleted so it can detect its own artifact and clean its
// receipt under Caskroom. Otherwise `brew list --cask` would still
// report the cask as installed.
func UninstallApp(ctx context.Context, runner Runner, app AppEntry, dryRun bool) UninstallResult {
	res := UninstallResult{App: app}
	if app.Path == "" {
		res.Err = errors.New("missing app path")
		return res
	}

	if !dryRun {
		res.StillRunning = stopApp(ctx, runner, app)
	}

	cask := detectHomebrewCask(ctx, runner, app)
	if cask != "" {
		if dryRun {
			res.BrewRemoved = true
		} else {
			out := runner.Run(ctx, "brew", "uninstall", "--cask", "--zap", "--force", cask)
			if out.Err == nil && !brewCaskStillInstalled(ctx, runner, cask) {
				res.BrewRemoved = true
			}
		}
	}

	paths := FindRelatedPaths(app)
	for _, p := range paths {
		size := pathSizeKB(p)
		if dryRun {
			res.Files = append(res.Files, p)
			res.RemovedKB += size
			continue
		}
		// brew --zap may have already removed it.
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		if err := movePathToTrashStubborn(ctx, runner, p); err != nil {
			res.Failed = append(res.Failed, p)
			continue
		}
		res.Files = append(res.Files, p)
		res.RemovedKB += size
	}

	if !dryRun {
		removeLoginItem(ctx, runner, app)
		unregisterLaunchServices(ctx, runner, app.Path)
	}

	if len(res.Failed) > 0 && len(res.Files) == 0 && !res.BrewRemoved {
		res.Err = fmt.Errorf("nothing removed (%d failures)", len(res.Failed))
	}
	return res
}

// BatchUninstall uninstalls a list of apps and then runs shared post-batch
// cleanup once: refresh LaunchServices, remove Dock entries, run
// `brew autoremove` if any cask was removed. Bloom-managed paths are moved to
// Trash; paths that macOS refuses to trash are reported as failures instead of
// being permanently deleted.
func BatchUninstall(ctx context.Context, runner Runner, apps []AppEntry, dryRun bool) BatchSummary {
	var sum BatchSummary
	anyBrew := false

	for _, app := range apps {
		res := UninstallApp(ctx, runner, app, dryRun)
		sum.Results = append(sum.Results, res)
		sum.TotalRemovedKB += res.RemovedKB
		if res.BrewRemoved {
			anyBrew = true
		}
	}
	if dryRun {
		sum.BrewAutoremove = anyBrew
		return sum
	}
	removeAppsFromDock(ctx, runner, apps)
	refreshLaunchServices(ctx, runner)
	if anyBrew {
		out := runner.Run(ctx, "brew", "autoremove")
		sum.BrewAutoremove = out.Err == nil
	}
	return sum
}

// removeLoginItem deletes a macOS Login Item entry whose name matches the app.
// Uses System Events via osascript, which is Apple's documented mechanism for
// programmatic Login Items management.
func removeLoginItem(ctx context.Context, runner Runner, app AppEntry) {
	name := strings.ReplaceAll(app.Name, `\`, `\\`)
	name = strings.ReplaceAll(name, `"`, `\"`)
	script := fmt.Sprintf(`tell application "System Events"
		try
			repeat with i from (count of login items) to 1 by -1
				try
					if name of login item i is "%s" then
						delete login item i
					end if
				end try
			end repeat
		end try
	end tell`, name)
	_ = runner.Run(ctx, "/usr/bin/osascript", "-e", script)
}

// lsregisterPath returns the canonical path for the Launch Services helper
// shipped with macOS.
const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister"

// unregisterLaunchServices removes the app bundle from the LaunchServices
// database so stale entries do not linger in Spotlight or "Open With".
func unregisterLaunchServices(ctx context.Context, runner Runner, appPath string) {
	if appPath == "" {
		return
	}
	_ = runner.Run(ctx, lsregisterPath, "-u", appPath)
}

// refreshLaunchServices garbage-collects and rebuilds the LaunchServices
// database so removed apps disappear from system UIs.
func refreshLaunchServices(ctx context.Context, runner Runner) {
	_ = runner.Run(ctx, lsregisterPath, "-gc")
	_ = runner.Run(ctx, lsregisterPath, "-r", "-f", "-domain", "local", "-domain", "user", "-domain", "system")
}

// removeAppsFromDock rewrites com.apple.dock to drop persistent-apps tiles
// whose file-data path matches one of the removed bundles, then restarts Dock.
func removeAppsFromDock(ctx context.Context, runner Runner, apps []AppEntry) {
	if len(apps) == 0 {
		return
	}
	out := runner.Run(ctx, "defaults", "read", "com.apple.dock", "persistent-apps")
	if out.Err != nil {
		return
	}
	changed := false
	body := out.Stdout
	for _, app := range apps {
		// "_CFURLString" = "file:///Applications/Foo.app/"
		needle := "file://" + urlPathEscape(app.Path)
		if strings.Contains(body, needle) || strings.Contains(body, app.Path) {
			changed = true
		}
	}
	if !changed {
		return
	}
	// Rebuild the array by filtering out matching entries via PlistBuddy.
	for _, app := range apps {
		// Iterate from high to low to keep indices stable as we delete.
		count := dockTileCount(ctx, runner)
		for i := count - 1; i >= 0; i-- {
			val := runner.Run(ctx, "/usr/libexec/PlistBuddy", "-c",
				fmt.Sprintf("Print :persistent-apps:%d:tile-data:file-data:_CFURLString", i),
				dockPlistPath())
			if val.Err != nil {
				continue
			}
			s := strings.TrimSpace(val.Stdout)
			if strings.Contains(s, app.Path) || strings.Contains(s, urlPathEscape(app.Path)) {
				_ = runner.Run(ctx, "/usr/libexec/PlistBuddy", "-c",
					fmt.Sprintf("Delete :persistent-apps:%d", i),
					dockPlistPath())
			}
		}
	}
	_ = runner.Run(ctx, "/usr/bin/killall", "Dock")
}

func dockPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Preferences", "com.apple.dock.plist")
}

func dockTileCount(ctx context.Context, runner Runner) int {
	out := runner.Run(ctx, "/usr/libexec/PlistBuddy", "-c",
		"Print :persistent-apps", dockPlistPath())
	if out.Err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(out.Stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Dict {") {
			count++
		}
	}
	return count
}

func urlPathEscape(p string) string {
	// Minimal escaping for the file:// URL form used by Dock plists.
	r := strings.NewReplacer(" ", "%20")
	return r.Replace(p)
}

const stopAppQuitPolls = 10
const stopAppForcePolls = 5

var stopAppPollInterval = 100 * time.Millisecond

func stopApp(ctx context.Context, runner Runner, app AppEntry) bool {
	matchName := readBundleExecutable(app.Path)
	if matchName == "" {
		matchName = app.Name
	}

	running := processRunning(ctx, runner, matchName)
	if running {
		if script := quitAppScript(app); script != "" {
			_ = runner.Run(ctx, "/usr/bin/osascript", "-e", script)
		}
		running = !waitForProcessExit(ctx, runner, matchName, stopAppQuitPolls)
	}

	unloadMatchingLaunchAgents(ctx, runner, app)

	if running {
		running = !forceQuitAppProcesses(ctx, runner, app, matchName)
	}
	return running
}

func unloadMatchingLaunchAgents(ctx context.Context, runner Runner, app AppEntry) {
	if app.BundleID == "" {
		return
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !launchAgentNameMatchesBundleID(name, app.BundleID) {
			continue
		}
		_ = runner.Run(ctx, "/bin/launchctl", "unload", filepath.Join(dir, name))
	}
}

func forceQuitAppProcesses(ctx context.Context, runner Runner, app AppEntry, matchName string) bool {
	pids := matchingAppProcessIDs(ctx, runner, app, matchName)
	if len(pids) == 0 {
		return !processRunning(ctx, runner, matchName)
	}
	for _, pid := range pids {
		_ = runner.Run(ctx, "/bin/kill", "-TERM", pid)
	}
	if waitForMatchedAppProcessesExit(ctx, runner, app, matchName, stopAppForcePolls) {
		return true
	}

	pids = matchingAppProcessIDs(ctx, runner, app, matchName)
	if len(pids) == 0 {
		return true
	}
	for _, pid := range pids {
		_ = runner.Run(ctx, "/bin/kill", "-KILL", pid)
	}
	return waitForMatchedAppProcessesExit(ctx, runner, app, matchName, stopAppForcePolls)
}

func waitForMatchedAppProcessesExit(ctx context.Context, runner Runner, app AppEntry, matchName string, polls int) bool {
	for i := 0; i < polls; i++ {
		if len(matchingAppProcessIDs(ctx, runner, app, matchName)) == 0 {
			return true
		}
		if stopAppPollInterval <= 0 {
			continue
		}
		timer := time.NewTimer(stopAppPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return true
		case <-timer.C:
		}
	}
	return len(matchingAppProcessIDs(ctx, runner, app, matchName)) == 0
}

func matchingAppProcessIDs(ctx context.Context, runner Runner, app AppEntry, matchName string) []string {
	if matchName == "" {
		return nil
	}
	bundlePaths := appBundlePathCandidates(app.Path)
	if len(bundlePaths) == 0 {
		return nil
	}
	var matched []string
	for _, pid := range processIDs(ctx, runner, matchName) {
		processPath := processExecutablePath(ctx, runner, pid)
		if processPathMatchesAppBundle(processPath, matchName, bundlePaths) {
			matched = append(matched, pid)
		}
	}
	return matched
}

func appBundlePathCandidates(appPath string) []string {
	var paths []string
	seen := map[string]bool{}
	addPath := func(path string) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	addPath(appPath)
	addPath(symlinkTargetPath(appPath))
	if resolved, err := filepath.EvalSymlinks(appPath); err == nil {
		addPath(resolved)
	}
	return paths
}

func processExecutablePath(ctx context.Context, runner Runner, pid string) string {
	out := runner.Run(ctx, "/bin/ps", "-ww", "-p", pid, "-o", "comm=")
	if out.Err != nil {
		return ""
	}
	for _, line := range strings.Split(out.Stdout, "\n") {
		if path := strings.TrimSpace(line); path != "" {
			return path
		}
	}
	return ""
}

func processPathMatchesAppBundle(processPath, matchName string, bundlePaths []string) bool {
	if processPath == "" || matchName == "" || !filepath.IsAbs(processPath) {
		return false
	}
	processPaths := []string{filepath.Clean(processPath)}
	if resolved, err := filepath.EvalSymlinks(processPath); err == nil && resolved != "" {
		processPaths = append(processPaths, filepath.Clean(resolved))
	}
	seen := map[string]bool{}
	for _, processPath := range processPaths {
		if seen[processPath] {
			continue
		}
		seen[processPath] = true
		for _, bundlePath := range bundlePaths {
			expected := filepath.Join(bundlePath, "Contents", "MacOS", matchName)
			if processPath == filepath.Clean(expected) {
				return true
			}
		}
	}
	return false
}

func quitAppScript(app AppEntry) string {
	if looksLikeBundleID(app.BundleID) {
		return fmt.Sprintf("tell application id %q to quit", app.BundleID)
	}
	if app.Name != "" {
		return fmt.Sprintf("tell application %q to quit", app.Name)
	}
	return ""
}

func launchAgentNameMatchesBundleID(name, bundleID string) bool {
	if !looksLikeBundleID(bundleID) || !strings.HasSuffix(name, ".plist") {
		return false
	}
	stem := strings.TrimSuffix(name, ".plist")
	return stem == bundleID || strings.HasPrefix(stem, bundleID+".")
}

func processRunning(ctx context.Context, runner Runner, name string) bool {
	return len(processIDs(ctx, runner, name)) > 0
}

func processIDs(ctx context.Context, runner Runner, name string) []string {
	if name == "" {
		return nil
	}
	out := runner.Run(ctx, "/usr/bin/pgrep", "-x", name)
	if out.Err != nil {
		return nil
	}
	var pids []string
	for _, field := range strings.Fields(out.Stdout) {
		if looksLikePID(field) {
			pids = append(pids, field)
		}
	}
	return pids
}

func looksLikePID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func waitForProcessExit(ctx context.Context, runner Runner, name string, polls int) bool {
	for i := 0; i < polls; i++ {
		if !processRunning(ctx, runner, name) {
			return true
		}
		if stopAppPollInterval <= 0 {
			continue
		}
		timer := time.NewTimer(stopAppPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return true
		case <-timer.C:
		}
	}
	return !processRunning(ctx, runner, name)
}

// caskroomDirs lists the standard Homebrew Caskroom locations on macOS.
// Apple Silicon uses /opt/homebrew, Intel uses /usr/local.
var caskroomDirs = []string{
	"/opt/homebrew/Caskroom",
	"/usr/local/Caskroom",
}

// detectHomebrewCask returns the cask token that owns app, or "" if none.
// The lookup follows Homebrew's documented Caskroom layout
// (<prefix>/Caskroom/<token>/<version>/...) and uses progressively broader
// strategies to handle apps that are not symlinked from /Applications.
func detectHomebrewCask(ctx context.Context, runner Runner, app AppEntry) string {
	if _, err := runner.LookPath("brew"); err != nil {
		return ""
	}
	if token := caskTokenFromResolvedPath(app.Path); token != "" {
		return token
	}
	if token := caskTokenFromSymlink(app.Path); token != "" {
		return token
	}
	bundle := filepath.Base(app.Path)
	if token := caskTokenByBundleSearch(bundle); token != "" {
		return token
	}
	return caskTokenByBrewQuery(ctx, runner, app)
}

// caskTokenFromCaskroomPath returns the token component when path is inside
// any known Caskroom, else "".
func caskTokenFromCaskroomPath(path string) string {
	for _, room := range caskroomDirs {
		prefix := room + "/"
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		token := rest
		if idx := strings.Index(rest, "/"); idx >= 0 {
			token = rest[:idx]
		}
		if isValidCaskToken(token) {
			return token
		}
	}
	return ""
}

func caskTokenFromResolvedPath(appPath string) string {
	resolved, err := filepath.EvalSymlinks(appPath)
	if err != nil || resolved == "" {
		return ""
	}
	return caskTokenFromCaskroomPath(resolved)
}

func caskTokenFromSymlink(appPath string) string {
	info, err := os.Lstat(appPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	target, err := os.Readlink(appPath)
	if err != nil || target == "" {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(appPath), target)
	}
	return caskTokenFromCaskroomPath(filepath.Clean(target))
}

// caskTokenByBundleSearch walks each Caskroom looking for a directory entry
// matching the bundle name. Returns the token only if exactly one cask owns
// that bundle, to avoid uninstalling the wrong package.
func caskTokenByBundleSearch(bundle string) string {
	if bundle == "" {
		return ""
	}
	tokens := map[string]bool{}
	for _, room := range caskroomDirs {
		entries, err := os.ReadDir(room)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			token := entry.Name()
			if !isValidCaskToken(token) {
				continue
			}
			if matchesBundleUnderCask(filepath.Join(room, token), bundle) {
				tokens[token] = true
			}
		}
	}
	if len(tokens) != 1 {
		return ""
	}
	for token := range tokens {
		return token
	}
	return ""
}

// matchesBundleUnderCask reports whether a Caskroom/<token>/* tree
// contains an entry literally named bundle (e.g. "Foo.app").
func matchesBundleUnderCask(caskDir, bundle string) bool {
	versions, err := os.ReadDir(caskDir)
	if err != nil {
		return false
	}
	for _, ver := range versions {
		if !ver.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(caskDir, ver.Name()))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), bundle) {
				return true
			}
		}
	}
	return false
}

// caskTokenByBrewQuery is the slowest fallback: list all installed casks,
// match by lowercased app name, then verify with brew info that the app
// path is actually owned by that cask.
func caskTokenByBrewQuery(ctx context.Context, runner Runner, app AppEntry) string {
	out := runner.Run(ctx, "brew", "list", "--cask")
	if out.Err != nil {
		return ""
	}
	wanted := strings.ToLower(app.Name)
	for _, line := range strings.Split(out.Stdout, "\n") {
		token := strings.TrimSpace(line)
		if token == "" {
			continue
		}
		if !strings.EqualFold(token, wanted) {
			continue
		}
		info := runner.Run(ctx, "brew", "info", "--cask", token)
		if info.Err == nil && strings.Contains(info.Stdout, app.Path) {
			return token
		}
		// Even without explicit path mention, exact-name match counts.
		return token
	}
	return ""
}

// brewCaskStillInstalled returns true when `brew list --cask` still lists
// the token (i.e. the uninstall did not take effect).
func brewCaskStillInstalled(ctx context.Context, runner Runner, token string) bool {
	if token == "" {
		return false
	}
	out := runner.Run(ctx, "brew", "list", "--cask")
	if out.Err != nil {
		return false
	}
	for _, line := range strings.Split(out.Stdout, "\n") {
		if strings.TrimSpace(line) == token {
			return true
		}
	}
	return false
}

func isValidCaskToken(token string) bool {
	if token == "" {
		return false
	}
	for i, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '@' || r == '.':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// movePathToTrashStubborn moves p to Trash, retrying after clearing common
// macOS flags and write bits. It deliberately never falls back to permanent
// deletion; paths that macOS refuses to trash are reported to the caller.
func movePathToTrashStubborn(ctx context.Context, runner Runner, p string) error {
	if err := movePathToTrash(ctx, runner, p); err == nil {
		return nil
	}
	_ = runner.Run(ctx, "/usr/bin/chflags", "-R", "nouchg,noschg,nouappnd", p)
	_ = runner.Run(ctx, "/bin/chmod", "-R", "u+w", p)
	if err := movePathToTrash(ctx, runner, p); err == nil {
		return nil
	}
	return fmt.Errorf("could not move to Trash")
}

func pathSizeKB(p string) int64 {
	info, err := os.Lstat(p)
	if err != nil {
		return 0
	}
	if info.Mode().IsRegular() {
		return (info.Size() + 1023) / 1024
	}
	return directorySizeKB(p)
}

// FormatBytes returns a short human readable size from KB.
func FormatBytes(kb int64) string {
	bytes := kb * 1024
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// PrintAppList writes a TSV listing of installed apps:
// path, name, bundleID, sizeKB, lastUsedEpoch, nameDisplayWidth.
// nameDisplayWidth is the rendered width (CJK/fullwidth = 2) so callers
// can pad in monospaced terminals without miscounting bytes vs columns.
func PrintAppList(out io.Writer, apps []AppEntry) {
	for _, app := range apps {
		fmt.Fprintf(out, "%s\t%s\t%s\t%d\t%d\t%d\n",
			app.Path, app.Name, app.BundleID, app.SizeKB, app.LastUsedEpoch,
			DisplayWidth(app.Name))
	}
}

// DisplayWidth returns the rendered width of s in monospaced cells.
// CJK ideographs, Hangul, Hiragana/Katakana, fullwidth Latin, and most
// emoji are counted as width 2. ASCII control characters are skipped.
func DisplayWidth(s string) int {
	width := 0
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		if isWideRune(r) {
			width += 2
		} else {
			width++
		}
	}
	return width
}

// isWideRune reports whether r occupies two columns when rendered in a
// typical monospaced terminal. Ranges follow Unicode East Asian Width.
func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E,   // CJK Radicals, Kangxi
		r >= 0x3041 && r <= 0x33FF,   // Hiragana / Katakana / CJK Symbols
		r >= 0x3400 && r <= 0x4DBF,   // CJK Ext A
		r >= 0x4E00 && r <= 0x9FFF,   // CJK Unified
		r >= 0xA000 && r <= 0xA4CF,   // Yi Syllables
		r >= 0xAC00 && r <= 0xD7A3,   // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF,   // CJK Compatibility Ideographs
		r >= 0xFE30 && r <= 0xFE4F,   // CJK Compatibility Forms
		r >= 0xFF00 && r <= 0xFF60,   // Fullwidth Forms
		r >= 0xFFE0 && r <= 0xFFE6,   // Fullwidth Signs
		r >= 0x1F300 && r <= 0x1FAFF, // Emoji + Pictographs
		r >= 0x20000 && r <= 0x2FFFD, // CJK Ext B-F
		r >= 0x30000 && r <= 0x3FFFD: // CJK Ext G
		return true
	}
	return false
}
