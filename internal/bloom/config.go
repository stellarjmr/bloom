package bloom

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	TaskOrder     []string
	Tasks         map[string]TaskConfig
	Clean         CleanConfig
	ProgressWidth int
	Color         bool
}

type TaskConfig struct {
	Enabled     bool
	Include     []string
	Exclude     []string
	InstallHint string
}

var defaultTaskOrder = []string{"brew", "cask", "amp", "yazi", "nvim", "mason", "npm"}

func DefaultTaskNames() []string {
	return append([]string(nil), defaultTaskOrder...)
}

func DefaultConfig() Config {
	return Config{
		TaskOrder:     DefaultTaskNames(),
		ProgressWidth: 24,
		Color:         true,
		Clean:         CleanConfig{Whitelist: DefaultCleanWhitelist()},
		Tasks: map[string]TaskConfig{
			"brew":    {Enabled: true},
			"cask":    {Enabled: true},
			"amp":     {Enabled: true},
			"yazi":    {Enabled: true},
			"nvim":    {Enabled: true},
			"mason":   {Enabled: true},
			"npm":     {Enabled: true},
			"cleanup": {Enabled: true},
		},
	}
}

func DefaultConfigPath() string {
	return configHome() + "/bloom/config.toml"
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			if missingErr := confirmConfigMissing(path); missingErr != nil {
				return cfg, fmt.Errorf("config %s is unavailable: %w", path, missingErr)
			}
			return cfg, nil
		}
		return cfg, err
	}
	defer file.Close()

	section := ""
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := stripComment(scanner.Text())
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return cfg, fmt.Errorf("%s:%d: expected key = value", path, lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch {
		case section == "settings":
			if err := parseSetting(&cfg, key, value); err != nil {
				return cfg, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
		case section == "tasks":
			if key != "order" {
				return cfg, fmt.Errorf("%s:%d: unsupported tasks key %q", path, lineNo, key)
			}
			order, err := parseStringArray(value)
			if err != nil {
				return cfg, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
			cfg.TaskOrder = order
		case strings.HasPrefix(section, "tasks."):
			name := strings.TrimPrefix(section, "tasks.")
			task := cfg.Tasks[name]
			if err := parseTaskSetting(&task, key, value); err != nil {
				return cfg, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
			cfg.Tasks[name] = task
		case section == "clean":
			if err := parseCleanSetting(&cfg.Clean, key, value); err != nil {
				return cfg, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
		default:
			return cfg, fmt.Errorf("%s:%d: unsupported section [%s]", path, lineNo, section)
		}
	}
	if err := scanner.Err(); err != nil {
		return cfg, err
	}
	cfg.Clean.Whitelist = effectiveCleanWhitelist(cfg.Clean.Whitelist)
	return cfg, nil
}

// confirmConfigMissing separates a config that does not exist from one that
// cannot be reached. Defaults are safe only for the first: a dangling symlink
// (a dotfiles link whose target moved, or whose volume is not mounted) or an
// ancestor that is not a directory would otherwise drop the user's settings,
// including clean whitelist entries that protect their data.
//
// The walk follows the kernel's lookup instead of a lexical clean: each
// symlink is resolved before the next component, so ".." after a link climbs
// from the link's target, exactly as open(2) does for the same path.
func confirmConfigMissing(path string) error {
	current := string(os.PathSeparator)
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		if current, err = filepath.EvalSymlinks(wd); err != nil {
			return err
		}
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == os.PathSeparator })
	for i, part := range parts {
		switch part {
		case ".":
			continue
		case "..":
			// current is fully resolved, so its lexical parent is the physical one.
			current = filepath.Dir(current)
			continue
		}
		next := filepath.Join(current, part)
		info, err := os.Lstat(next)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(next)
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%s is a broken symlink", next)
			}
			if err != nil {
				return err
			}
			if info, err = os.Stat(resolved); err != nil {
				return err
			}
			next = resolved
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("%s is not a directory", next)
		}
		current = next
	}
	return fmt.Errorf("%s exists but could not be opened", path)
}

// applyEnvironmentConfig applies environment overrides such as NO_COLOR.
// It must be applied at use time (rendering), never during LoadConfig:
// load-modify-save flows would otherwise persist the temporary override
// into the user's config file.
func applyEnvironmentConfig(cfg *Config) {
	if os.Getenv("NO_COLOR") != "" {
		cfg.Color = false
	}
}

func WriteDefaultConfig(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(DefaultConfigText()), 0o644)
}

func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(ConfigText(cfg)), 0o644)
}

func DefaultConfigText() string {
	return ConfigText(DefaultConfig())
}

func ConfigText(cfg Config) string {
	cfg.Clean.Whitelist = effectiveCleanWhitelist(cfg.Clean.Whitelist)
	var b strings.Builder
	fmt.Fprintln(&b, "# bm config")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[settings]")
	fmt.Fprintf(&b, "progress_width = %d\n", cfg.ProgressWidth)
	fmt.Fprintf(&b, "color = %s\n", formatBool(cfg.Color))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[tasks]")
	fmt.Fprintf(&b, "order = %s\n", formatStringArray(cfg.TaskOrder))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "[clean]")
	fmt.Fprintf(&b, "whitelist = %s\n", formatStringArray(cfg.Clean.Whitelist))

	for _, name := range cfg.TaskOrder {
		task := cfg.Tasks[name]
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "[tasks.%s]\n", name)
		fmt.Fprintf(&b, "enabled = %s\n", formatBool(task.Enabled))
		if taskSupportsPackages(name) {
			fmt.Fprintf(&b, "include = %s\n", formatStringArray(task.Include))
			fmt.Fprintf(&b, "exclude = %s\n", formatStringArray(task.Exclude))
		}
	}

	return b.String()
}

func SetEnabledTasks(cfg *Config, enabled []string) error {
	enabledSet := map[string]bool{}
	for _, name := range enabled {
		if !isDefaultTask(name) {
			return fmt.Errorf("unknown task %q", name)
		}
		enabledSet[name] = true
	}

	cfg.TaskOrder = DefaultTaskNames()
	if cfg.Tasks == nil {
		cfg.Tasks = map[string]TaskConfig{}
	}
	for _, name := range cfg.TaskOrder {
		task := cfg.Tasks[name]
		task.Enabled = enabledSet[name]
		cfg.Tasks[name] = task
	}
	return nil
}

func SetTaskInclude(cfg *Config, name string, include []string) error {
	if !isDefaultTask(name) {
		return fmt.Errorf("unknown task %q", name)
	}
	if !taskSupportsPackages(name) {
		return fmt.Errorf("task %q does not support package filters", name)
	}
	if cfg.Tasks == nil {
		cfg.Tasks = map[string]TaskConfig{}
	}
	task := cfg.Tasks[name]
	task.Include = uniqueStrings(include)
	task.Exclude = nil
	cfg.Tasks[name] = task
	if !containsString(cfg.TaskOrder, name) {
		cfg.TaskOrder = append(cfg.TaskOrder, name)
	}
	return nil
}

func taskSupportsPackages(name string) bool {
	switch name {
	case "brew", "cask", "yazi", "nvim", "mason", "npm":
		return true
	default:
		return false
	}
}

func isDefaultTask(name string) bool {
	for _, task := range defaultTaskOrder {
		if task == name {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func formatBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func formatStringArray(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(value))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func parseSetting(cfg *Config, key, value string) error {
	switch key {
	case "progress_width":
		width, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid progress_width")
		}
		if width < 8 || width > 80 {
			return fmt.Errorf("progress_width must be between 8 and 80")
		}
		cfg.ProgressWidth = width
	case "color":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		cfg.Color = b
	default:
		return fmt.Errorf("unsupported settings key %q", key)
	}
	return nil
}

func parseTaskSetting(task *TaskConfig, key, value string) error {
	switch key {
	case "enabled":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		task.Enabled = b
	case "include":
		items, err := parseStringArray(value)
		if err != nil {
			return err
		}
		task.Include = items
	case "exclude":
		items, err := parseStringArray(value)
		if err != nil {
			return err
		}
		task.Exclude = items
	case "install_hint":
		s, err := parseString(value)
		if err != nil {
			return err
		}
		task.InstallHint = s
	default:
		return fmt.Errorf("unsupported task key %q", key)
	}
	return nil
}

func parseCleanSetting(clean *CleanConfig, key, value string) error {
	switch key {
	case "whitelist":
		items, err := parseStringArray(value)
		if err != nil {
			return err
		}
		clean.Whitelist = normalizeCleanWhitelist(items)
	default:
		return fmt.Errorf("unsupported clean key %q", key)
	}
	return nil
}

func stripComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func parseBool(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false")
	}
}

func parseString(value string) (string, error) {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", fmt.Errorf("expected quoted string")
	}
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return "", err
	}
	return unquoted, nil
}

func parseStringArray(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != '[' || value[len(value)-1] != ']' {
		return nil, fmt.Errorf("expected string array")
	}
	body := strings.TrimSpace(value[1 : len(value)-1])
	if body == "" {
		return nil, nil
	}

	var out []string
	for _, part := range splitArray(body) {
		item, err := parseString(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func splitArray(body string) []string {
	var parts []string
	start := 0
	inString := false
	escaped := false
	for i, r := range body {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == ',' && !inString {
			parts = append(parts, body[start:i])
			start = i + 1
		}
	}
	parts = append(parts, body[start:])
	return parts
}
