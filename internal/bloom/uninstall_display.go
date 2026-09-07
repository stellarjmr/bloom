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
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	appDisplayMetadataLimit   = 1 << 20
	appDisplayMetadataTimeout = 2 * time.Second
)

func (app AppEntry) displayName() string {
	if name := sanitizeAppDisplayName(app.DisplayName); name != "" {
		return name
	}
	if name := sanitizeAppDisplayName(app.Name); name != "" {
		return name
	}
	return sanitizeAppDisplayName(strings.TrimSuffix(filepath.Base(app.Path), ".app"))
}

func sanitizeAppDisplayName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "(null)" || utf8.RuneCountInString(value) > 256 {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			if unicode.IsSpace(r) {
				return ' '
			}
			return -1
		}
		return r
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func readPreferredAppleLanguages(ctx context.Context) []string {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, appDisplayMetadataTimeout)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, "/usr/bin/defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil || probeCtx.Err() != nil {
		return nil
	}
	return parseAppleLanguages(string(out))
}

func parseAppleLanguages(output string) []string {
	fields := strings.FieldsFunc(output, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("(),\"'", r)
	})
	seen := map[string]bool{}
	var languages []string
	for _, field := range fields {
		tag := canonicalLanguageTag(field)
		key := strings.ToLower(tag)
		if tag == "" || seen[key] {
			continue
		}
		seen[key] = true
		languages = append(languages, tag)
	}
	return languages
}

func canonicalLanguageTag(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "_", "-"))
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-') {
			return ""
		}
	}
	parts := strings.Split(value, "-")
	for i, part := range parts {
		if part == "" {
			return ""
		}
		part = strings.ToLower(part)
		switch {
		case i > 0 && len(part) == 4:
			part = strings.ToUpper(part[:1]) + part[1:]
		case i > 0 && (len(part) == 2 || len(part) == 3):
			part = strings.ToUpper(part)
		}
		parts[i] = part
	}
	return strings.Join(parts, "-")
}

func localizationDirectoryCandidates(language string) []string {
	tag := canonicalLanguageTag(language)
	if tag == "" {
		return nil
	}
	parts := strings.Split(tag, "-")
	base := parts[0]
	candidates := []string{tag, strings.ReplaceAll(tag, "-", "_")}
	if len(parts) > 1 && len(parts[1]) == 4 {
		candidates = append(candidates, base+"-"+parts[1], base+"_"+parts[1])
		if len(parts) > 2 {
			candidates = append(candidates, base+"_"+parts[2], base+"-"+parts[2])
		}
	} else if len(parts) > 1 {
		candidates = append(candidates, base+"_"+parts[1], base+"-"+parts[1])
	}
	candidates = append(candidates, base)
	if legacy := legacyLocalizationName(base); legacy != "" {
		candidates = append(candidates, legacy)
	}
	return uniqueFoldedStrings(candidates)
}

func legacyLocalizationName(base string) string {
	switch strings.ToLower(base) {
	case "en":
		return "English"
	case "de":
		return "German"
	case "es":
		return "Spanish"
	case "fr":
		return "French"
	case "it":
		return "Italian"
	case "ja":
		return "Japanese"
	case "ko":
		return "Korean"
	case "pt":
		return "Portuguese"
	case "ru":
		return "Russian"
	case "zh":
		return "Chinese"
	default:
		return ""
	}
}

func uniqueFoldedStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func readAppDisplayName(ctx context.Context, appPath string, preferredLanguages []string) string {
	fallback := sanitizeAppDisplayName(strings.TrimSuffix(filepath.Base(appPath), ".app"))
	plists, _ := bundleInfoPlistCandidates(appPath)
	for _, plist := range plists {
		values := readPlistStringValues(ctx, plist,
			"CFBundleDisplayName", "CFBundleName", "CFBundleDevelopmentRegion")
		unlocalized := sanitizeAppDisplayName(values["CFBundleDisplayName"])
		if unlocalized == "" {
			unlocalized = sanitizeAppDisplayName(values["CFBundleName"])
		}

		resources := bundleResourcesForInfoPlist(plist)
		lproj, matched := preferredLocalizationDirectory(resources, preferredLanguages, values["CFBundleDevelopmentRegion"])
		if matched {
			localizedValues := readPlistStringValues(ctx, filepath.Join(lproj, "InfoPlist.strings"),
				"CFBundleDisplayName", "CFBundleName")
			localized := sanitizeAppDisplayName(localizedValues["CFBundleDisplayName"])
			if localized == "" {
				localized = sanitizeAppDisplayName(localizedValues["CFBundleName"])
			}
			if localized != "" {
				return localized
			}
			if unlocalized != "" {
				return unlocalized
			}
			return fallback
		}
		if unlocalized != "" {
			return unlocalized
		}
	}
	return fallback
}

func bundleResourcesForInfoPlist(plist string) string {
	dir := filepath.Dir(plist)
	if strings.EqualFold(filepath.Base(dir), "Contents") {
		return filepath.Join(dir, "Resources")
	}
	return dir
}

func preferredLocalizationDirectory(resources string, languages []string, developmentRegion string) (string, bool) {
	info, err := os.Lstat(resources)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}
	developmentBase := languageBase(developmentRegion)
	for _, language := range languages {
		for _, candidate := range localizationDirectoryCandidates(language) {
			path := filepath.Join(resources, candidate+".lproj")
			if normalDirectory(path) {
				return path, true
			}
		}
		if developmentBase != "" && languageBase(language) == developmentBase {
			path := filepath.Join(resources, "Base.lproj")
			if normalDirectory(path) {
				return path, true
			}
		}
	}
	return "", false
}

func languageBase(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "english":
		value = "en"
	case "german":
		value = "de"
	case "spanish":
		value = "es"
	case "french":
		value = "fr"
	case "italian":
		value = "it"
	case "japanese":
		value = "ja"
	case "korean":
		value = "ko"
	case "portuguese":
		value = "pt"
	case "russian":
		value = "ru"
	case "chinese":
		value = "zh"
	}
	tag := canonicalLanguageTag(value)
	if tag == "" {
		return ""
	}
	return strings.ToLower(strings.Split(tag, "-")[0])
}

func normalDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func readPlistStringValues(ctx context.Context, path string, keys ...string) map[string]string {
	wanted := make(map[string]bool, len(keys))
	for _, key := range keys {
		wanted[key] = true
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 0 || info.Size() > appDisplayMetadataLimit {
		return map[string]string{}
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > appDisplayMetadataLimit {
		return map[string]string{}
	}
	if validXMLPlistData(data) {
		return xmlPlistStringValues(data, wanted)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, appDisplayMetadataTimeout)
	defer cancel()
	converted, err := exec.CommandContext(probeCtx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", "--", path).Output()
	if err != nil || probeCtx.Err() != nil || len(converted) > appDisplayMetadataLimit || !validXMLPlistData(converted) {
		return map[string]string{}
	}
	return xmlPlistStringValues(converted, wanted)
}

func xmlPlistStringValues(data []byte, wanted map[string]bool) map[string]string {
	values := map[string]string{}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	pendingKey := ""
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return values
		}
		if err != nil {
			return map[string]string{}
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			var key string
			if err := decoder.DecodeElement(&key, &start); err != nil {
				return map[string]string{}
			}
			if wanted[key] {
				pendingKey = key
			} else {
				pendingKey = ""
			}
		case "string":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return map[string]string{}
			}
			if pendingKey != "" {
				values[pendingKey] = value
				pendingKey = ""
			}
		default:
			if pendingKey != "" {
				pendingKey = ""
			}
		}
	}
}
