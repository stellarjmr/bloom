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
	Paths    []string
	Complete bool
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
	if !looksLikeBundleID(bundleID) {
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Complete = false
		return result
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
		matches, known := candidateBundleIDMatches(scanCtx, candidate, bundleID)
		if !known {
			result.Complete = false
			return
		}
		if matches && !seenMatches[candidate] {
			seenMatches[candidate] = true
			result.Paths = append(result.Paths, candidate)
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

// candidateBundleIDMatches avoids spawning plutil for every installed app.
// XML plists are parsed directly. Binary plists are passed to plutil only when
// their bytes can contain the requested ASCII identifier.
func candidateBundleIDMatches(ctx context.Context, appPath, want string) (match, known bool) {
	plists, complete := bundleInfoPlistCandidates(appPath)
	if len(plists) == 0 {
		return false, complete
	}
	unknown := !complete
	for _, plist := range plists {
		matches, plistKnown := plistBundleIDMatches(ctx, plist, want)
		if matches {
			return true, true
		}
		if !plistKnown {
			unknown = true
		}
	}
	return false, !unknown
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

func plistBundleIDMatches(ctx context.Context, plist, want string) (match, known bool) {
	info, err := os.Stat(plist)
	if errors.Is(err, os.ErrNotExist) {
		return false, false
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > uninstallSiblingInfoPlistLimit {
		return false, false
	}
	data, err := os.ReadFile(plist)
	if err != nil {
		return false, false
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("<?xml")) || bytes.HasPrefix(trimmed, []byte("<plist")) {
		if !validXMLPlistData(data) {
			return false, false
		}
		id := readXMLPlistStringData(data, "CFBundleIdentifier")
		if id == "" || !looksLikeBundleID(id) {
			return false, true
		}
		return bundleIDEqual(id, want), true
	}
	if !bytes.HasPrefix(data, []byte("bplist")) {
		id := readBundleIDFromPlist(ctx, plist)
		if id != "" {
			return bundleIDEqual(id, want), true
		}
		return false, plistLintSucceeds(ctx, plist)
	}
	lowerData := bytes.ToLower(data)
	if !bytes.Contains(lowerData, []byte(strings.ToLower(want))) &&
		!bytes.Contains(lowerData, utf16BEASCII(strings.ToLower(want))) {
		return false, true
	}
	id := readBundleIDFromPlist(ctx, plist)
	if id == "" {
		return false, false
	}
	return bundleIDEqual(id, want), true
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
