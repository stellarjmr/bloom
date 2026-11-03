package bloom

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	uninstallSiblingScanTimeout    = 5 * time.Second
	uninstallSiblingCandidateCap   = 2048
	uninstallSiblingInfoPlistLimit = 4 << 20
)

// Keep the sibling scan aligned with Bloom's application discovery scope.
// Tests replace this slice with an isolated application root.
var uninstallSiblingAppDirs = defaultAppDirs

type uninstallSiblingScan struct {
	// Paths are other installed copies with the same bundle ID.
	Paths    []string
	Complete bool
	// Family lists other installed builds of the same app: a name that differs
	// from the selected one only by a release channel ("Firefox" and "Firefox
	// Nightly"), or a bundle ID that extends the selected one at a dot or is
	// extended by it (com.google.Chrome and com.google.Chrome.canary).
	Family []string
	// NameFamily reports that a Family build shares the selected app's base
	// name, and with it the folders named after that base.
	NameFamily bool
	// ExtendedIDs are Family bundle IDs that strictly extend the selected ID.
	// A leftover named with one belongs to that build.
	ExtendedIDs []string
	// FamilyExecutables are the bundle executable names of Family builds.
	FamilyExecutables []string
}

func applyUninstallSiblingScan(result *UninstallResult, scan uninstallSiblingScan) {
	if result == nil {
		return
	}
	if len(scan.Paths) > 0 {
		result.SharedBundleSibling = true
	}
	if !scan.Complete {
		result.SharedBundleUncertain = true
	}
	result.SiblingPaths = uniqueSortedStrings(append(result.SiblingPaths, scan.Paths...))
	result.FamilyBuilds = uniqueSortedStrings(append(result.FamilyBuilds, scan.Family...))
}

// familyScanAddsBuilds reports whether live found a family build, or a
// relation to one, that the plan built from planned did not account for.
func familyScanAddsBuilds(planned, live uninstallSiblingScan) bool {
	if live.NameFamily && !planned.NameFamily {
		return true
	}
	for _, path := range live.Family {
		if !containsString(planned.Family, path) {
			return true
		}
	}
	for _, id := range live.ExtendedIDs {
		if !containsString(planned.ExtendedIDs, id) {
			return true
		}
	}
	return false
}

func familyZapRefusal(builds []string) error {
	return errors.New("refusing Homebrew --zap because another installed build of this app may share its data: " +
		strings.Join(builds, ", "))
}

// appChannelWords name the release channels that tell builds of one app
// apart. "Edge" is left out: "Microsoft Edge" would lose its own product name.
var appChannelWords = []string{
	"Developer Edition", "Technology Preview",
	"Nightly", "Beta", "Alpha", "Dev", "Canary", "Preview", "Insiders", "Insider",
	"Stable", "Release", "RC", "LTS",
}

// appFamilyBaseName strips one trailing release-channel word, separated by a
// space or " - ", so every build of an app shares one base name: "Firefox
// Nightly" and "Firefox" are both "Firefox", and "Visual Studio Code -
// Insiders" is "Visual Studio Code". A name without a channel word, or one
// whose base would be shorter than a leftover name token, is its own base.
func appFamilyBaseName(name string) string {
	name = strings.TrimSpace(name)
	for _, word := range appChannelWords {
		if len(name) <= len(word) || !strings.EqualFold(name[len(name)-len(word):], word) {
			continue
		}
		prefix := name[:len(name)-len(word)]
		if !strings.HasSuffix(prefix, " ") {
			continue
		}
		base := strings.TrimSpace(prefix)
		base = strings.TrimSpace(strings.TrimSuffix(base, " -"))
		if utf8.RuneCountInString(base) >= 3 {
			return base
		}
	}
	return name
}

// familyNameVariants returns the literal leftover names, lowercased, that
// every build sharing name's base uses: the base and its no-space, hyphen,
// and underscore spellings.
func familyNameVariants(name string) []string {
	base := appFamilyBaseName(name)
	if utf8.RuneCountInString(base) < 3 {
		return nil
	}
	lower := strings.ToLower(base)
	return uniqueStrings([]string{
		lower,
		strings.ReplaceAll(lower, " ", ""),
		strings.ReplaceAll(lower, " ", "-"),
		strings.ReplaceAll(lower, " ", "_"),
	})
}

type bundleIDRelation int

const (
	bundleIDUnrelated bundleIDRelation = iota
	bundleIDExtendedBySelected
	bundleIDExtendsSelected
	bundleIDSame
)

// relateBundleIDs compares a candidate's bundle ID with the selected one on
// whole dot-delimited components, ASCII case-insensitively. Whichever ID is
// the shorter must keep three components, so a vendor prefix such as
// com.google never makes the vendor's other apps builds of one another.
func relateBundleIDs(candidate, selected string) bundleIDRelation {
	if !looksLikeBundleID(candidate) || !looksLikeBundleID(selected) {
		return bundleIDUnrelated
	}
	if bundleIDEqual(candidate, selected) {
		return bundleIDSame
	}
	c, s := asciiLower(candidate), asciiLower(selected)
	if strings.HasPrefix(c, s+".") && strings.Count(s, ".") >= 2 {
		return bundleIDExtendsSelected
	}
	if strings.HasPrefix(s, c+".") && strings.Count(c, ".") >= 2 {
		return bundleIDExtendedBySelected
	}
	return bundleIDUnrelated
}

// bundleIDFamilyNeedles are the identifiers whose bytes a related binary
// plist must contain: the selected ID, which every ID extending it contains,
// and each parent ID of at least three components it could extend.
func bundleIDFamilyNeedles(bundleID string) []string {
	needles := []string{bundleID}
	parts := strings.Split(bundleID, ".")
	for n := len(parts) - 1; n >= 3; n-- {
		needles = append(needles, strings.Join(parts[:n], "."))
	}
	return needles
}

func sharedBundleZapRefusal(app AppEntry, result UninstallResult) error {
	if len(result.SiblingPaths) > 0 {
		return errors.New("refusing Homebrew --zap because another installed app shares bundle ID " +
			app.BundleID + ": " + strings.Join(result.SiblingPaths, ", "))
	}
	return errors.New("refusing Homebrew --zap because Bloom could not verify that no other installed app shares bundle ID " + app.BundleID)
}

func uninstallAppIdentityMatches(app AppEntry, expectedIdentity, expectedBundleID string) bool {
	if expectedIdentity == "" || cleanPathIdentity(app.Path) != expectedIdentity {
		return false
	}
	if expectedBundleID == "" {
		return true
	}
	return bundleIDEqual(readBundleID(app.Path), expectedBundleID)
}

// pinUninstallAppIdentity keeps the selected bundle's inode alive while the
// uninstall plan is built. Without the open directory descriptor, a
// delete-and-recreate race can reuse the same device/inode pair before the
// action-boundary check and make a replacement look like the selected app.
// Symlinks retain the existing lstat-based checks because os.Open follows
// them rather than pinning the link itself.
func pinUninstallAppIdentity(path, expectedIdentity string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if expectedIdentity == "" || cleanPathIdentity(path) != expectedIdentity {
		return nil, errors.New("app bundle changed during uninstall")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, nil
	}

	anchor, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	anchoredInfo, err := anchor.Stat()
	if err != nil {
		_ = anchor.Close()
		return nil, err
	}
	if !os.SameFile(info, anchoredInfo) || cleanPathIdentity(path) != expectedIdentity {
		_ = anchor.Close()
		return nil, errors.New("app bundle changed during uninstall")
	}
	return anchor, nil
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// scanUninstallBundleSiblings checks Bloom's application roots for another
// .app bundle with the same CFBundleIdentifier. A failed root traversal makes
// the result incomplete so callers can preserve shared data conservatively.
func scanUninstallBundleSiblings(ctx context.Context, app AppEntry) uninstallSiblingScan {
	result := uninstallSiblingScan{Complete: true}
	bundleID := strings.TrimSpace(app.BundleID)
	validID := looksLikeBundleID(bundleID)
	selectedBase := ""
	if leftoverNameToken(app) != "" {
		selectedBase = appFamilyBaseName(app.Name)
	}
	if !validID && selectedBase == "" {
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Complete = false
		return result
	}
	needles := []string(nil)
	if validID {
		needles = bundleIDFamilyNeedles(bundleID)
	}
	addFamily := func(candidate string) {
		if !containsString(result.Family, candidate) {
			result.Family = append(result.Family, candidate)
			if executable := readBundleExecutable(candidate); executable != "" {
				result.FamilyExecutables = append(result.FamilyExecutables, executable)
			}
		}
	}

	scanCtx, cancel := context.WithTimeout(ctx, uninstallSiblingScanTimeout)
	defer cancel()
	home, _ := os.UserHomeDir()
	roots := expandedUninstallSiblingRoots(home, app.Path)
	selectedIdentity := cleanPathIdentity(app.Path)
	selectedResolved := resolvedAppBundlePath(app.Path)
	seenCandidates := map[string]bool{}
	seenMatches := map[string]bool{}
	candidates := 0

	var inspectCandidate func(string)
	inspectCandidate = func(candidate string) {
		if !result.Complete || scanCtx.Err() != nil {
			result.Complete = false
			return
		}
		candidate = filepath.Clean(candidate)
		if seenCandidates[candidate] || isProtectedAppPath(candidate) {
			return
		}
		seenCandidates[candidate] = true
		candidates++
		if candidates > uninstallSiblingCandidateCap {
			result.Complete = false
			return
		}
		if sameAppBundle(candidate, app.Path, selectedIdentity, selectedResolved) {
			return
		}
		relation := bundleIDUnrelated
		candidateID := ""
		if validID {
			ids, known := candidateRelatedBundleIDs(scanCtx, candidate, needles)
			if !known {
				result.Complete = false
				return
			}
			for _, id := range ids {
				if rel := relateBundleIDs(id, bundleID); rel > relation {
					relation, candidateID = rel, id
				}
			}
		}
		if relation == bundleIDSame {
			if !seenMatches[candidate] {
				seenMatches[candidate] = true
				result.Paths = append(result.Paths, candidate)
			}
			return
		}
		if relation == bundleIDExtendsSelected {
			addFamily(candidate)
			if !containsString(result.ExtendedIDs, candidateID) {
				result.ExtendedIDs = append(result.ExtendedIDs, candidateID)
			}
		}
		if relation == bundleIDExtendedBySelected {
			addFamily(candidate)
		}
		if selectedBase != "" && strings.EqualFold(appFamilyBaseName(appBundleBaseName(candidate)), selectedBase) {
			addFamily(candidate)
			result.NameFamily = true
		}
	}

	for _, root := range roots {
		if !result.Complete {
			break
		}
		info, err := os.Stat(root.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() {
			result.Complete = false
			break
		}
		entries, err := os.ReadDir(root.path)
		if err != nil {
			result.Complete = false
			break
		}
		for _, entry := range entries {
			if scanCtx.Err() != nil {
				result.Complete = false
				break
			}
			name := entry.Name()
			path := filepath.Join(root.path, name)
			if strings.HasSuffix(strings.ToLower(name), ".app") {
				inspectCandidate(path)
				continue
			}
			if !root.recurseOneLevel || !entry.IsDir() || strings.HasPrefix(name, ".") {
				continue
			}
			children, err := os.ReadDir(path)
			if err != nil {
				result.Complete = false
				break
			}
			for _, child := range children {
				if strings.HasSuffix(strings.ToLower(child.Name()), ".app") {
					inspectCandidate(filepath.Join(path, child.Name()))
				}
			}
		}
	}

	if scanCtx.Err() != nil {
		result.Complete = false
	}
	sort.Strings(result.Paths)
	sort.Strings(result.Family)
	sort.Strings(result.ExtendedIDs)
	return result
}

type uninstallSiblingRoot struct {
	path            string
	recurseOneLevel bool
}

func expandedUninstallSiblingRoots(home, selectedPath string) []uninstallSiblingRoot {
	roots := make([]uninstallSiblingRoot, 0, len(uninstallSiblingAppDirs)+1)
	seen := map[string]bool{}
	add := func(path string, recurse bool) {
		if strings.HasPrefix(path, "~") && home != "" {
			path = filepath.Join(home, path[1:])
		}
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		seen[path] = true
		roots = append(roots, uninstallSiblingRoot{path: path, recurseOneLevel: recurse})
	}
	for _, dir := range uninstallSiblingAppDirs {
		add(dir, filepath.Clean(dir) == "/Applications")
	}

	selected := filepath.Clean(selectedPath)
	insideRoot := false
	for _, root := range roots {
		if pathIsWithinRoot(selected, root.path) {
			insideRoot = true
			break
		}
	}
	if !insideRoot && selected != "" && selected != "." {
		add(filepath.Dir(selected), false)
	}
	return roots
}

func sameAppBundle(candidate, selected, selectedIdentity, selectedResolved string) bool {
	if filepath.Clean(candidate) == filepath.Clean(selected) {
		return true
	}
	if selectedIdentity != "" && cleanPathIdentity(candidate) == selectedIdentity {
		return true
	}
	resolved := resolvedAppBundlePath(candidate)
	return selectedResolved != "" && resolved != "" && resolved == selectedResolved
}

func resolvedAppBundlePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(resolved)
}

// candidateRelatedBundleIDs returns the bundle IDs of appPath's Info.plists
// that could relate to the selected app, avoiding plutil for every installed
// app: XML plists are parsed directly, and a binary plist goes to plutil only
// when its bytes contain one of needles (needles[0] is the selected ID).
// known is false while any plist could not be judged, unless one already
// names the selected ID: that makes the candidate a same-ID copy, the most
// conservative answer, whatever the unread plist says. Any other ID leaves
// the candidate undecided, because the unread plist might name it.
func candidateRelatedBundleIDs(ctx context.Context, appPath string, needles []string) (ids []string, known bool) {
	plists, complete := bundleInfoPlistCandidates(appPath)
	if len(plists) == 0 {
		return nil, complete
	}
	known = complete
	for _, plist := range plists {
		id, plistKnown := plistRelatedBundleID(ctx, plist, needles)
		if id != "" {
			ids = append(ids, id)
			if len(needles) > 0 && bundleIDEqual(id, needles[0]) {
				return ids, true
			}
		}
		if !plistKnown {
			known = false
		}
	}
	return ids, known
}

func bundleInfoPlistCandidates(appPath string) ([]string, bool) {
	const maxWrappedBundles = 32
	complete := true
	seen := map[string]bool{}
	paths := []string{}
	addIfPresent := func(path string) {
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil || !info.Mode().IsRegular() {
			complete = false
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}

	addIfPresent(filepath.Join(appPath, "Contents", "Info.plist"))
	addIfPresent(filepath.Join(appPath, "WrappedBundle", "Info.plist"))
	wrapper := filepath.Join(appPath, "Wrapper")
	entries, err := os.ReadDir(wrapper)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			complete = false
		}
		return paths, complete
	}
	wrapped := 0
	for _, entry := range entries {
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".app") {
			continue
		}
		wrapped++
		if wrapped > maxWrappedBundles {
			complete = false
			break
		}
		addIfPresent(filepath.Join(wrapper, entry.Name(), "Info.plist"))
	}
	return paths, complete
}

// plistRelatedBundleID returns the plist's CFBundleIdentifier, or "" when it
// has none or, for a binary plist, when its bytes name none of needles.
// known is false when the plist could not be judged.
func plistRelatedBundleID(ctx context.Context, plist string, needles []string) (id string, known bool) {
	info, err := os.Stat(plist)
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > uninstallSiblingInfoPlistLimit {
		return "", false
	}
	data, err := os.ReadFile(plist)
	if err != nil {
		return "", false
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("<?xml")) || bytes.HasPrefix(trimmed, []byte("<plist")) {
		if !validXMLPlistData(data) {
			return "", false
		}
		id := readXMLPlistStringData(data, "CFBundleIdentifier")
		if !looksLikeBundleID(id) {
			return "", true
		}
		return id, true
	}
	if !bytes.HasPrefix(data, []byte("bplist")) {
		if id := readBundleIDFromPlist(ctx, plist); id != "" {
			return id, true
		}
		return "", plistLintSucceeds(ctx, plist)
	}
	lowerData := bytes.ToLower(data)
	mentioned := false
	for _, needle := range needles {
		needle = strings.ToLower(needle)
		if bytes.Contains(lowerData, []byte(needle)) || bytes.Contains(lowerData, utf16BEASCII(needle)) {
			mentioned = true
			break
		}
	}
	if !mentioned {
		return "", true
	}
	id = readBundleIDFromPlist(ctx, plist)
	if id == "" {
		return "", false
	}
	return id, true
}

func validXMLPlistData(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	sawPlist := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return sawPlist
		}
		if err != nil {
			return false
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local == "plist" {
			sawPlist = true
		}
	}
}

func plistLintSucceeds(ctx context.Context, plist string) bool {
	cmd := exec.CommandContext(ctx, "/usr/bin/plutil", "-lint", plist)
	return cmd.Run() == nil && ctx.Err() == nil
}

func readXMLPlistStringData(data []byte, key string) string {
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

func utf16BEASCII(value string) []byte {
	out := make([]byte, 0, len(value)*2)
	for i := 0; i < len(value); i++ {
		out = append(out, 0, value[i])
	}
	return out
}
