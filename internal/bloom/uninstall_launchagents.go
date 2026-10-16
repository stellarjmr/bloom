package bloom

import (
	"bytes"
	"context"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	launchAgentPlistLimit          = 1 << 20
	launchAgentProgramProbeTimeout = 2 * time.Second
)

// LaunchAgent ownership follows launchd's own view of the job. A plist named
// exactly <bundleID>.plist is the app's own agent. Any other label is owned
// only when launchd would start an executable inside the selected bundle: the
// Program key, or ProgramArguments[0] when Program is absent. A label that
// merely extends the bundle ID, or a plist that mentions the app path in
// WatchPaths, arguments, or comments, does not establish ownership; Riot
// Client's install checker, for example, names League of Legends only in its
// WatchPaths.
type launchAgentOwner struct {
	bundleID string
	appPath  string
}

func newLaunchAgentOwner(app AppEntry) launchAgentOwner {
	return launchAgentOwner{bundleID: app.BundleID, appPath: app.Path}
}

func userLaunchAgentsDir(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents")
}

// matchOwnedLaunchAgents appends the user LaunchAgents that owner owns.
func matchOwnedLaunchAgents(ctx context.Context, home string, owner launchAgentOwner, out *[]string) {
	dir := userLaunchAgentsDir(home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		plist := filepath.Join(dir, entry.Name())
		if owner.owns(ctx, plist) {
			*out = append(*out, plist)
		}
	}
}

// owns reports whether plist is a regular, non-Apple LaunchAgent plist that
// belongs to the owner's app while the bundle still exists. Symlinked plists
// are never owned.
func (o launchAgentOwner) owns(ctx context.Context, plist string) bool {
	owned, _ := o.ownership(ctx, plist)
	return owned
}

// ownership is owns plus the launchd program that proved it; the program is
// empty when the exact <bundleID>.plist name alone establishes ownership.
func (o launchAgentOwner) ownership(ctx context.Context, plist string) (bool, string) {
	if !launchAgentPlistEligible(plist) {
		return false, ""
	}
	if o.namesAgent(plist) {
		return true, ""
	}
	program, ok := launchAgentProgram(ctx, plist)
	if !ok || !o.programInsideBundle(program) {
		return false, ""
	}
	return true, program
}

// stillOwns rechecks a planned agent at its final move, after the bundle has
// moved to Trash and containment can no longer be measured on disk: the
// plist must still be eligible and either carry the exact bundle-ID name or
// still name the very program that proved ownership while planning.
func (o launchAgentOwner) stillOwns(ctx context.Context, plist, plannedProgram string) bool {
	if !launchAgentPlistEligible(plist) {
		return false
	}
	if plannedProgram == "" {
		return o.namesAgent(plist)
	}
	program, ok := launchAgentProgram(ctx, plist)
	return ok && program == plannedProgram
}

// namesAgent compares ASCII case only: bundle IDs are ASCII, and Unicode
// folding would let a distinct file such as com.example.ſync.plist pass for
// com.example.sync.plist without its program being checked.
func (o launchAgentOwner) namesAgent(plist string) bool {
	return looksLikeBundleID(o.bundleID) &&
		asciiLower(filepath.Base(plist)) == asciiLower(o.bundleID+".plist")
}

func launchAgentPlistEligible(plist string) bool {
	name := strings.ToLower(filepath.Base(plist))
	if !strings.HasSuffix(name, ".plist") || strings.HasPrefix(name, "com.apple.") {
		return false
	}
	info, err := os.Lstat(plist)
	return err == nil && info.Mode().IsRegular()
}

// programInsideBundle reports whether program is an executable inside the
// owner's existing bundle. The path must run through the selected bundle
// path itself (or that path's symlink target or resolution): a link elsewhere
// that points into the bundle could be retargeted after planning without
// changing the plist. The spelling check folds case only as a pre-filter;
// the decision is filesystem identity. The program must be a regular file,
// and its directory, with symlinks resolved, must sit at or below the bundle
// by identity, so a case-variant spelling of the same bundle agrees while a
// symlinked folder inside the bundle cannot point at an outside executable.
func (o launchAgentOwner) programInsideBundle(program string) bool {
	if program == "" || o.appPath == "" || !filepath.IsAbs(program) ||
		hasDotDotComponent(program) || hasControlChar(program) {
		return false
	}
	program = filepath.Clean(program)
	throughBundle := false
	for _, bundle := range appBundlePathCandidates(o.appPath) {
		if hasPathPrefixFold(program, bundle) {
			throughBundle = true
			break
		}
	}
	if !throughBundle {
		return false
	}
	info, err := os.Lstat(program)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	bundle, err := os.Stat(o.appPath)
	if err != nil || !bundle.IsDir() {
		return false
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(program))
	if err != nil {
		return false
	}
	for {
		if current, err := os.Stat(dir); err == nil && os.SameFile(current, bundle) {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// hasPathPrefixFold reports whether path lies strictly below prefix, folding
// case the way a case-insensitive volume may. It only pre-filters; callers
// confirm with filesystem identity. Unicode normalization variants (NFC and
// NFD spellings of the same name) are not folded, so such an agent is left
// in place: a missed leftover, never a claimed foreign agent.
func hasPathPrefixFold(path, prefix string) bool {
	prefix = filepath.Clean(prefix)
	if prefix == string(os.PathSeparator) || len(path) <= len(prefix) || path[len(prefix)] != os.PathSeparator {
		return false
	}
	return strings.EqualFold(path[:len(prefix)], prefix)
}

// launchAgentProgram returns the executable launchd starts for plist: the
// Program key, else ProgramArguments[0].
func launchAgentProgram(ctx context.Context, plist string) (string, bool) {
	info, err := os.Lstat(plist)
	if err != nil || !info.Mode().IsRegular() || info.Size() > launchAgentPlistLimit {
		return "", false
	}
	data, err := os.ReadFile(plist)
	if err != nil {
		return "", false
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("<?xml")) || bytes.HasPrefix(trimmed, []byte("<!DOCTYPE")) ||
		bytes.HasPrefix(trimmed, []byte("<plist")) {
		return xmlLaunchAgentProgram(data)
	}
	return plutilLaunchAgentProgram(ctx, plist)
}

// plutilLaunchAgentProgram reads binary and other non-XML plists. A missing
// Program key falls back to ProgramArguments[0]; a timed-out or interrupted
// probe proves nothing, so it reports no program.
func plutilLaunchAgentProgram(ctx context.Context, plist string) (string, bool) {
	for _, key := range []string{"Program", "ProgramArguments.0"} {
		probeCtx, cancel := context.WithTimeout(ctx, launchAgentProgramProbeTimeout)
		out, err := exec.CommandContext(probeCtx, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", plist).Output()
		probeErr := probeCtx.Err()
		cancel()
		if probeErr != nil {
			return "", false
		}
		if err != nil {
			continue
		}
		program := strings.TrimSuffix(string(out), "\n")
		return program, program != ""
	}
	return "", false
}

// xmlLaunchAgentProgram reads the top-level Program string, else the first
// ProgramArguments string, from an XML plist. Nested dictionaries are skipped
// so a Program key inside another value cannot stand in for launchd's.
func xmlLaunchAgentProgram(data []byte) (string, bool) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	if !xmlNextStartElementIs(decoder, "plist") || !xmlNextStartElementIs(decoder, "dict") {
		return "", false
	}
	var program, firstArgument string
	hasProgram := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", false
		}
		switch element := token.(type) {
		case xml.EndElement:
			// Every child is consumed below, so this closes the top-level dict.
			if hasProgram {
				return program, program != ""
			}
			return firstArgument, firstArgument != ""
		case xml.StartElement:
			if element.Name.Local != "key" {
				if decoder.Skip() != nil {
					return "", false
				}
				continue
			}
			var key string
			if decoder.DecodeElement(&key, &element) != nil {
				return "", false
			}
			value, ok := xmlNextStartElement(decoder)
			if !ok {
				return "", false
			}
			switch {
			case key == "Program" && value.Name.Local == "string":
				if decoder.DecodeElement(&program, &value) != nil {
					return "", false
				}
				hasProgram = true
			case key == "ProgramArguments" && value.Name.Local == "array":
				first, ok := xmlFirstArrayString(decoder)
				if !ok {
					return "", false
				}
				firstArgument = first
			default:
				if decoder.Skip() != nil {
					return "", false
				}
			}
		}
	}
}

func xmlNextStartElementIs(decoder *xml.Decoder, name string) bool {
	element, ok := xmlNextStartElement(decoder)
	return ok && element.Name.Local == name
}

// xmlNextStartElement returns the next element start, skipping character
// data, comments, and directives. An element end first means a missing value.
func xmlNextStartElement(decoder *xml.Decoder) (xml.StartElement, bool) {
	for {
		token, err := decoder.Token()
		if err != nil {
			return xml.StartElement{}, false
		}
		switch element := token.(type) {
		case xml.StartElement:
			return element, true
		case xml.EndElement:
			return xml.StartElement{}, false
		}
	}
}

// xmlFirstArrayString consumes an <array> whose start was already read and
// returns its first element when that element is a <string>.
func xmlFirstArrayString(decoder *xml.Decoder) (string, bool) {
	first := ""
	seen := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", false
		}
		switch element := token.(type) {
		case xml.EndElement:
			return first, true
		case xml.StartElement:
			if !seen && element.Name.Local == "string" {
				if decoder.DecodeElement(&first, &element) != nil {
					return "", false
				}
			} else if decoder.Skip() != nil {
				return "", false
			}
			seen = true
		}
	}
}
