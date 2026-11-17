package bloom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// caskZapInfoTimeout bounds the brew info call that reads a cask's current
// definition.
const caskZapInfoTimeout = 30 * time.Second

// caskReceiptMaxBytes bounds the install receipt Bloom reads.
const caskReceiptMaxBytes = 8 << 20

// Stanzas whose trash and delete entries Bloom judges. Homebrew runs the
// uninstall stanza on every brew uninstall and adds the zap stanza for
// --zap (Installer#uninstall_artifacts).
const (
	caskStanzaUninstall = "uninstall"
	caskStanzaZap       = "zap"
)

// zapReachRoot is a folder that holds the user's own files or protected
// data. A cask step that would trash or delete anything in it, or the folder
// itself, keeps Bloom from running brew uninstall. uninstallExempt names
// paths inside the root, relative to it, that the owning cask's own
// uninstall stanza may remove: Steam's deletes its client bundle,
// Steam.AppBundle, inside the library folder.
type zapReachRoot struct {
	path            string
	reason          string
	uninstallExempt []string
}

func zapReachRoots(home string) []zapReachRoot {
	var roots []zapReachRoot
	for _, name := range []string{"Desktop", "Documents", "Downloads", "Movies", "Music", "Pictures", "Public"} {
		roots = append(roots, zapReachRoot{path: filepath.Join(home, name), reason: "in your " + name + " folder"})
	}
	roots = append(roots,
		zapReachRoot{path: filepath.Join(home, "Library", "Mobile Documents"), reason: "in iCloud Drive"},
		zapReachRoot{path: filepath.Join(home, "Library", "CloudStorage"), reason: "in a cloud storage folder"},
		// Configuration Bloom's own discovery never touches.
		zapReachRoot{path: filepath.Join(home, ".config"), reason: "in your ~/.config folder"},
	)
	for _, root := range protectedUninstallDataRoots(home) {
		roots = append(roots, zapReachRoot{path: root.path, reason: root.reason, uninstallExempt: root.uninstallExempt})
	}
	return roots
}

// rootsForUninstallEntry leaves out a root whose uninstallExempt path holds
// the uninstall entry's path. Every other root still judges the entry, so a
// client bundle linked into Documents is still refused.
func rootsForUninstallEntry(pattern, home string, roots []zapReachRoot) []zapReachRoot {
	expanded := expandHomebrewHomePlaceholder(pattern, home)
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		expanded = home + expanded[1:]
	}
	var out []zapReachRoot
	for _, root := range roots {
		exempt := false
		for _, rel := range root.uninstallExempt {
			if pathAtOrBelowFold(expanded, filepath.Join(root.path, rel)) {
				exempt = true
				break
			}
		}
		if !exempt {
			out = append(out, root)
		}
	}
	return out
}

// caskRemovalPattern is one trash or delete path of a cask and the stanza
// that holds it.
type caskRemovalPattern struct {
	path   string
	stanza string
}

// caskReach is a protected path a cask step would take.
type caskReach struct {
	UninstallKeptPath
	stanza string
}

// caskZapReachedData lists what the cask's uninstall and zap steps would
// trash or delete among the user's own folders, iCloud Drive, cloud storage
// folders, and protected data. Zap stanzas reach well past an app's Library
// data: every Ableton Live cask trashes ~/Music/Ableton, and Actual's trashes
// ~/Documents/Actual, where its budget files live. A few uninstall stanzas
// do the same on a plain uninstall.
//
// brew uninstall runs the installed definition: the artifacts recorded in
// the cask's install receipt or its installed caskfile, otherwise the
// current definition. Every one of them that exists is read and their
// targets combined. A source that cannot be read, an installed Ruby caskfile
// with no recorded artifacts, or no readable source at all is an error: an
// unknown step must not run.
func caskZapReachedData(ctx context.Context, runner Runner, token string) ([]caskReach, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, errors.New("home folder unknown")
	}
	patterns, err := caskRemovalPatterns(ctx, runner, token)
	if err != nil {
		return nil, err
	}
	roots := zapReachRoots(home)
	var reached []caskReach
	seen := map[string]bool{}
	for _, pattern := range patterns {
		patternRoots := roots
		if pattern.stanza == caskStanzaUninstall {
			patternRoots = rootsForUninstallEntry(pattern.path, home, roots)
		}
		for _, item := range zapPatternReach(pattern.path, home, patternRoots) {
			key := pattern.stanza + "\x00" + item.Path
			if !seen[key] {
				seen[key] = true
				reached = append(reached, caskReach{UninstallKeptPath: item, stanza: pattern.stanza})
			}
		}
	}
	return reached, nil
}

// caskZapDecision judges the cask's uninstall and zap steps. An error
// refuses the cask: its uninstall stanza, which runs with or without --zap,
// reaches the user's files, or the steps cannot be read. Otherwise it lists
// what only the zap would take; Homebrew's uninstall then runs without
// --zap, and Bloom's own leftover list, which keeps all of it, cleans up.
func caskZapDecision(ctx context.Context, runner Runner, token string) ([]UninstallKeptPath, error) {
	reached, err := caskZapReachedData(ctx, runner, token)
	if err != nil {
		return nil, err
	}
	var byUninstall, byZap []UninstallKeptPath
	seen := map[string]bool{}
	for _, item := range reached {
		key := item.stanza + "\x00" + item.Path
		if seen[key] {
			continue
		}
		seen[key] = true
		if item.stanza == caskStanzaUninstall {
			byUninstall = append(byUninstall, item.UninstallKeptPath)
		} else {
			byZap = append(byZap, item.UninstallKeptPath)
		}
	}
	if len(byUninstall) > 0 {
		return nil, caskUninstallStepsRefusal(token, byUninstall)
	}
	return byZap, nil
}

// caskUninstallStepsRefusal refuses a cask whose uninstall stanza reaches
// the user's files. Leaving out --zap would not help: that stanza runs on
// every brew uninstall.
func caskUninstallStepsRefusal(cask string, items []UninstallKeptPath) error {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, item.Path+" ("+item.Reason+")")
	}
	return fmt.Errorf("refusing to uninstall Homebrew cask %s because its uninstall steps, which run with or without --zap, would also remove %s; move that data elsewhere first",
		cask, strings.Join(parts, ", "))
}

func caskZapReadError(token string, err error) error {
	return fmt.Errorf("could not read the uninstall and zap steps of Homebrew cask %s, so Bloom did not run brew uninstall (%v); retry, or check them with brew info --cask %s before uninstalling it yourself", token, err, token)
}

// caskRemovalPatterns returns the trash and delete paths of the cask's
// uninstall and zap stanzas from its installed definition and its current
// one. rmdir entries are left out: Homebrew removes those folders only when
// they hold nothing but empty folders and .DS_Store files. script entries
// run a vendor's own cleanup tool and are not judged either.
func caskRemovalPatterns(ctx context.Context, runner Runner, token string) ([]caskRemovalPattern, error) {
	patterns, sources, err := installedCaskRemovalPatterns(token)
	if err != nil {
		return nil, caskZapReadError(token, err)
	}
	current, infoErr := brewInfoCaskDefinition(ctx, runner, token)
	if infoErr == nil {
		found, err := definitionRemovalPatterns(current)
		if err != nil {
			return nil, caskZapReadError(token, fmt.Errorf("brew info: %w", err))
		}
		patterns = append(patterns, found...)
		sources++
	}
	if sources == 0 {
		return nil, caskZapReadError(token, infoErr)
	}
	return patterns, nil
}

// installedCaskRemovalPatterns reads the paths each standard Caskroom
// records for token, and how many sources held artifacts: the receipt's
// uninstall_artifacts (.metadata/INSTALL_RECEIPT.json) and every installed
// caskfile (.metadata/<version>/<timestamp>/Casks/<token>.json, or a legacy
// .internal.json in Homebrew's internal encoding). Current Homebrew writes
// compact caskfiles without artifacts and records them in the receipt; older
// installs kept the whole definition in the caskfile, per-platform
// variations and per-language overrides included. A Ruby caskfile cannot be
// read here, so it is accepted only when its receipt recorded the artifacts
// at install time. Every Caskroom counts, because the brew on PATH decides
// which one the uninstall uses, and refusing on the other one is the safe
// side.
func installedCaskRemovalPatterns(token string) ([]caskRemovalPattern, int, error) {
	if !isValidCaskToken(token) {
		return nil, 0, fmt.Errorf("invalid cask token %q", token)
	}
	var patterns []caskRemovalPattern
	sources := 0
	for _, room := range caskroomDirs {
		receipt, err := readCaskMetadataJSON(filepath.Join(room, token, ".metadata", "INSTALL_RECEIPT.json"))
		if err != nil {
			return nil, 0, fmt.Errorf("install receipt: %w", err)
		}
		recorded := false
		if fields, ok := receipt.(map[string]any); ok {
			if artifacts := fields["uninstall_artifacts"]; jsonListHasItems(artifacts) {
				found, err := artifactListRemovalPatterns(artifacts)
				if err != nil {
					return nil, 0, fmt.Errorf("install receipt: %w", err)
				}
				patterns = append(patterns, found...)
				sources++
				recorded = true
			}
		} else if receipt != nil {
			return nil, 0, errors.New("install receipt is not a JSON object")
		}
		caskfiles, err := filepath.Glob(filepath.Join(room, token, ".metadata", "*", "*", "Casks", "*"))
		if err != nil {
			return nil, 0, fmt.Errorf("installed caskfile: %w", err)
		}
		for _, caskfile := range caskfiles {
			switch filepath.Base(caskfile) {
			case token + ".json", token + ".internal.json":
				definition, err := readCaskMetadataJSON(caskfile)
				if err != nil {
					return nil, 0, fmt.Errorf("installed caskfile %s: %w", caskfile, err)
				}
				fields, ok := definition.(map[string]any)
				if !ok {
					if definition == nil {
						continue
					}
					return nil, 0, fmt.Errorf("installed caskfile %s is not a JSON object", caskfile)
				}
				found, err := definitionRemovalPatterns(fields)
				if err != nil {
					return nil, 0, fmt.Errorf("installed caskfile %s: %w", caskfile, err)
				}
				patterns = append(patterns, found...)
				if jsonListHasItems(fields["artifacts"]) || jsonListHasItems(fields["raw_artifacts"]) {
					sources++
				}
			case token + ".rb":
				if !recorded {
					return nil, 0, fmt.Errorf("installed caskfile %s is Ruby and its receipt records no uninstall artifacts", caskfile)
				}
			}
		}
	}
	return patterns, sources, nil
}

// readCaskMetadataJSON decodes a small regular file under a Caskroom. A
// missing file is nil without an error.
func readCaskMetadataJSON(path string) (any, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, caskReceiptMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > caskReceiptMaxBytes {
		return nil, fmt.Errorf("too large: %s", path)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func jsonListHasItems(value any) bool {
	list, ok := value.([]any)
	return ok && len(list) > 0
}

// brewInfoCaskDefinition returns the cask's current definition from brew
// info --json=v2.
func brewInfoCaskDefinition(ctx context.Context, runner Runner, token string) (any, error) {
	infoCtx, cancel := context.WithTimeout(ctx, caskZapInfoTimeout)
	defer cancel()
	out := runner.Run(infoCtx, "brew", "info", "--json=v2", "--cask", token)
	if infoCtx.Err() != nil {
		return nil, fmt.Errorf("brew info --json=v2 --cask %s did not finish: %w", token, infoCtx.Err())
	}
	if out.Err != nil {
		return nil, fmt.Errorf("brew info --json=v2 --cask %s failed: %s", token, brewErrorLine(out))
	}
	var info struct {
		Casks []map[string]any `json:"casks"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &info); err != nil {
		return nil, fmt.Errorf("brew info --json=v2 --cask %s: %w", token, err)
	}
	for _, cask := range info.Casks {
		if cask["token"] == token || cask["full_token"] == token {
			return cask, nil
		}
	}
	return nil, fmt.Errorf("brew info --json=v2 --cask %s did not describe the cask", token)
}

// definitionRemovalPatterns collects the trash and delete paths of every
// uninstall and zap stanza in a cask definition, in each artifact list it
// holds: the top-level artifacts, per-platform variations, or per-language
// overrides. Homebrew's public encoding writes an artifact entry as
// {"zap": [{"trash": …}]}, its internal one as [":zap", args, {":trash": …},
// block] with empty parts dropped, symbols written with a leading colon, and
// a string that itself starts with a backslash or colon escaped by one more
// backslash. A stanza key outside an artifact list, such as a postflight
// step's uninstall flag, is not a stanza. Any stanza shape it cannot read is
// an error rather than "no targets".
func definitionRemovalPatterns(definition any) ([]caskRemovalPattern, error) {
	var patterns []caskRemovalPattern
	var walk func(value any) error
	walk = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				if isArtifactListKey(key) {
					found, err := artifactListRemovalPatterns(item)
					if err != nil {
						return err
					}
					patterns = append(patterns, found...)
					continue
				}
				if err := walk(item); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range v {
				if err := walk(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return patterns, walk(definition)
}

func isArtifactListKey(key string) bool {
	switch strings.TrimPrefix(key, ":") {
	case "artifacts", "raw_artifacts", "uninstall_artifacts":
		return true
	}
	return false
}

// artifactListRemovalPatterns reads the uninstall and zap entries of one
// artifact list, in either encoding.
func artifactListRemovalPatterns(list any) ([]caskRemovalPattern, error) {
	if list == nil {
		return nil, nil
	}
	entries, ok := list.([]any)
	if !ok {
		return nil, fmt.Errorf("artifact list holds %T", list)
	}
	var patterns []caskRemovalPattern
	for _, entry := range entries {
		switch e := entry.(type) {
		case map[string]any:
			for _, stanza := range []string{caskStanzaUninstall, caskStanzaZap} {
				if value, ok := e[stanza]; ok {
					found, err := publicStanzaPatterns(value, stanza)
					if err != nil {
						return nil, err
					}
					patterns = append(patterns, found...)
				}
			}
		case []any:
			if len(e) == 0 {
				continue
			}
			key, _ := e[0].(string)
			if stanza := strings.TrimPrefix(key, ":"); key != stanza && (stanza == caskStanzaUninstall || stanza == caskStanzaZap) {
				found, err := internalStanzaPatterns(e[1:], stanza)
				if err != nil {
					return nil, err
				}
				patterns = append(patterns, found...)
			}
		}
	}
	return patterns, nil
}

// publicStanzaPatterns reads one stanza in the public encoding: a list of
// directive objects, or a single one.
func publicStanzaPatterns(value any, stanza string) ([]caskRemovalPattern, error) {
	var directives []map[string]any
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			directive, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s stanza holds %T, not a directive object", stanza, item)
			}
			directives = append(directives, directive)
		}
	case map[string]any:
		directives = append(directives, v)
	default:
		return nil, fmt.Errorf("%s stanza holds %T", stanza, value)
	}
	var patterns []caskRemovalPattern
	for _, directive := range directives {
		for _, key := range []string{"trash", "delete"} {
			raw, ok := directive[key]
			if !ok {
				continue
			}
			paths, err := jsonPathList(raw)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", stanza, key, err)
			}
			for _, path := range paths {
				patterns = append(patterns, caskRemovalPattern{path: path, stanza: stanza})
			}
		}
	}
	return patterns, nil
}

// internalStanzaPatterns reads the parts after a stanza's key in the
// internal encoding; its directives sit in the kwargs object.
func internalStanzaPatterns(parts []any, stanza string) ([]caskRemovalPattern, error) {
	var patterns []caskRemovalPattern
	for _, part := range parts {
		kwargs, ok := part.(map[string]any)
		if !ok {
			// Positional args or the block placeholder.
			continue
		}
		for _, key := range []string{":trash", ":delete"} {
			raw, ok := kwargs[key]
			if !ok {
				continue
			}
			paths, err := jsonPathList(raw)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", stanza, key, err)
			}
			for _, path := range paths {
				if strings.HasPrefix(path, ":") {
					return nil, fmt.Errorf("%s %s holds a symbol, not a path: %s", stanza, key, path)
				}
				patterns = append(patterns, caskRemovalPattern{path: strings.TrimPrefix(path, `\`), stanza: stanza})
			}
		}
	}
	return patterns, nil
}

// jsonPathList reads a directive value written as one path or a list of
// paths.
func jsonPathList(value any) ([]string, error) {
	switch v := value.(type) {
	case string:
		return []string{v}, nil
	case []any:
		paths := make([]string, 0, len(v))
		for _, item := range v {
			path, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("path list holds %T", item)
			}
			paths = append(paths, path)
		}
		return paths, nil
	default:
		return nil, fmt.Errorf("value is %T, not a path or a list of paths", value)
	}
}

const zapBraceExpansionLimit = 256

// zapPatternReach returns what one zap path would take from the protected
// roots. Homebrew replaces its serialized home folder, /$HOME, when it loads
// a definition; at zap time it expands a leading ~, skips relative paths and
// . or .. segments, and globs the rest with Ruby's glob. Bloom expands braces
// itself, reads [!...] as Go's [^...], and globs each result with Go's glob,
// which agrees with Ruby's for the rest, backslash escapes included. A **
// pattern, which Ruby walks recursively, counts as reaching any root its
// literal text could name.
func zapPatternReach(pattern, home string, roots []zapReachRoot) []UninstallKeptPath {
	expanded := expandHomebrewHomePlaceholder(pattern, home)
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		expanded = home + expanded[1:]
	}
	if !filepath.IsAbs(expanded) {
		return nil
	}
	for _, segment := range strings.Split(expanded, "/") {
		if segment == "." || segment == ".." {
			return nil
		}
	}
	// Ruby expands braces even in a pattern without other glob characters.
	alternatives, ok := expandGlobBraces(expanded, zapBraceExpansionLimit)
	if !ok {
		return unexpandablePatternReach(expanded, roots)
	}
	var reached []UninstallKeptPath
	for _, alternative := range alternatives {
		reached = append(reached, zapAlternativeReach(goGlobNegation(alternative), roots)...)
	}
	return reached
}

// zapAlternativeReach judges one brace-free zap path.
func zapAlternativeReach(expanded string, roots []zapReachRoot) []UninstallKeptPath {
	// With a trailing slash Ruby's glob matches folders only, and rm or the
	// Trash then act on what a linked entry points to, so the entry is also
	// judged by where it resolves.
	followEntry := false
	for len(expanded) > 1 && strings.HasSuffix(expanded, "/") && !strings.HasSuffix(expanded, `\/`) {
		expanded = expanded[:len(expanded)-1]
		followEntry = true
	}
	// Only a whole ** component walks recursively. Inside a name Ruby reads
	// ** as *, and so does Go's Match.
	if hasRecursiveGlobComponent(expanded) {
		return recursiveGlobReach(expanded, roots)
	}
	// A backslash escapes the next character for Ruby's glob as for Go's, so
	// only a path without one names itself literally.
	if !globHasMeta(expanded) {
		if _, err := os.Lstat(expanded); err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
				return nil
			}
			// A path Bloom cannot check counts as present inside a root.
			return uncheckedPathReach(expanded, roots)
		}
		return existingPathReach(expanded, followEntry, roots)
	}
	// POSIX classes such as [[:alpha:]] are Ruby's alone.
	if strings.Contains(expanded, "[[:") {
		return unexpandablePatternReach(expanded, roots)
	}
	// Like Ruby's, this glob skips folders it cannot read, and Homebrew
	// expands the pattern with the same access Bloom has, so a folder hidden
	// from Bloom is hidden from the zap too.
	matches, err := globFold(expanded)
	if err != nil {
		return unexpandablePatternReach(expanded, roots)
	}
	var reached []UninstallKeptPath
	for _, match := range matches {
		reached = append(reached, existingPathReach(match, followEntry, roots)...)
	}
	return reached
}

// recursiveGlobReach judges a pattern with a whole ** component. Ruby walks
// everything below what the components before it match, following a link
// those components name, so each such head reaches a root that it lies in,
// spelled or resolved, or an existing root below it.
func recursiveGlobReach(pattern string, roots []zapReachRoot) []UninstallKeptPath {
	components := strings.Split(pattern, "/")
	cut := 0
	for cut < len(components) && components[cut] != "**" {
		cut++
	}
	head := strings.Join(components[:cut], "/")
	if head == "" {
		head = "/"
	}
	var heads []string
	if globHasMeta(head) {
		matches, err := globFold(head)
		if err != nil {
			return unexpandablePatternReach(pattern, roots)
		}
		heads = matches
	} else if _, err := os.Lstat(head); err == nil {
		heads = []string{head}
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
		return uncheckedPathReach(head, roots)
	}
	var reached []UninstallKeptPath
	for _, match := range heads {
		variants := []string{filepath.Clean(match)}
		resolved, err := filepath.EvalSymlinks(match)
		if err != nil {
			// A dangling head leads nowhere; one Bloom cannot resolve counts
			// as present.
			if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
				reached = append(reached, uncheckedPathReach(match, roots)...)
			}
			continue
		}
		variants = append(variants, resolved)
		// Ruby's ** does not follow a link, but the components after it can
		// match one and go on through it, so every link below the head counts
		// by where it leads. The walk starts at the resolved head, because
		// WalkDir does not descend into a root that is itself a link.
		targets, complete := linkTargetsBelow(resolved)
		if !complete {
			reached = append(reached, UninstallKeptPath{Path: pattern, Reason: "below a folder too large for Bloom to check"})
			continue
		}
		variants = append(variants, targets...)
		for _, root := range roots {
			if recursiveHeadReachesRoot(variants, root.path) {
				reached = append(reached, UninstallKeptPath{Path: pattern, Reason: root.reason})
			}
		}
	}
	return reached
}

// recursiveZapWalkLimit bounds the entries Bloom walks below a ** head.
var recursiveZapWalkLimit = 20000

// linkTargetsBelow returns where each link below dir resolves, walking
// without following links as Ruby's ** does. complete is false past
// recursiveZapWalkLimit entries. A folder the walk cannot read is skipped,
// as Ruby's glob skips it with the same access.
func linkTargetsBelow(dir string) ([]string, bool) {
	var targets []string
	visited := 0
	complete := true
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		visited++
		if visited > recursiveZapWalkLimit {
			complete = false
			return filepath.SkipAll
		}
		if path != dir && entry.Type()&fs.ModeSymlink != 0 {
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				targets = append(targets, resolved)
			}
		}
		return nil
	})
	return targets, complete
}

func recursiveHeadReachesRoot(heads []string, root string) bool {
	for _, rootVariant := range zapRootVariants(root) {
		for _, head := range heads {
			if pathAtOrBelowFold(head, rootVariant) {
				return true
			}
			if pathAtOrBelowFold(rootVariant, head) {
				if _, err := os.Lstat(rootVariant); err == nil {
					return true
				}
			}
		}
	}
	return false
}

// hasRecursiveGlobComponent reports a path component that is exactly **.
func hasRecursiveGlobComponent(pattern string) bool {
	for _, component := range strings.Split(pattern, "/") {
		if component == "**" {
			return true
		}
	}
	return false
}

// existingPathReach reports the roots an existing path lies in or holds,
// judged by its own spelling and by its parent folder's resolved location,
// since a trash or delete follows a symlinked parent but not the entry
// itself, and with followEntry by the entry's own resolved location too.
func existingPathReach(path string, followEntry bool, roots []zapReachRoot) []UninstallKeptPath {
	variants := []string{filepath.Clean(path)}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		variants = append(variants, filepath.Join(parent, filepath.Base(path)))
	}
	if followEntry {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			variants = append(variants, resolved)
		}
	}
	var reached []UninstallKeptPath
	for _, root := range roots {
		for _, rootVariant := range zapRootVariants(root.path) {
			hit := false
			for _, variant := range variants {
				if pathAtOrBelowFold(variant, rootVariant) || pathAtOrBelowFold(rootVariant, variant) {
					hit = true
					break
				}
			}
			if hit {
				reached = append(reached, UninstallKeptPath{Path: filepath.Clean(path), Reason: root.reason})
				break
			}
		}
	}
	return reached
}

// uncheckedPathReach reports the roots a path that could not be checked lies
// in or holds.
func uncheckedPathReach(path string, roots []zapReachRoot) []UninstallKeptPath {
	var reached []UninstallKeptPath
	for _, root := range roots {
		if pathAtOrBelowFold(path, root.path) || pathAtOrBelowFold(root.path, path) {
			reached = append(reached, UninstallKeptPath{Path: filepath.Clean(path), Reason: root.reason})
		}
	}
	return reached
}

// unexpandablePatternReach judges a pattern by its literal text before the
// first glob character, spelled as written and with its folder resolved: it
// reaches a root that it lies in or that its expansion could produce.
func unexpandablePatternReach(pattern string, roots []zapReachRoot) []UninstallKeptPath {
	literal := pattern
	if idx := strings.IndexAny(pattern, "*?[{"); idx >= 0 {
		literal = pattern[:idx]
	}
	literal = unescapeGlobLiteral(literal)
	literals := []string{literal}
	if slash := strings.LastIndex(literal, "/"); slash > 0 {
		if folder, err := filepath.EvalSymlinks(literal[:slash]); err == nil {
			literals = append(literals, folder+literal[slash:])
		}
	}
	var reached []UninstallKeptPath
	for _, root := range roots {
		if literalPrefixReaches(literals, zapRootVariants(root.path)) {
			reached = append(reached, UninstallKeptPath{Path: pattern, Reason: root.reason})
		}
	}
	return reached
}

func literalPrefixReaches(literals, rootVariants []string) bool {
	for _, literal := range literals {
		literal = strings.ToLower(literal)
		for _, root := range rootVariants {
			root = strings.ToLower(filepath.Clean(root))
			if literal == root || strings.HasPrefix(literal, root+"/") || strings.HasPrefix(root, literal) {
				return true
			}
		}
	}
	return false
}

// globFold expands pattern like filepath.Glob, but compares wildcard
// components case-insensitively, as Ruby's glob does on a case-insensitive
// volume such as the default APFS one. Literal components are looked up as
// written, which the volume already folds.
func globFold(pattern string) ([]string, error) {
	if _, err := filepath.Match(pattern, ""); err != nil {
		return nil, err
	}
	if !globHasMeta(pattern) {
		if _, err := os.Lstat(pattern); err != nil {
			return nil, nil
		}
		return []string{pattern}, nil
	}
	dir, file := filepath.Split(pattern)
	if dir != string(os.PathSeparator) {
		dir = strings.TrimSuffix(dir, string(os.PathSeparator))
	}
	if !globHasMeta(dir) {
		return globFoldDir(dir, file, nil)
	}
	if dir == pattern {
		return nil, filepath.ErrBadPattern
	}
	parents, err := globFold(dir)
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, parent := range parents {
		if matches, err = globFoldDir(parent, file, matches); err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func globFoldDir(dir, pattern string, matches []string) ([]string, error) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return matches, nil
	}
	// Like Ruby's, a component without wildcards is looked up by name, which
	// the volume folds, non-ASCII letters included.
	if !strings.ContainsAny(pattern, "*?[") {
		path := filepath.Join(dir, unescapeGlobLiteral(pattern))
		if _, err := os.Lstat(path); err == nil {
			matches = append(matches, path)
		}
		return matches, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return matches, nil
	}
	folded := caseFoldGlobComponent(pattern)
	for _, entry := range entries {
		matched, err := filepath.Match(folded, entry.Name())
		if err != nil {
			return matches, err
		}
		if matched {
			matches = append(matches, filepath.Join(dir, entry.Name()))
		}
	}
	return matches, nil
}

// caseFoldGlobComponent rewrites each ASCII letter outside a bracket class
// as a [xX] class, and adds the other case of every letter and letter range
// to a positive class, so Go's Match folds case at least as widely as Ruby's
// glob does on a case-insensitive volume, where it folds ranges and
// literals. A negated class is copied unchanged: matching it exactly only
// matches more names.
func caseFoldGlobComponent(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '\\' && i+1 < len(pattern):
			if next := pattern[i+1]; isASCIILetter(next) {
				b.WriteString(asciiLetterClass(next))
			} else {
				b.WriteString(pattern[i : i+2])
			}
			i++
		case c == '[':
			class, end, ok := foldGlobClass(pattern, i)
			if !ok {
				// Unterminated; Match reports the bad pattern.
				b.WriteString(pattern[i:])
				return b.String()
			}
			b.WriteString(class)
			i = end
		case isASCIILetter(c):
			b.WriteString(asciiLetterClass(c))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// foldGlobClass reads the class that opens at pattern[i] and returns it,
// with the other case of its ASCII letters and letter ranges added when it
// is positive, and the index of its closing ]. ok is false for an
// unterminated class.
func foldGlobClass(pattern string, i int) (string, int, bool) {
	j := i + 1
	negated := j < len(pattern) && pattern[j] == '^'
	if negated {
		j++
	}
	type span struct{ lo, hi byte }
	var members []span
	first := true
	for j < len(pattern) && (pattern[j] != ']' || first) {
		lo, next, ok := globClassChar(pattern, j)
		if !ok {
			return "", 0, false
		}
		hi := lo
		j = next
		if j+1 < len(pattern) && pattern[j] == '-' && pattern[j+1] != ']' {
			if hi, next, ok = globClassChar(pattern, j+1); !ok {
				return "", 0, false
			}
			j = next
		}
		members = append(members, span{lo, hi})
		first = false
	}
	if j >= len(pattern) {
		return "", 0, false
	}
	class := pattern[i:j]
	if !negated {
		var extra strings.Builder
		for _, member := range members {
			for _, letters := range []span{{'a', 'z'}, {'A', 'Z'}} {
				lo, hi := max(member.lo, letters.lo), min(member.hi, letters.hi)
				if lo > hi {
					continue
				}
				extra.WriteByte(lo ^ 0x20)
				if hi > lo {
					extra.WriteByte('-')
					extra.WriteByte(hi ^ 0x20)
				}
			}
		}
		class += extra.String()
	}
	return class + "]", j, true
}

func globClassChar(pattern string, j int) (byte, int, bool) {
	if pattern[j] == '\\' {
		if j+1 >= len(pattern) {
			return 0, 0, false
		}
		return pattern[j+1], j + 2, true
	}
	return pattern[j], j + 1, true
}

func isASCIILetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func asciiLetterClass(c byte) string {
	lower, upper := c|0x20, c&^0x20
	return "[" + string(lower) + string(upper) + "]"
}

// globHasMeta matches filepath's own test: *, ?, [ or a backslash escape.
func globHasMeta(path string) bool {
	return strings.ContainsAny(path, `*?[\`)
}

// expandHomebrewHomePlaceholder replaces each /$HOME, the placeholder
// Homebrew serializes the home folder as, with home. A longer variable such
// as $HOMEBREW_PREFIX is left alone.
func expandHomebrewHomePlaceholder(path, home string) string {
	const placeholder = "/$HOME"
	var b strings.Builder
	for {
		idx := strings.Index(path, placeholder)
		if idx < 0 {
			b.WriteString(path)
			return b.String()
		}
		end := idx + len(placeholder)
		if end < len(path) && (path[end] == '_' || path[end] >= 'A' && path[end] <= 'Z') {
			b.WriteString(path[:end])
		} else {
			b.WriteString(path[:idx])
			b.WriteString(home)
		}
		path = path[end:]
	}
}

// goGlobNegation rewrites Ruby's unescaped [! set negation as Go's [^.
func goGlobNegation(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch {
		case pattern[i] == '\\' && i+1 < len(pattern):
			b.WriteString(pattern[i : i+2])
			i++
		case pattern[i] == '[' && i+1 < len(pattern) && pattern[i+1] == '!':
			b.WriteString("[^")
			i++
		default:
			b.WriteByte(pattern[i])
		}
	}
	return b.String()
}

// unescapeGlobLiteral drops the backslashes that escape characters in a
// glob's literal text.
func unescapeGlobLiteral(literal string) string {
	var b strings.Builder
	for i := 0; i < len(literal); i++ {
		if literal[i] == '\\' && i+1 < len(literal) {
			i++
		}
		b.WriteByte(literal[i])
	}
	return b.String()
}

// expandGlobBraces expands Ruby glob braces, nested ones included, into the
// patterns they stand for. A backslash escapes the next character. ok is
// false for an unbalanced brace or more than limit results.
func expandGlobBraces(pattern string, limit int) ([]string, bool) {
	start, depth := -1, 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				return nil, false
			}
			depth--
			if depth > 0 {
				continue
			}
			var out []string
			for _, alternative := range splitGlobBraceAlternatives(pattern[start+1 : i]) {
				expanded, ok := expandGlobBraces(pattern[:start]+alternative+pattern[i+1:], limit)
				if !ok || len(out)+len(expanded) > limit {
					return nil, false
				}
				out = append(out, expanded...)
			}
			return out, true
		}
	}
	if depth != 0 {
		return nil, false
	}
	return []string{pattern}, true
}

// splitGlobBraceAlternatives splits a brace body at its top-level commas.
func splitGlobBraceAlternatives(body string) []string {
	var parts []string
	depth, last := 0, 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, body[last:i])
				last = i + 1
			}
		}
	}
	return append(parts, body[last:])
}

func zapRootVariants(root string) []string {
	root = filepath.Clean(root)
	variants := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		variants = append(variants, resolved)
	}
	return variants
}

// pathAtOrBelowFold reports whether path is root or lies inside it, compared
// ASCII case-insensitively like the default APFS volume.
func pathAtOrBelowFold(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if strings.EqualFold(path, root) {
		return true
	}
	return len(path) > len(root) && path[len(root)] == os.PathSeparator && strings.EqualFold(path[:len(root)], root)
}
